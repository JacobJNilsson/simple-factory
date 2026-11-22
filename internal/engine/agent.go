package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// SetProcess sets the task process. Without it, the engine runs the embedded default.
func (e *Engine) SetProcess(p process.Process) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.proc = p
	e.sess.proc = p
}

// Runnable refuses a valid process that this engine cannot run yet.
// A session agent keeps the name, harness and ends of a default agent, because session names and commands follow those names.
// A shell agent may have any name. A shell branch needs a run of its own agent, which the engine does not do yet.
func Runnable(p process.Process) error {
	def := process.Default()
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(p.Agents)) {
		a := p.Agents[name]
		if a.Harness == process.Shell {
			continue
		}
		d, ok := def.Agents[name]
		if !ok || d.Harness != a.Harness || d.Ends != a.Ends {
			errs = append(errs, fmt.Errorf("process: agents.%s: this sfy runs a %s agent only under a default name with the default harness and ends. Use a shell agent, or one of the default agents", name, a.Harness))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(p.Lists)) {
		if p.Lists[name].Then.Pass.Shell != nil {
			errs = append(errs, fmt.Errorf("process: lists.%s.then.pass: this sfy does not run a shell branch yet", name))
		}
	}
	if first, err := p.First(p.Start); err == nil && first.Agent != "worker" {
		errs = append(errs, fmt.Errorf("process: the start list %s must begin with the worker", p.Start))
	}
	return errors.Join(errs...)
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
func (e *Engine) passFrom(t task.Task, at process.Position, h handoff) (string, error) {
	next, target, err := e.next(t, at, process.Pass)
	if err != nil {
		return "", err
	}
	switch target.List {
	case process.Done:
		return "", e.finish(t)
	case process.Escalated:
		return "", e.escalateTask(t, fmt.Sprintf("Task %s passed %s, and the process escalates it there.", t.ID, at), true)
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
		if len(cmds) > 0 {
			return e.startShell(t, at, a, cmds, dropDir, h)
		}
		return e.passFrom(t, at, h)
	case a.Harness == process.Claude && a.Ends == process.EndsVerdict:
		return "", e.startReview(t, h.report, h.checked)
	case a.Harness == process.Claude && a.Ends == process.EndsPushed:
		return "", e.askPush(t, h.from)
	case a.Harness == process.Operator:
		return "", e.hold(t, h.from)
	case a.Harness == process.Lead:
		return "", e.approve(t, h.from, h.pushed)
	}
	return "", fmt.Errorf("task %s cannot enter %s: this sfy runs a %s agent with ends %s only as the first agent of the start list", t.ID, at, a.Harness, a.Ends)
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

// failAt moves a task back after the agent at at failed, as the list's fail branch says. A fail at max_rounds escalates.
// A failed save keeps the session of the agent that failed, so the agent can try again.
func (e *Engine) failAt(t task.Task, at process.Position, f failure) error {
	_, target, err := e.next(t, at, process.Fail)
	if err != nil {
		return err
	}
	worker := workerOf(t)
	if target.List == process.Escalated {
		t.FailReview()
		return errors.Join(e.message(worker, f.from, f.note(t)), e.escalateTask(t, f.why(t), true), f.stop())
	}
	if target.List == process.Done {
		return e.finish(t)
	}
	if first, err := e.proc.First(target.List); err != nil || !e.isWorker(first.Agent) {
		return fmt.Errorf("task %s cannot go back to list %s, because this sfy starts a list again only at the worker", t.ID, target.List)
	}
	// The worker has to fix the result, so an escalated task resumes in feedback and not in a step that no longer runs.
	if target.Round {
		t.FailCheck()
	} else {
		t.FailReview()
	}
	if t.Round >= e.proc.MaxRounds(at.List, e.cfg.ReviewRounds) {
		msgErr := e.message(worker, f.from, f.note(t))
		return errors.Join(msgErr, e.escalateTask(t, f.why(t), true), f.stop())
	}
	if err := e.save(t); err != nil {
		return err
	}
	return errors.Join(e.message(worker, f.from, f.note(t)), f.stop())
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
