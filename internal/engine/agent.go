package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// SetProcess sets the task process. Without it, the engine runs the embedded default.
// A running task keeps its position and follows the new process from its next agent.
func (e *Engine) SetProcess(p process.Process) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.proc = p
	e.sess.proc = p
}

// handoff is what an agent that ended passes to the agent after it.
type handoff struct {
	// from is the session that ended, which signs the notes that follow.
	from string
	// report is the worker's report, which the reviewer gets.
	report string
	// checked is true when sfy ran the task's check and it passed.
	checked bool
	// pushed is true when the worker pushed the branch.
	pushed bool
}

// facts returns what the branches of the process may ask about a task. Only a task with a repo asks the repo for its remote.
func (e *Engine) facts(t task.Task) (process.Facts, error) {
	f := process.Facts{Repo: t.Repo != "", Hold: t.HoldsPush(), Branch: t.Existing != ""}
	if t.Repo == "" {
		return f, nil
	}
	remote, err := e.w.HasRemote(t)
	f.Remote = remote
	return f, err
}

// next returns where a task goes after the agent at at ended. It asks for the facts only when the branch needs them.
func (e *Engine) next(t task.Task, at process.Position, outcome process.Outcome) (process.Position, process.Target, error) {
	var f process.Facts
	if outcome == process.Pass && e.proc.Last(at) && e.proc.Lists[at.List].Then.Pass.Cases != nil {
		var err error
		if f, err = e.facts(t); err != nil {
			return process.Position{}, process.Target{}, err
		}
	}
	return e.proc.Next(at, outcome, f, "")
}

// passFrom moves a task on after the agent at at passed. It returns the answer of a shell agent that starts.
// A list whose pass branch is a shell agent runs that agent, and its output picks the target.
func (e *Engine) passFrom(t task.Task, at process.Position, h handoff) (string, error) {
	if branch := e.proc.Lists[at.List].Then.Pass.Shell; branch != nil && e.proc.Last(at) {
		return e.startBranch(t, at, *branch, h)
	}
	next, target, err := e.next(t, at, process.Pass)
	if err != nil {
		return "", err
	}
	return e.passTo(t, at, next, target, h)
}

// passTo moves a task to the target of a passed agent or list. next is the first agent of the target list.
func (e *Engine) passTo(t task.Task, at, next process.Position, target process.Target, h handoff) (string, error) {
	switch target.List {
	case process.Done:
		return "", e.finish(t)
	case process.Escalated:
		return "", e.escalateTask(t, fmt.Sprintf("Task %s passed %s, and the process escalates it there.", t.ID, at), true)
	}
	if target.Round {
		t.CountRound()
		if err := e.save(t); err != nil {
			return "", err
		}
	}
	return e.enter(t, next, h)
}

// enter starts the agent at at by its harness. A shell agent with nothing to run passes at once.
func (e *Engine) enter(t task.Task, at process.Position, h handoff) (string, error) {
	a := e.proc.Agents[at.Agent]
	switch {
	case a.Harness == process.Shell:
		cmds, dropDir, err := e.shellRun(t, a)
		if err != nil {
			return "", err
		}
		// A reviewer that passed has nothing left to judge, so its session ends before the shell agent runs.
		if err := e.endRound(t, h.from); err != nil {
			return "", err
		}
		if len(cmds) > 0 {
			return e.startShell(t, at, a, cmds, dropDir, h)
		}
		return e.passFrom(t, at, h)
	case a.Harness == process.Claude && a.Ends == process.EndsVerdict:
		return "", e.startReview(t, at, h.report, h.checked)
	case a.Harness == process.Claude && a.Ends == process.EndsPushed:
		return "", e.askPush(t, at, h.from)
	case a.Harness == process.Claude:
		return "", e.workOn(t, at, h)
	case a.Harness == process.Operator:
		return "", e.hold(t, at)
	case a.Harness == process.Lead:
		return "", e.approve(t, at, h.pushed)
	}
	return "", fmt.Errorf("task %s cannot enter %s: sfy has no %s harness", t.ID, at, a.Harness)
}

// endRound ends the session of a reviewer that passed. A worker's session stays, because a later agent may run in it.
func (e *Engine) endRound(t task.Task, from string) error {
	if id, ok := e.sess.parse(from); ok && id.task == t.ID && id.kind == reviewer {
		return e.sess.end(from)
	}
	return nil
}

// workOn moves a task to a claude agent that ends at done after another agent. Its session gets a message when it runs, and starts otherwise.
func (e *Engine) workOn(t task.Task, at process.Position, h handoff) error {
	running := t.State.Active()
	name := e.sess.nameOf(t, at.Agent)
	before, pending := t, t.Pending
	t.StartWork(t.Dir)
	t.MoveTo(at)
	if err := e.save(t); err != nil {
		return err
	}
	text := e.firstMessage(t, at, h, pending)
	if running && !e.sess.isGone(name) {
		return e.message(name, h.from, text)
	}
	if err := e.sess.start(t, at.Agent, text); err != nil {
		return errors.Join(err, e.save(before))
	}
	return nil
}

