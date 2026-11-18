package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// CheckResult is the result of one run of a task's check command.
type CheckResult struct {
	// Output is the kept tail of the command's stdout and stderr.
	Output string
	// Cut is the number of bytes that sfy dropped from the start of the output.
	Cut int
	// Err is nil when the command exited with 0. Otherwise it holds the exit error, the start error or the timeout.
	Err error
	// TimedOut is true when the timeout stopped the command.
	TimedOut bool
	Took     time.Duration
}

// Checker runs the check command of a task.
type Checker interface {
	// Start runs command in dir on another goroutine and calls done once with the result.
	// It returns at once and never calls done before it returns.
	Start(dir, command string, done func(CheckResult))
}

// checkRun is a check that runs for a task.
type checkRun struct {
	seq     int
	command string
	// report is the worker's report, which the reviewer gets when the check passes.
	report string
}

// SetChecker sets how the engine runs checks. Without it, a task with a check goes to review at once.
func (e *Engine) SetChecker(c Checker) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ck = c
}

// shownTasks returns the tasks with Checking set on those whose check runs.
func (e *Engine) shownTasks() ([]task.Task, error) {
	tasks, err := e.st.Tasks()
	for i := range tasks {
		_, tasks[i].Checking = e.checking[tasks[i].ID]
	}
	return tasks, err
}

// startCheck starts the check of a task and answers the worker at once. checkDone takes the result.
func (e *Engine) startCheck(t task.Task, report string) (string, error) {
	if _, running := e.checking[t.ID]; running {
		return "", fmt.Errorf("sfy already runs the check of task %s. Wait for its result", t.ID)
	}
	e.checkSeq++
	seq := e.checkSeq
	e.checking[t.ID] = checkRun{seq: seq, command: t.Check, report: report}
	e.ck.Start(t.Dir, t.Check, func(r CheckResult) { e.checkDone(t.ID, seq, r) })
	if err := e.refreshStatus(); err != nil {
		e.logf("status of task %s not written: %v", t.ID, err)
	}
	return fmt.Sprintf("sfy runs the check now: %s. Its result comes as a message. When it passes, the review starts.", t.Check), nil
}

// checkDone takes the result of a check. It ignores a check that endSessions or startWorker dropped.
func (e *Engine) checkDone(id string, seq int, r CheckResult) {
	e.mu.Lock()
	defer e.mu.Unlock()
	run, ok := e.checking[id]
	if !ok || run.seq != seq {
		return
	}
	delete(e.checking, id)
	verdict := "passed"
	if r.Err != nil {
		verdict = "failed"
	}
	e.logf("check of task %s %s after %s", id, verdict, r.Took.Round(time.Millisecond))
	if err := e.applyCheck(id, run, r); err != nil {
		e.logf("check of task %s: %v", id, err)
	}
	if err := e.refreshStatus(); err != nil {
		e.logf("status of task %s not written: %v", id, err)
	}
}

// applyCheck moves the task on after its check ended.
func (e *Engine) applyCheck(id string, run checkRun, r CheckResult) error {
	t, err := e.st.Task(id)
	if err != nil {
		return err
	}
	if t.State != task.Working && t.State != task.Feedback {
		e.logf("check of task %s dropped, because the task is %s", id, t.State)
		return nil
	}
	worker := "worker-" + id
	if t.Check != run.command {
		return e.message(worker, "sfy", fmt.Sprintf("The check of task %s changed while it ran. Run sfy report done again.", id))
	}
	if r.Err == nil {
		if err := e.startReview(t, run.report, true); err != nil {
			return errors.Join(err, e.message(worker, "sfy", fmt.Sprintf("The check passed, but the reviewer did not start: %v. Run sfy report done again.", err)))
		}
		return nil
	}
	// A failed check counts as a review round, so a worker that never fixes it ends up escalated.
	t.Round++
	t.State = task.Feedback
	note := e.checkFailedNote(t, run.command, r)
	if t.Round >= e.cfg.ReviewRounds {
		msgErr := e.message(worker, "sfy", note)
		return errors.Join(msgErr, e.escalateTask(t, fmt.Sprintf("Task %s failed its check in round %d.", t.ID, t.Round), true))
	}
	if err := e.save(t); err != nil {
		return err
	}
	return e.message(worker, "sfy", note)
}

// checkFailedNote tells the worker that its check failed, with the command, the error and the kept output.
func (e *Engine) checkFailedNote(t task.Task, command string, r CheckResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The check of task %s failed, so no review started. This counts as review round %d of %d. Fix the result, then run sfy report done.\n\n", t.ID, t.Round, e.cfg.ReviewRounds)
	fmt.Fprintf(&b, "Command: %s\n", command)
	if r.TimedOut {
		fmt.Fprintf(&b, "The check did not finish within %s, so sfy stopped it.\n", e.cfg.CheckTimeout)
	} else {
		fmt.Fprintf(&b, "Error: %v\n", r.Err)
	}
	if r.Cut > 0 {
		fmt.Fprintf(&b, "sfy cut the first %d bytes of the output.\n", r.Cut)
	}
	fmt.Fprintf(&b, "\n```\n%s\n```\n", strings.TrimRight(r.Output, "\n"))
	return b.String()
}
