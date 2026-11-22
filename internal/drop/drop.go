// Package drop reads the files that programs with no sfy session drop into a factory folder, and writes sfy's replies to them.
// A drop states its sender's name, and nothing checks that name.
package drop

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// maxSize is the largest drop file in bytes.
const maxSize = 1 << 20

// The kinds of a drop.
const (
	KindMessage = "message"
	KindTask    = "task"
)

// The kinds of a reply.
const (
	ReplyReceipt  = "receipt"
	ReplyRejected = "rejected"
	ReplyStatus   = "status"
	ReplyMessage  = "message"
)

// SenderPrefix comes before the stated name of a drop's sender wherever sfy shows it, as in drop:alerts.
// No factory name and no session name holds a colon, so a stated name never looks like a checked one.
const SenderPrefix = "drop:"

// keep is how long sfy keeps the done records and the replies.
const keep = 30 * 24 * time.Hour

// keepTmp is how long a file that a sender left in tmp/ stays, for example after a crash.
const keepTmp = 24 * time.Hour

var (
	fromName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	idName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// refused are names that a quick reader might take for a sender that sfy checked.
	refused = []string{"operator", "sfy", "lead", "main"}
)

// Drop is one parsed drop file.
type Drop struct {
	To, From, ID, Kind, Body string
}

// Sender returns the name that sfy shows for the drop's sender.
func (d Drop) Sender() string { return SenderPrefix + d.From }

// validFrom reports whether name may be the stated name of a sender.
func validFrom(name string) bool {
	return fromName.MatchString(name) && !slices.Contains(refused, name)
}

// Parse checks a drop file and returns its parts.
// On an error, the returned drop still holds From and ID when they passed their checks, so sfy can reply to the sender.
func Parse(raw []byte) (Drop, error) {
	if len(raw) > maxSize {
		return Drop{}, fmt.Errorf("the drop has more than %d bytes", maxSize)
	}
	if !utf8.Valid(raw) {
		return Drop{}, errors.New("the drop is not UTF-8")
	}
	head, body, ok := strings.Cut(string(raw), "\n\n")
	if !ok {
		return Drop{}, errors.New("the drop has no blank line after its header lines")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(head, "\n") {
		k, v, _ := strings.Cut(line, ":")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if _, seen := fields[k]; seen && isKnown(k) {
			return Drop{}, fmt.Errorf("the drop has two %s lines", k)
		}
		fields[k] = v
	}
	var d Drop
	var errs []error
	if from := fields["From"]; validFrom(from) {
		d.From = from
	} else {
		errs = append(errs, fmt.Errorf("the From name %q is not allowed. A name has 1 to 32 characters from a-z, 0-9 and -, and is not operator, sfy, lead or main", from))
	}
	if id := fields["Id"]; idName.MatchString(id) {
		d.ID = id
	} else {
		errs = append(errs, fmt.Errorf("the Id %q is not allowed. An id has 1 to 64 characters from A-Z, a-z, 0-9, ., _ and -", id))
	}
	switch to := fields["To"]; to {
	case "lead", "main":
		d.To = "lead"
	default:
		errs = append(errs, fmt.Errorf("a drop may go only to lead, and this one goes to %q", to))
	}
	switch kind := fields["Kind"]; kind {
	case "", KindMessage:
		d.Kind = KindMessage
	case KindTask:
		d.Kind = KindTask
	default:
		errs = append(errs, fmt.Errorf("the Kind %q is not message or task", kind))
	}
	d.Body = body
	if strings.TrimSpace(body) == "" {
		errs = append(errs, errors.New("the drop has no text"))
	}
	return d, errors.Join(errs...)
}

func isKnown(header string) bool {
	return slices.Contains([]string{"To", "From", "Id", "Kind"}, header)
}

// Reply is a file that sfy writes for a sender with no session, which reads it at its next run.
type Reply struct {
	// ID names the reply file. It sorts in the order that sfy wrote the replies.
	ID string
	// From is lead for an answer of the lead, and sfy otherwise.
	From string
	Kind string
	// Re is the Id of the drop that the reply answers, and empty for an answer of the lead.
	Re string
	// Task is the id of the task that the drop created, and State is that task's state.
	Task, State string
	Body        string
}

// Folder is the drop folder of one factory folder.
type Folder struct{ Home string }

func (f Folder) dir(parts ...string) string {
	return filepath.Join(append([]string{f.Home, "drop"}, parts...)...)
}

// Prepare creates tmp/ and new/, where senders write. sfy alone writes the other folders.
func (f Folder) Prepare() error {
	for _, d := range []string{f.dir("tmp"), f.dir("new")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("drop: %w", err)
		}
	}
	return nil
}

