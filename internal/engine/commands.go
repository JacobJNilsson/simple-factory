package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

var errEmptyVerdict = errors.New("a verdict needs the file body, and the body is empty")

// text returns the body when it is given, and the joined arguments otherwise.
func text(req protocol.Request, args []string) string {
	if req.Body != "" {
		return req.Body
	}
	return strings.Join(args, " ")
}

// load returns the task that the first argument names.
func (e *Engine) load(req protocol.Request) (task.Task, error) {
	if len(req.Args) == 0 {
		return task.Task{}, errors.New("this command needs a task id")
	}
	t, err := e.st.Task(req.Args[0])
	if errors.Is(err, store.ErrNotFound) {
		return task.Task{}, fmt.Errorf("no task %q", req.Args[0])
	}
	return t, err
}

// need fails unless the task is in one of the states.
func need(t task.Task, states ...task.State) error {
	for _, s := range states {
		if t.State == s {
			return nil
		}
	}
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = string(s)
	}
	return fmt.Errorf("task %s is %s, and this command needs %s", t.ID, t.State, strings.Join(names, " or "))
}

func (e *Engine) taskNew(req protocol.Request) (string, error) {
	t, err := task.Parse(req.Body)
	if err != nil {
		return "", err
	}
	t.State, t.Source = task.New, "operator"
	if req.Caller.Agent == "main" {
		t.State, t.Source = task.Queued, "main"
	}
	if t.ID, err = e.st.NextID(); err != nil {
		return "", err
	}
	t.Updated = e.c.Now()
	if err := e.st.Create(t); err != nil {
		return "", err
	}
	if err := e.refreshStatus(); err != nil {
		return "", err
	}
	warning := ""
	if t.Repo != "" {
		if w := e.l.TrustWarning(e.w.RepoFolder(t.Repo)); w != "" {
			warning = " Warning: " + w
		}
	}
	if t.State == task.New {
		note := fmt.Sprintf("New task %s: %s. Prepare it, then queue or cancel it. Run: sfy task show %s", t.ID, t.Title, t.ID) + warning
		if err := e.message("main", "sfy", note); err != nil {
			return "", err
		}
	}
	if warning != "" {
		// The id stays alone on the first line, so a program that reads the id still works.
		return t.ID + "\n" + strings.TrimSpace(warning), nil
	}
	return t.ID, nil
}

func (e *Engine) taskList(protocol.Request) (string, error) {
	tasks, err := e.st.Tasks()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, t := range tasks {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", t.ID, t.State, t.Title)
	}
	return b.String(), nil
}

func (e *Engine) taskShow(req protocol.Request) (string, error) {
	t, err := e.load(req)
	if err != nil {
		return "", err
	}
	verdicts, err := e.st.Verdicts(t.ID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "State: %s\nRound: %d\nProgress: %s\n\n%s", t.State, t.Round, t.Progress, t.Markdown())
	for _, v := range verdicts {
		result := "fail"
		if v.Pass {
			result = "pass"
		}
		fmt.Fprintf(&b, "\n## Verdict, round %d: %s\n\n%s\n", v.Round, result, v.Body)
	}
	return b.String(), nil
}

func (e *Engine) taskEdit(req protocol.Request) (string, error) {
	t, err := e.load(req)
	if err != nil {
		return "", err
	}
	if err := need(t, task.New, task.Queued, task.Working, task.Review, task.Feedback, task.Escalated); err != nil {
		return "", err
	}
	edited, err := task.Parse(req.Body)
	if err != nil {
		return "", err
	}
	if t.Dir != "" && edited.Repo != t.Repo {
		return "", fmt.Errorf("task %s has a workspace, so its repo is fixed", t.ID)
	}
	if t.Dir != "" && !slices.Equal(edited.Protected, t.Protected) {
		return "", fmt.Errorf("task %s has a workspace, so its protected paths are fixed", t.ID)
	}
	edited.ID, edited.State, edited.Round, edited.Source = t.ID, t.State, t.Round, t.Source
	edited.Progress, edited.Dir = t.Progress, t.Dir
	if err := e.save(edited); err != nil {
		return "", err
	}
	if t.Dir == "" {
		return "", nil
	}
	return "", e.noteWorker(t)
}

// noteWorker tells a worker to read its changed task file. A pane that no longer takes the note is no failure, because the message waits in the inbox.
func (e *Engine) noteWorker(t task.Task) error {
	name := "worker-" + t.ID
	if err := e.storeMessage(name, "main", "The task file for "+t.ID+" changed. Run: sfy task show "+t.ID); err != nil {
		return err
	}
	if !e.agent(name).waiting {
		return nil
	}
	return e.ignoreGone(name, e.typeNote(name, "task file changed"))
}