// firstMessage returns what a claude agent gets when the task reaches it after another agent.
// It holds the task file, the result of the agent before or every result, the feedback that no worker read, and the agent's message key.
func (e *Engine) firstMessage(t task.Task, at process.Position, h handoff, pending []string) string {
	a := e.proc.Agents[at.Agent]
	var b strings.Builder
	fmt.Fprintf(&b, "Task %s goes on at %s. Do your part of this task.\n\n%s", t.ID, at, t.Markdown())
	if a.Receives == process.ReceivesAll {
		if results, err := e.st.Results(t.ID); err == nil {
			for _, r := range results {
				fmt.Fprintf(&b, "\nThe result of %s:\n\n%s\n", r.Agent, strings.TrimRight(r.Body, "\n"))
			}
		}
	} else if h.report != "" {
		fmt.Fprintf(&b, "\nThe agent before you reports:\n\n%s\n", h.report)
	}
	for _, f := range pending {
		b.WriteString("\nFeedback:\n\n" + f + "\n")
	}
	if a.Message != "" {
		b.WriteString("\n" + a.Message + "\n")
	}
	return b.String()
}

// failure is what failAt needs to tell the worker and the lead.
type failure struct {
	// from signs the note to the worker.
	from string
	// note gives the worker's message for the round that the task has after the branch.
	note func(task.Task) string
	// why gives the reason of an escalation.
	why func(task.Task) string
	// end stops the session of the agent that failed. It is nil when no session runs.
	end func() error
}

func (f failure) stop() error {
	if f.end == nil {
		return nil
	}
	return f.end()
}

// failAt moves a task back after the agent at at failed, as the list's fail branch says.
func (e *Engine) failAt(t task.Task, at process.Position, f failure) error {
	_, target, err := e.next(t, at, process.Fail)
	if err != nil {
		return err
	}
	return e.failTo(t, at, target, f)
}

// failTo moves a task to the fail target of the list at at. A fail at max_rounds escalates.
// A target list that starts with a claude agent that ends at done sends the failure to that agent. Any other list starts at its first agent.
// A failed save keeps the session of the agent that failed, so the agent can try again.
func (e *Engine) failTo(t task.Task, at process.Position, target process.Target, f failure) error {
	if target.List == process.Escalated {
		t.FailReview()
		t.MoveTo(e.workerPosition(t))
		return errors.Join(e.message(e.workerOf(t), f.from, f.note(t)), e.escalateTask(t, f.why(t), true), f.stop())
	}
	if target.List == process.Done {
		return e.finish(t)
	}
	first, err := e.proc.First(target.List)
	if err != nil {
		return err
	}
	if !e.isWorker(first.Agent) {
		return e.restartAt(t, at, first, target, f)
	}
	// Sessions end at an operator or a lead agent, so a task that failed after one needs a new worker.
	ended := t.State == task.Held || t.State == task.Approved
	// The worker has to fix the result, so an escalated task resumes in feedback and not in a step that no longer runs.
	if target.Round {
		t.FailCheck()
	} else {
		t.FailReview()
	}
	t.MoveTo(first)
	worker := e.sess.nameOf(t, first.Agent)
	if t.Round >= e.proc.MaxRounds(at.List, e.cfg.ReviewRounds) {
		msgErr := e.message(worker, f.from, f.note(t))
		return errors.Join(msgErr, e.escalateTask(t, f.why(t), true), f.stop())
	}
	if err := e.save(t); err != nil {
		return err
	}
	if ended {
		return errors.Join(e.sess.start(t, first.Agent, f.note(t)), f.stop())
	}
	return errors.Join(e.message(worker, f.from, f.note(t)), f.stop())
}

// restartAt moves a task that failed at at to the first agent of a list that does not start with a worker.
func (e *Engine) restartAt(t task.Task, at, first process.Position, target process.Target, f failure) error {
	if target.Round {
		t.CountRound()
	}
	if t.Round >= e.proc.MaxRounds(at.List, e.cfg.ReviewRounds) {
		t.FailReview()
		t.MoveTo(e.workerPosition(t))
		return errors.Join(e.escalateTask(t, f.why(t), true), f.stop())
	}
	if err := f.stop(); err != nil {
		return err
	}
	_, err := e.enter(t, first, handoff{from: f.from, report: f.note(t)})
	return err
}

// workerPosition returns the position of the task's worker: its agent in the task's list, or the first agent of its start list.
func (e *Engine) workerPosition(t task.Task) process.Position {
	at := t.Position()
	agent := e.sess.sessionAgent(t, false)
	if agents, err := e.proc.Flatten(at.List); err == nil && slices.Contains(agents, agent) {
		return process.Position{List: at.List, Agent: agent}
	}
	first, _ := e.proc.First(e.sess.startList(t))
	return first
}

// isWorker reports whether an agent is a claude agent that ends at done, which runs in the worker's session.
func (e *Engine) isWorker(name string) bool {
	a := e.proc.Agents[name]
	return a.Harness == process.Claude && a.Ends == process.EndsDone
}

// finish completes a task whose process reached done, as sfy task complete does.
func (e *Engine) finish(t task.Task) error {
	t.Complete()
	if err := e.save(t); err != nil {
		return err
	}
	e.replyEnd(t)
	return e.cleanup(t)
}

// record stores an agent's result in the task's results folder. A failed write goes to the log, because the handoff already holds the text.
func (e *Engine) record(id, agent, body string) {
	if _, err := e.st.AddResult(id, agent, body); err != nil {
		e.logf("result of %s for task %s not written: %v", agent, id, err)
	}
}
