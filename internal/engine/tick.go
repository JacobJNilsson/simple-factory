package engine

import (
	"errors"
	"fmt"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// Tick does the periodic work. A step that fails does not stop the later steps, and Tick returns every error.
func (e *Engine) Tick() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.down.Load() {
		return nil
	}
	var errs []error
	for _, step := range []func() error{e.pickUpDrops, e.startQueued, e.resendNotes, e.retryCleanup, e.checkWorkers} {
		errs = append(errs, step())
	}
	return errors.Join(errs...)
}

// startQueued starts workers for the oldest queued tasks while a slot is free.
func (e *Engine) startQueued() error {
	tasks, err := e.st.Tasks()
	if err != nil {
		return err
	}
	running := 0
	for _, t := range tasks {
		if t.State.Active() {
			running++
		}
	}
	errs := []error{e.noteCancelledDeps(tasks)}
	for _, t := range tasks {
		if running >= e.cfg.MaxWorkers {
			break
		}
		if t.State != task.Queued || len(t.WaitingOn(tasks)) > 0 {
			continue
		}
		if err := e.startWorker(t); err != nil {
			errs = append(errs, err)
			continue
		}
		running++
	}
	return errors.Join(errs...)
}

// noteCancelledDeps tells the lead once for each queued task that waits for a cancelled task.
// The waiting task stays queued, and the lead edits or cancels it.
func (e *Engine) noteCancelledDeps(tasks []task.Task) error {
	state := map[string]task.State{}
	for _, t := range tasks {
		state[t.ID] = t.State
	}
	current := map[string]bool{}
	var errs []error
	for _, t := range tasks {
		if t.State != task.Queued {
			continue
		}
		for _, dep := range t.After {
			if state[dep] != task.Cancelled {
				continue
			}
			key := t.ID + ">" + dep
			current[key] = true
			if e.depNoted[key] {
				continue
			}
			note := fmt.Sprintf("Task %s waits for %s, which is cancelled. Edit or cancel task %s.", t.ID, dep, t.ID)
			if err := e.message("lead", "sfy", note); err != nil {
				errs = append(errs, err)
				continue
			}
			e.depNoted[key] = true
		}
	}
	for key := range e.depNoted {
		if !current[key] {
			delete(e.depNoted, key)
		}
	}
	return errors.Join(errs...)
}

// maxStartFailures is how many starts in a row may fail before the task moves to escalated.
const maxStartFailures = 3

// startWorker saves the task as working before it starts the session, so a crash leaves a record of the workspace.
// A failure puts the task back in queued. A workspace that Remove could not delete stays in Dir, and the next try reuses it.
// A reopened task has earlier work in its workspace, so a failed start keeps the workspace.
// The worker is the first agent of the task's start list, which Runnable and the List check make a claude agent that ends at done.
func (e *Engine) startWorker(t task.Task) error {
	first, err := e.proc.First(e.sess.startList(t))
	if err != nil {
		return e.startFailed(t, fmt.Errorf("task %s: %w", t.ID, err))
	}
	prompt, pending := workerPrompt(t), t.Pending
	if a := e.proc.Agents[first.Agent]; a.Message != "" {
		prompt += "\n" + a.Message + "\n"
	}
	p, err := e.w.Prepare(t)
	t.Branch, t.Lease = p.Branch, p.Lease
	if err != nil {
		return e.startFailed(t, fmt.Errorf("task %s: %w", t.ID, err))
	}
	dir := p.Dir
	t.StartWork(dir)
	t.MoveTo(first)
	if err := e.save(t); err != nil {
		return errors.Join(err, e.discard(t))
	}
	if err := e.sess.start(t, first.Agent, prompt); err != nil {
		rmErr := e.discard(t)
		t.RequeueAfterFailedStart(pending, rmErr == nil && !t.Reopened)
		return e.startFailed(t, errors.Join(fmt.Errorf("task %s: start worker: %w", t.ID, err), rmErr, e.save(t)))
	}
	delete(e.startFails, t.ID)
	delete(e.checking, t.ID)
	return nil
}

// discard removes the workspace of a task whose start failed, unless it holds the earlier work of a reopened task.
func (e *Engine) discard(t task.Task) error {
	if t.Reopened {
		return nil
	}
	return e.w.Remove(t)
}

// workerPrompt returns the first message of a worker: the task file, then the feedback that no worker has read.
// A reopened task also gets a note about its earlier work.
func workerPrompt(t task.Task) string {
	prompt := "Do this task.\n\n" + t.Markdown()
	if t.Reopened {
		prompt = "Do this task again. It was finished, then feedback came in.\n\n" + t.Markdown()
	}
	for _, f := range t.Pending {
		prompt += "\nFeedback:\n\n" + f + "\n"
	}
	if t.Repo != "" {
		prompt += "\n" + holdPush + "\n"
	}
	if t.HoldsPush() {
		prompt += "\n" + operatorHold + "\n"
	}
	if t.Reopened {
		prompt += "\nThis workspace and its branch hold the earlier work. The branch may already be pushed or integrated into the default branch. Address the feedback on top of that work.\n"
	}
	return prompt
}

// startFailed counts a failed start. After maxStartFailures in a row the task moves to escalated and the lead hears of it.
func (e *Engine) startFailed(t task.Task, err error) error {
	e.startFails[t.ID]++
	var pre *PreflightError
	// A failed preflight check stays failed until the operator fixes the machine, so retries only lose time.
	if !errors.As(err, &pre) && e.startFails[t.ID] < maxStartFailures {
		return err
	}
	delete(e.startFails, t.ID)
	why := fmt.Sprintf("Its worker failed to start %d times. Last error: %v", maxStartFailures, err)
	if pre != nil {
		why = fmt.Sprintf("Its worker cannot start. Fix this, then run sfy task resume %s: %v", t.ID, pre.Msg)
	}
	return errors.Join(err, e.escalateTask(t, why, true))
}

// resendNotes gives the late notes to the lead, the workers of active tasks and the reviewers of tasks in review.
func (e *Engine) resendNotes() error {
	tasks, err := e.st.Tasks()
	if err != nil {
		return err
	}
	names := []string{"lead"}
	for _, t := range tasks {
		if t.State.Active() {
			names = append(names, e.workerOf(t))
		}
		if t.State == task.Review {
			names = append(names, e.reviewerOf(t))
		}
	}
	return e.notes.Tick(names)
}

// retryCleanup repeats the cleanup of done and cancelled tasks that still have a workspace.
// It also ends the sessions of an approved task again, in case the stop after the pass failed.
func (e *Engine) retryCleanup() error {
	tasks, err := e.st.Tasks()
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tasks {
		if t.State == task.Approved {
			errs = append(errs, e.endSessions(t))
		}
		if (t.State == task.Done || t.State == task.Cancelled) && t.Dir != "" {
			errs = append(errs, e.retryOne(t))
		}
	}
	return errors.Join(errs...)
}

// cleanupBackoff is how many retries a cleanup skips after a liveness check that errored.
const cleanupBackoff = 10

// retryOne runs the cleanup of one task. A check that cannot tell whether a session runs makes the next retries wait.
func (e *Engine) retryOne(t task.Task) error {
	if e.cleanupWait[t.ID] > 0 {
		e.cleanupWait[t.ID]--
		return nil
	}
	err := e.cleanup(t)
	if errors.Is(err, errAliveUnknown) {
		e.cleanupWait[t.ID] = cleanupBackoff
	}
	return err
}