func (e *Engine) taskQueue(req protocol.Request) (string, error) {
	t, err := e.load(req)
	if err != nil {
		return "", err
	}
	if err := need(t, task.New); err != nil {
		return "", err
	}
	t.State = task.Queued
	return "", e.save(t)
}

func (e *Engine) taskCancel(req protocol.Request) (string, error) {
	t, err := e.load(req)
	if err != nil {
		return "", err
	}
	if err := need(t, task.New, task.Queued, task.Working, task.Review, task.Feedback, task.Escalated); err != nil {
		return "", err
	}
	t.State, t.Before = task.Cancelled, ""
	if err := e.save(t); err != nil {
		return "", err
	}
	return "", e.cleanup(t)
}

// resume returns an escalated task to the state it had before, and keeps its round.
func (e *Engine) resume(req protocol.Request) (string, error) {
	t, err := e.load(req)
	if err != nil {
		return "", err
	}
	if err := need(t, task.Escalated); err != nil {
		return "", err
	}
	if t.Before == "" {
		return "", fmt.Errorf("task %s has no recorded state to return to", t.ID)
	}
	t.State, t.Before = t.Before, ""
	if err := e.save(t); err != nil {
		return "", err
	}
	if !active(t) {
		return "", nil
	}
	note := fmt.Sprintf("Task %s is resumed. Go on, and report as usual.", t.ID)
	names := []string{"worker-" + t.ID}
	if t.State == task.Review {
		names = append(names, reviewerOf(t))
	}
	var errs []error
	for _, name := range names {
		if err := e.storeMessage(name, "sfy", note); err != nil {
			errs = append(errs, err)
			continue
		}
		if e.agent(name).waiting {
			errs = append(errs, e.ignoreGone(name, e.typeNote(name, "new message")))
		}
	}
	return "", errors.Join(errs...)
}

func (e *Engine) send(req protocol.Request) (string, error) {
	if len(req.Args) == 0 {
		return "", errors.New("send needs an agent name")
	}
	body := text(req, req.Args[1:])
	if body == "" {
		return "", errors.New("send needs a text")
	}
	if err := e.live(req.Args[0]); err != nil {
		return "", err
	}
	return "", e.message(req.Args[0], req.Caller.Agent, body)
}

// live fails unless the named agent runs now: main, a worker of a task in work, or a reviewer of the task's current round.
func (e *Engine) live(agent string) error {
	if agent == "main" {
		return nil
	}
	k, _ := kindOf(agent)
	var id string
	switch k {
	case worker:
		id = strings.TrimPrefix(agent, "worker-")
	case reviewer:
		id = strings.Split(agent, "-")[1]
	default:
		return fmt.Errorf("%q is not an agent that runs", agent)
	}
	t, err := e.st.Task(id)
	if err != nil {
		return fmt.Errorf("%q is not an agent that runs", agent)
	}
	if k == worker {
		if err := need(t, task.Working, task.Review, task.Feedback); err != nil {
			return fmt.Errorf("%s does not run: %w", agent, err)
		}
		return nil
	}
	if t.State != task.Review || agent != reviewerOf(t) {
		return fmt.Errorf("%s does not run", agent)
	}
	return nil
}

// worker returns the task of the calling worker, which may report only on its own task.
func (e *Engine) worker(req protocol.Request, states ...task.State) (task.Task, error) {
	t, err := e.st.Task(req.Caller.Task)
	if err != nil {
		return task.Task{}, fmt.Errorf("no task %q for %s", req.Caller.Task, req.Caller.Agent)
	}
	if req.Caller.Agent != "worker-"+t.ID {
		return task.Task{}, fmt.Errorf("%s may report only on its own task", req.Caller.Agent)
	}
	return t, need(t, states...)
}

func (e *Engine) reportDone(req protocol.Request) (string, error) {
	t, err := e.worker(req, task.Working, task.Feedback)
	if err != nil {
		return "", err
	}
	before := t
	t.State = task.Review
	t.Round++
	if err := e.save(t); err != nil {
		return "", err
	}
	prompt := fmt.Sprintf("Review this task.\n\n%s\nThe worker reports:\n\n%s\n", t.Markdown(), text(req, req.Args))
	err = e.l.Start(SessionSpec{
		Name: reviewerOf(t), Kind: "reviewer", Task: t.ID, Dir: t.Dir,
		Model: e.cfg.Models.Reviewer, Prompt: prompt, Protected: t.Protected,
	})
	if err != nil {
		// The worker can retry only if the task is back in its old state.
		if rerr := e.save(before); rerr != nil {
			return "", errors.Join(err, rerr)
		}
		return "", err
	}
	e.agents[reviewerOf(t)] = &agentState{}
	return "", nil
}

