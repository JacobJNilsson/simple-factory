// Package store keeps sfy state in plain files.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// ErrNotFound reports an unknown task id.
var ErrNotFound = errors.New("store: task not found")

// Message is one message between agents.
type Message struct {
	ID, From, To, Body string
	Created            time.Time
}

// Verdict is a reviewer's judgment of one round, or feedback that the operator or main sent.
type Verdict struct {
	Round int
	Pass  bool
	// Feedback is true for feedback from sfy task feedback. Round is the round that the task had when the feedback came.
	Feedback bool
	Body     string
}

// Store holds tasks, verdicts and messages. The sfy process is its only writer.
type Store interface {
	NextID() (string, error)
	Create(task.Task) error
	Task(id string) (task.Task, error)
	Tasks() ([]task.Task, error)
	// Save writes task.md and the state file, so it also replaces the file of a new task.
	Save(task.Task) error
	AddVerdict(id string, round int, pass bool, body string) error
	// AddFeedback records feedback that a task got in the given round. Verdicts returns it with the verdicts.
	AddFeedback(id string, round int, body string) error
	Send(Message) error
	Unread(agent string) ([]Message, error)
	MarkRead(agent string, ids []string) error
	// Verdicts returns the task's verdicts and feedback ordered by round. Feedback follows the verdict of its round.
	Verdicts(id string) ([]Verdict, error)
	WriteStatus(text string) error
}

type files struct{ dir string }

// Files returns a Store that keeps its files under dir.
// CONTRACT: SPEC.md keeps the store behind one Go interface so another store can replace it.
func Files(dir string) Store { return files{dir: dir} }

type stateFile struct {
	State    task.State `json:"state"`
	Before   task.State `json:"before,omitempty"`
	Round    int        `json:"round"`
	Archived bool       `json:"archived,omitempty"`
	Source   string     `json:"source"`
	Progress string     `json:"progress,omitempty"`
	Dir      string     `json:"dir,omitempty"`
	Pending  []string   `json:"pending,omitempty"`
	Reopened bool       `json:"reopened,omitempty"`
	// Pushing is the task's flag of the same name.
	Pushing bool `json:"pushing,omitempty"`
	// Branch is the task's field of the same name.
	Branch  string    `json:"branch,omitempty"`
	Updated time.Time `json:"updated"`
}

var (
	taskID   = regexp.MustCompile(`^t[0-9]+$`)
	safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// writeFile writes through a temporary name and a rename, so a reader never sees half a file.
// It syncs the file and then the folder, so a crash after the rename keeps the data.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

func (f files) taskDir(id string) (string, error) {
	if !taskID.MatchString(id) {
		return "", ErrNotFound
	}
	return filepath.Join(f.dir, "tasks", id), nil
}

// existingTaskDir returns the folder of a task that exists. A folder without state.json is no task.
func (f files) existingTaskDir(id string) (string, error) {
	d, err := f.taskDir(id)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(d, "state.json")); errors.Is(err, fs.ErrNotExist) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	return d, nil
}