// New lists the files in new/ in name order. It skips folders and names that start with a dot.
func (f Folder) New() ([]string, error) {
	entries, err := os.ReadDir(f.dir("new"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("drop: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, f.dir("new", e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// Read returns the contents of a drop file. A file over maxSize returns maxSize+1 bytes, so Parse refuses it.
func (f Folder) Read(path string) ([]byte, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("drop: %w", err)
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, maxSize+1))
}

func (f Folder) donePath(from, id string) string { return f.dir("done", from, id+".md") }

// Seen reports whether sfy already took a drop with this sender and id.
func (f Folder) Seen(from, id string) bool {
	_, err := os.Stat(f.donePath(from, id))
	return err == nil
}

// Known reports whether sfy took a drop of this sender, so the sender reads its replies.
func (f Folder) Known(from string) bool {
	if !validFrom(from) {
		return false
	}
	st, err := os.Stat(f.dir("done", from))
	return err == nil && st.IsDir()
}

// Done moves a taken drop to done/<from>/<id>.md, which records it for the duplicate check.
func (f Folder) Done(path string, d Drop) error {
	target := f.donePath(d.From, d.ID)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("drop: %w", err)
	}
	if err := os.Rename(path, target); err != nil {
		return fmt.Errorf("drop: %w", err)
	}
	return nil
}

// Remove deletes a drop that sfy skips, such as a duplicate.
func (f Folder) Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("drop: %w", err)
	}
	return nil
}

// Reject moves a drop to rejected/ and writes the reason beside it, in a file with the extra suffix .why.
func (f Folder) Reject(path, why string) error {
	target := f.dir("rejected", filepath.Base(path))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("drop: %w", err)
	}
	if err := os.Rename(path, target); err != nil {
		return fmt.Errorf("drop: %w", err)
	}
	return writeFile(target+".why", []byte(why+"\n"))
}

// Reply writes replies/<to>/<id>.md through a temporary name that starts with a dot, and a rename.
func (f Folder) Reply(to string, r Reply) error {
	if !validFrom(to) {
		return fmt.Errorf("drop: no reply to the name %q", to)
	}
	if !idName.MatchString(r.ID) {
		return fmt.Errorf("drop: bad reply id %q", r.ID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "To: %s\nFrom: %s\nId: %s\nKind: %s\n", to, r.From, r.ID, r.Kind)
	for _, h := range [][2]string{{"Re", r.Re}, {"Task", r.Task}, {"State", r.State}} {
		if h[1] != "" {
			fmt.Fprintf(&b, "%s: %s\n", h[0], h[1])
		}
	}
	fmt.Fprintf(&b, "\n%s\n", strings.TrimRight(r.Body, "\n"))
	return writeFile(f.dir("replies", to, r.ID+".md"), []byte(b.String()))
}

// Prune removes the files in done/ and replies/ that are older than 30 days, and the files in tmp/ that are older than one day.
func (f Folder) Prune(now time.Time) error {
	return errors.Join(
		prune(f.dir("done"), now.Add(-keep)),
		prune(f.dir("replies"), now.Add(-keep)),
		prune(f.dir("tmp"), now.Add(-keepTmp)),
	)
}

// prune removes the regular files below dir whose last change came before cutoff.
func prune(dir string, cutoff time.Time) error {
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || !e.Type().IsRegular() {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(cutoff) {
			return os.Remove(path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("drop: %w", err)
	}
	return nil
}

// writeFile writes through a temporary name and a rename, so a reader never sees half a file.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("drop: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("drop: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("drop: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("drop: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("drop: %w", err)
	}
	return nil
}