// reportBlocked lets a worker or a reviewer tell the main agent that it cannot go on.
func (e *Engine) reportBlocked(req protocol.Request) (string, error) {
	var t task.Task
	var err error
	if strings.HasPrefix(req.Caller.Agent, "review-") {
		t, err = e.reviewing(req)
	} else {
		t, err = e.worker(req, task.Working, task.Feedback)
	}
	if err != nil {
		return "", err
	}
	note := fmt.Sprintf("Task %s is blocked: %s", t.ID, text(req, req.Args))
	return "", e.message("main", req.Caller.Agent, note)
}

func (e *Engine) reportProgress(req protocol.Request) (string, error) {
	t, err := e.worker(req, task.Working, task.Feedback)
	if err != nil {
		return "", err
	}
	t.Progress = text(req, req.Args)
	return "", e.save(t)
}

func reviewerOf(t task.Task) string { return fmt.Sprintf("review-%s-%d", t.ID, t.Round) }

// reviewing returns the task in review that the calling reviewer judges.
func (e *Engine) reviewing(req protocol.Request) (task.Task, error) {
	t, err := e.st.Task(req.Caller.Task)
	if err != nil {
		return task.Task{}, fmt.Errorf("no task %q for %s", req.Caller.Task, req.Caller.Agent)
	}
	if err := need(t, task.Review); err != nil {
		return task.Task{}, err
	}
	if req.Caller.Agent != reviewerOf(t) {
		return task.Task{}, fmt.Errorf("%s may judge only round %d of task %s", req.Caller.Agent, t.Round, t.ID)
	}
	return t, nil
}

func (e *Engine) verdictPass(req protocol.Request) (string, error) {
	t, err := e.reviewing(req)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(req.Body) == "" {
		return "", errEmptyVerdict
	}
	if err := e.st.AddVerdict(t.ID, t.Round, true, req.Body); err != nil {
		return "", err
	}
	t.State = task.Done
	if err := e.save(t); err != nil {
		return "", err
	}
	if err := e.message("main", req.Caller.Agent, fmt.Sprintf("Task %s is done.", t.ID)); err != nil {
		return "", err
	}
	return "", e.cleanup(t)
}

func (e *Engine) verdictFail(req protocol.Request) (string, error) {
	t, err := e.reviewing(req)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(req.Body) == "" {
		return "", errEmptyVerdict
	}
	if err := e.st.AddVerdict(t.ID, t.Round, false, req.Body); err != nil {
		return "", err
	}
	reviewer := reviewerOf(t)
	note := fmt.Sprintf("Review round %d of task %s failed. Fix the result, then run sfy report done.\n\n%s", t.Round, t.ID, req.Body)
	// The worker has to fix the result, so an escalated task resumes in feedback and not in a review that no longer runs.
	t.State = task.Feedback
	if t.Round >= e.cfg.ReviewRounds {
		msgErr := e.message("worker-"+t.ID, req.Caller.Agent, note)
		err := e.escalateTask(t, fmt.Sprintf("Task %s failed review round %d.", t.ID, t.Round), true)
		return "", errors.Join(msgErr, err, e.stop(reviewer))
	}
	if err := e.save(t); err != nil {
		return "", err
	}
	return "", errors.Join(e.message("worker-"+t.ID, req.Caller.Agent, note), e.stop(reviewer))
}

func (e *Engine) inbox(req protocol.Request) (string, error) {
	msgs, err := e.st.Unread(req.Caller.Agent)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		fmt.Fprintf(&b, "From %s:\n%s\n\n", m.From, m.Body)
		ids = append(ids, m.ID)
	}
	return b.String(), e.st.MarkRead(req.Caller.Agent, ids)
}

func (e *Engine) escalate(req protocol.Request) (string, error) {
	t, err := e.load(req)
	if err != nil {
		return "", err
	}
	if err := need(t, task.New, task.Queued, task.Working, task.Review, task.Feedback); err != nil {
		return "", err
	}
	return "", e.escalateTask(t, text(req, req.Args[1:]), false)
}

// escalateTask moves a task to escalated and draws the operator's attention.
// It also tells main, unless main escalated the task itself and already knows.
func (e *Engine) escalateTask(t task.Task, why string, tellMain bool) error {
	t.Before, t.State = t.State, task.Escalated
	if err := e.save(t); err != nil {
		return err
	}
	if tellMain {
		if err := e.message("main", "sfy", fmt.Sprintf("Task %s is escalated. %s", t.ID, why)); err != nil {
			return err
		}
	}
	return e.l.Notify("main", fmt.Sprintf("Task %s needs the operator. %s", t.ID, why))
}