// The counter file, not a folder scan, keeps an id from returning after its task is gone.
func (f files) NextID() (string, error) {
	path := filepath.Join(f.dir, "next-id")
	n := 0
	if b, err := os.ReadFile(path); err == nil {
		n, err = strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			return "", fmt.Errorf("store: read %s: %w", path, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	n++
	if err := writeFile(path, []byte(strconv.Itoa(n)+"\n")); err != nil {
		return "", err
	}
	return "t" + strconv.Itoa(n), nil
}

// Create builds the folder under a temporary name and renames it into place,
// so a crash never leaves a task folder with missing files.
func (f files) Create(t task.Task) error {
	d, err := f.taskDir(t.ID)
	if err != nil {
		return fmt.Errorf("store: invalid task id %q", t.ID)
	}
	if _, err := f.existingTaskDir(t.ID); err == nil {
		return fmt.Errorf("store: task %s already exists", t.ID)
	}
	parent := filepath.Dir(d)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := writeTask(tmp, t); err != nil {
		return err
	}
	if err := os.Rename(tmp, d); err != nil {
		return err
	}
	return syncDir(parent)
}

func (f files) Save(t task.Task) error {
	d, err := f.existingTaskDir(t.ID)
	if err != nil {
		return err
	}
	return writeTask(d, t)
}

// checkRoundTrip refuses a task whose file would read back with other fields, such as a
// Description that holds a section heading of its own.
func checkRoundTrip(t task.Task) error {
	back, err := task.Parse(t.Markdown())
	if err != nil {
		return fmt.Errorf("store: task %s does not parse back: %w", t.ID, err)
	}
	same := back.Title == t.Title && back.Description == t.Description && back.Goal == t.Goal &&
		back.Repo == t.Repo && back.Check == t.Check && back.Type == t.Type && back.Extra == t.Extra &&
		slices.Equal(back.Protected, t.Protected) && slices.Equal(back.After, t.After)
	if !same {
		return fmt.Errorf("store: task %s does not parse back to the same fields", t.ID)
	}
	return nil
}

// normalize trims the fields the way Parse reads them, so a goal with a trailing newline is no conflict.
func normalize(t task.Task) task.Task {
	t.Title = strings.TrimSpace(t.Title)
	t.Description = strings.TrimSpace(t.Description)
	t.Goal = strings.TrimSpace(t.Goal)
	t.Repo = strings.TrimSpace(t.Repo)
	t.Check = strings.TrimSpace(t.Check)
	t.Type = strings.TrimSpace(t.Type)
	t.Extra = strings.TrimSpace(t.Extra)
	var protected []string
	for _, p := range t.Protected {
		if p = strings.TrimSpace(p); p != "" {
			protected = append(protected, p)
		}
	}
	t.Protected = protected
	return t
}

func writeTask(dir string, t task.Task) error {
	t = normalize(t)
	if err := checkRoundTrip(t); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "task.md"), []byte(t.Markdown())); err != nil {
		return err
	}
	b, err := json.MarshalIndent(stateFile{
		State: t.State, Before: t.Before, Round: t.Round, Archived: t.Archived, Source: t.Source, Progress: t.Progress,
		Dir: t.Dir, Pending: t.Pending, Reopened: t.Reopened, Pushing: t.Pushing, Branch: t.Branch, Updated: t.Updated,
	}, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, "state.json"), append(b, '\n'))
}

func (f files) Task(id string) (task.Task, error) {
	d, err := f.existingTaskDir(id)
	if err != nil {
		return task.Task{}, err
	}
	md, err := os.ReadFile(filepath.Join(d, "task.md"))
	if err != nil {
		return task.Task{}, err
	}
	t, err := task.Parse(string(md))
	if err != nil {
		return task.Task{}, fmt.Errorf("store: task %s: %w", id, err)
	}
	raw, err := os.ReadFile(filepath.Join(d, "state.json"))
	if err != nil {
		return task.Task{}, err
	}
	var st stateFile
	if err := json.Unmarshal(raw, &st); err != nil {
		return task.Task{}, fmt.Errorf("store: task %s state: %w", id, err)
	}
	t.ID = id
	t.State, t.Before, t.Round, t.Archived, t.Source = st.State, st.Before, st.Round, st.Archived, st.Source
	t.Progress, t.Dir, t.Pending, t.Reopened, t.Pushing, t.Branch, t.Updated = st.Progress, st.Dir, st.Pending, st.Reopened, st.Pushing, st.Branch, st.Updated
	return t, nil
}

func (f files) Tasks() ([]task.Task, error) {
	entries, err := os.ReadDir(filepath.Join(f.dir, "tasks"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []task.Task
	for _, e := range entries {
		if !e.IsDir() || !taskID.MatchString(e.Name()) {
			continue
		}
		t, err := f.Task(e.Name())
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return number(out[i].ID) < number(out[j].ID) })
	return out, nil
}

// number returns the numeric part of a task id, or 0 for an id that has none.
func number(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "t"))
	return n
}

func (f files) AddVerdict(id string, round int, pass bool, body string) error {
	d, err := f.existingTaskDir(id)
	if err != nil {
		return err
	}
	result := "fail"
	if pass {
		result = "pass"
	}
	return writeFile(filepath.Join(d, "verdicts", fmt.Sprintf("%d-%s.md", round, result)), []byte(body))
}

