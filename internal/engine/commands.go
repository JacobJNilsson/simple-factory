package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/JacobJNilsson/factory/internal/protocol"
	"github.com/JacobJNilsson/factory/internal/store"
	"github.com/JacobJNilsson/factory/internal/task"
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
	if t.State == task.New {
		note := fmt.Sprintf("New task %s: %s. Prepare it, then queue or cancel it. Run: factory task show %s", t.ID, t.Title, t.ID)
		if err := e.message("main", "factory", note); err != nil {
			return "", err
		}
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
	if err := need(t, task.New); err != nil {
		return "", err
	}
	edited, err := task.Parse(req.Body)
	if err != nil {
		return "", err
	}
	edited.ID, edited.State, edited.Round, edited.Source = t.ID, t.State, t.Round, t.Source
	edited.Progress, edited.Dir = t.Progress, t.Dir
	return "", e.save(edited)
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
	t.State = task.Cancelled
	if err := e.save(t); err != nil {
		return "", err
	}
	return "", e.cleanup(t)
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
		Model: e.cfg.Models.Reviewer, Prompt: prompt,
	})
	if err != nil {
		// The worker can retry only if the task is back in its old state.
		if rerr := e.save(before); rerr != nil {
			return "", errors.Join(err, rerr)
		}
		return "", err
	}
	return "", nil
}

func (e *Engine) reportBlocked(req protocol.Request) (string, error) {
	t, err := e.worker(req, task.Working, task.Feedback)
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
	if t.Round >= e.cfg.ReviewRounds {
		err := e.escalateTask(t, fmt.Sprintf("Task %s failed review round %d.", t.ID, t.Round))
		return "", errors.Join(err, e.stop(reviewer))
	}
	t.State = task.Feedback
	if err := e.save(t); err != nil {
		return "", err
	}
	note := fmt.Sprintf("Review round %d of task %s failed. Fix the result, then run factory report done.\n\n%s", t.Round, t.ID, req.Body)
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
	return "", e.escalateTask(t, text(req, req.Args[1:]))
}

// escalateTask moves a task to escalated, tells main and draws the operator's attention.
func (e *Engine) escalateTask(t task.Task, why string) error {
	t.State = task.Escalated
	if err := e.save(t); err != nil {
		return err
	}
	if err := e.message("main", "factory", fmt.Sprintf("Task %s is escalated. %s", t.ID, why)); err != nil {
		return err
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