func (e *Engine) status(protocol.Request) (string, error) {
	tasks, err := e.st.Tasks()
	if err != nil {
		return "", err
	}
	return store.RenderStatus(tasks), nil
}

// maxReminders is how many turns in a row without a report get a reminder before main hears of it.
const maxReminders = 2

// runs reports whether a hook comes from a session that sfy still counts as running.
func (e *Engine) runs(name string) bool {
	return name == "main" || e.live(name) == nil
}

// hookTurnEnded runs when an agent's turn ends and its session waits for input.
func (e *Engine) hookTurnEnded(req protocol.Request) (string, error) {
	name := req.Caller.Agent
	s := e.agent(name)
	// The pane waits even when the task does not run. A later message must find that out.
	s.waiting, s.hookSince = true, true
	if !e.runs(name) {
		return "", nil
	}
	var errs []error
	if k, _ := kindOf(name); (k == worker || k == reviewer) && !s.reported {
		errs = append(errs, e.remind(name, s))
	}
	s.reported = false
	unread, err := e.st.Unread(name)
	if err != nil {
		return "", errors.Join(append(errs, err)...)
	}
	if len(unread) > 0 {
		errs = append(errs, e.typeNote(name, "turn ended"))
	}
	return "", errors.Join(errs...)
}

// hookIdle runs when Claude Code reports that a session waits for input.
// It types no reminder, because no turn just ended.
func (e *Engine) hookIdle(req protocol.Request) (string, error) {
	name := req.Caller.Agent
	s := e.agent(name)
	s.waiting, s.hookSince = true, true
	if !e.runs(name) {
		return "", nil
	}
	unread, err := e.st.Unread(name)
	if err != nil || len(unread) == 0 {
		return "", err
	}
	return "", e.typeNote(name, "idle")
}

// remind types the reminder for a missing report. After maxReminders it tells main once and types nothing.
func (e *Engine) remind(name string, s *agentState) error {
	s.reminders++
	if s.reminders > maxReminders {
		if s.reminders > maxReminders+1 {
			return nil
		}
		return e.message("main", "sfy", fmt.Sprintf("%s ended %d turns without a report.", name, maxReminders))
	}
	s.waiting, s.hookSince = false, false
	return e.l.Send(name, noReportNote)
}

// hookPermission runs when an agent's permission prompt opens.
// A worker or a reviewer never waits at a prompt. The hook denies the call and records it in the log.
// Main hears nothing, so a denial does not wake it.
func (e *Engine) hookPermission(req protocol.Request) (string, error) {
	name := req.Caller.Agent
	runs := e.runs(name)
	if name == "main" {
		if !runs {
			return "", nil
		}
		e.agent(name).hookSince = true
		return "", e.l.Notify("main", "Main waits at a permission prompt. "+permissionRequest(req.Body))
	}
	// A stale agent of a finished task gets the denial too, because its prompt would wait for nobody.
	if runs {
		e.logf("denied tool call of %s. %s", name, permissionRequest(req.Body))
	}
	return permissionDenial, nil
}

// permissionDenial is the PermissionRequest hook reply that denies a call and tells the agent why.
const permissionDenial = `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":` +
	`"sfy denied this tool call. Change files with the Edit and Write tools inside your folder. ` +
	`Run only commands that work in the sandbox. If you need anything else, run sfy report blocked <what and why>."}}}`

// permissionRequest names the tool and its main input from a permission hook payload.
// A body that is not a payload with a tool name stays as it is.
func permissionRequest(body string) string {
	var p struct {
		Tool  string          `json:"tool_name"`
		Input json.RawMessage `json:"tool_input"`
	}
	if json.Unmarshal([]byte(body), &p) != nil || p.Tool == "" {
		return body
	}
	var in map[string]any
	_ = json.Unmarshal(p.Input, &in)
	label, key := "Input", ""
	switch p.Tool {
	case "Bash":
		label, key = "Command", "command"
	case "Edit", "Write", "Read":
		label, key = "File", "file_path"
	}
	if v, ok := in[key].(string); ok && v != "" {
		return fmt.Sprintf("Tool: %s. %s: %s", p.Tool, label, v)
	}
	if len(p.Input) == 0 {
		return fmt.Sprintf("Tool: %s", p.Tool)
	}
	compact := &bytes.Buffer{}
	if json.Compact(compact, p.Input) != nil {
		return fmt.Sprintf("Tool: %s", p.Tool)
	}
	return fmt.Sprintf("Tool: %s. Input: %s", p.Tool, compact)
}