func (f files) AddFeedback(id string, round int, body string) error {
	d, err := f.existingTaskDir(id)
	if err != nil {
		return err
	}
	// A task takes any number of feedbacks in one round, so a number keeps their order.
	n := 1
	if entries, err := os.ReadDir(filepath.Join(d, "verdicts")); err == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), "-feedback-") {
				n++
			}
		}
	}
	return writeFile(filepath.Join(d, "verdicts", fmt.Sprintf("%d-feedback-%04d.md", round, n)), []byte(body))
}

func (f files) inbox(agent string) (string, error) {
	if !safeName.MatchString(agent) {
		return "", fmt.Errorf("store: invalid agent name %q", agent)
	}
	return filepath.Join(f.dir, "inbox", agent), nil
}

// A message file holds a From line, a Created line, a blank line and then the body.
func (f files) Send(m Message) error {
	if !safeName.MatchString(m.ID) {
		return fmt.Errorf("store: invalid message id %q", m.ID)
	}
	d, err := f.inbox(m.To)
	if err != nil {
		return err
	}
	if strings.ContainsAny(m.From, "\r\n") {
		return fmt.Errorf("store: message from %q holds a newline", m.From)
	}
	for _, p := range []string{filepath.Join(d, m.ID+".md"), filepath.Join(d, "read", m.ID+".md")} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("store: message %s already exists", m.ID)
		}
	}
	text := fmt.Sprintf("From: %s\nCreated: %s\n\n%s", m.From, m.Created.Format(time.RFC3339Nano), m.Body)
	return writeFile(filepath.Join(d, m.ID+".md"), []byte(text))
}

func (f files) Unread(agent string) ([]Message, error) {
	d, err := f.inbox(agent)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(d)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []Message
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(d, e.Name()))
		if err != nil {
			return nil, err
		}
		m, err := parseMessage(string(raw))
		if err != nil {
			return nil, fmt.Errorf("store: message %s: %w", e.Name(), err)
		}
		m.ID, m.To = strings.TrimSuffix(e.Name(), ".md"), agent
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func parseMessage(s string) (Message, error) {
	head, body, ok := strings.Cut(s, "\n\n")
	if !ok {
		return Message{}, errors.New("no blank line after the header")
	}
	var m Message
	for _, line := range strings.Split(head, "\n") {
		switch k, v, _ := strings.Cut(line, ": "); k {
		case "From":
			m.From = v
		case "Created":
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				return Message{}, err
			}
			m.Created = t
		}
	}
	m.Body = body
	return m, nil
}

func (f files) MarkRead(agent string, ids []string) error {
	d, err := f.inbox(agent)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(d, "read"), 0o700); err != nil {
		return err
	}
	for _, id := range ids {
		if !safeName.MatchString(id) {
			return fmt.Errorf("store: invalid message id %q", id)
		}
		err := os.Rename(filepath.Join(d, id+".md"), filepath.Join(d, "read", id+".md"))
		// A message that is already read stays read, so a retry is harmless.
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (f files) WriteStatus(text string) error {
	return writeFile(filepath.Join(f.dir, "status.md"), []byte(text))
}

func (f files) Verdicts(id string) ([]Verdict, error) {
	d, err := f.existingTaskDir(id)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(d, "verdicts"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []Verdict
	for _, e := range entries {
		var v Verdict
		var result string
		if _, err := fmt.Sscanf(strings.TrimSuffix(e.Name(), ".md"), "%d-%s", &v.Round, &result); err != nil || (result != "pass" && result != "fail" && !strings.HasPrefix(result, "feedback-")) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(d, "verdicts", e.Name()))
		if err != nil {
			return nil, err
		}
		v.Pass, v.Feedback, v.Body = result == "pass", strings.HasPrefix(result, "feedback-"), string(body)
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Round != out[j].Round {
			return out[i].Round < out[j].Round
		}
		return !out[i].Feedback && out[j].Feedback
	})
	return out, nil
}
