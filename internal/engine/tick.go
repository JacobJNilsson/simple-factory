package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

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
	started := map[string]bool{}
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
		started[t.ID] = true
		running++
	}
	errs = append(errs, e.noteSlotWaits(tasks, started, running >= e.cfg.MaxWorkers))
	return errors.Join(errs...)
}

// noteSlotWaits tells the lead once when a ready queued task has waited queue_wait_note for a slot while an active task waits.
// full reports whether every slot is taken. A task that starts, or a free slot, ends the wait, so a later wait gets its own note.
func (e *Engine) noteSlotWaits(tasks []task.Task, started map[string]bool, full bool) error {
	ready := map[string]bool{}
	if full {
		for _, t := range tasks {
			if t.State == task.Queued && len(t.WaitingOn(tasks)) == 0 && !started[t.ID] {
				ready[t.ID] = true
			}
		}
	}
	now := e.c.Now()
	for id := range ready {
		if _, ok := e.slotWait[id]; !ok {
			e.slotWait[id] = now
		}
	}
	for id := range e.slotWait {
		if !ready[id] {
			delete(e.slotWait, id)
			delete(e.slotNoted, id)
		}
	}
	if e.cfg.QueueWaitNote == 0 {
		return nil
	}
	var errs []error
	for _, t := range tasks {
		since, ok := e.slotWait[t.ID]
		if !ok || e.slotNoted[t.ID] || now.Sub(since) < e.cfg.QueueWaitNote {
			continue
		}
		holders := e.waitingTasks(tasks)
		if len(holders) == 0 {
			continue
		}
		if err := e.message("lead", "sfy", slotWaitNote(t.ID, now.Sub(since), holders, now)); err != nil {
			errs = append(errs, err)
			continue
		}
		e.slotNoted[t.ID] = true
	}
	return errors.Join(errs...)
}

// slotHolder is an active task that holds a slot while it waits.
type slotHolder struct {
	id string
	// pushing is true for a task that waits for its worker to push, and false for a worker that reported blocked.
	pushing bool
	since   time.Time
}

// waitingTasks returns the active tasks that hold a slot while they wait for a push or for an answer to a blocked worker.
// A push wait counts from the task's last save, which askPush makes. An edit of the task by the lead starts the count again.
func (e *Engine) waitingTasks(tasks []task.Task) []slotHolder {
	var out []slotHolder
	for _, t := range tasks {
		if !t.State.Active() {
			continue
		}
		if t.State == task.Review && t.Pushing {
			out = append(out, slotHolder{id: t.ID, pushing: true, since: t.Updated})
			continue
		}
		if s := e.sess.agents[e.workerOf(t)]; s != nil && !s.blockedAt.IsZero() {
			out = append(out, slotHolder{id: t.ID, since: s.blockedAt})
		}
	}
	return out
}

// slotWaitNote builds the note that tells the lead which waiting tasks keep a queued task from a slot.
func slotWaitNote(queued string, waited time.Duration, holders []slotHolder, now time.Time) string {
	parts := []string{fmt.Sprintf("Task %s has waited %s for a worker slot.", queued, minutes(waited))}
	for _, h := range holders {
		if h.pushing {
			parts = append(parts, fmt.Sprintf("Task %s holds a slot and has waited %s for its worker to push.", h.id, minutes(now.Sub(h.since))))
		} else {
			parts = append(parts, fmt.Sprintf("Task %s holds a slot and has waited %s since its worker reported blocked.", h.id, minutes(now.Sub(h.since))))
		}
	}
	parts = append(parts, "Answer, escalate or cancel one of these tasks to free a slot.")
	return strings.Join(parts, " ")
}

// minutes writes a duration in whole minutes, rounded down.
func minutes(d time.Duration) string {
	switch n := int(d / time.Minute); n {
	case 0:
		return "less than a minute"
	case 1:
		return "1 minute"
	default:
		return fmt.Sprintf("%d minutes", n)
	}
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
// It also ends the sessions of an approved task until they all ended once, in case the stop after the pass failed.
func (e *Engine) retryCleanup() error {
	tasks, err := e.st.Tasks()
	if err != nil {
		return err
	}
	var errs []error
	retried, approved := map[string]bool{}, map[string]bool{}
	for _, t := range tasks {
		switch {
		case t.State == task.Approved:
			approved[t.ID] = true
			if !e.sess.ended[t.ID] {
				retried[t.ID] = true
				errs = append(errs, e.retryOne(t, e.endApproved))
			}
		case (t.State == task.Done || t.State == task.Cancelled) && t.Dir != "":
			retried[t.ID] = true
			errs = append(errs, e.retryOne(t, e.cleanup))
		}
	}
	for id := range e.cleanupRetries {
		if !retried[id] {
			delete(e.cleanupRetries, id)
		}
	}
	for id := range e.sess.ended {
		if !approved[id] {
			delete(e.sess.ended, id)
		}
	}
	return errors.Join(errs...)
}

// cleanupRetry is what the tick knows of the failed cleanups of one task.
type cleanupRetry struct {
	// updated is the Updated stamp of the task at the last failure.
	updated time.Time
	// fails counts the failed cleanups in a row.
	fails int
	// skip is the number of ticks left to skip before the next try.
	skip int
	// told is true after sfy told the lead that the cleanup of this task fails.
	told bool
}

// cleanupBackoff is how many ticks a cleanup retry skips after its first failure.
const cleanupBackoff = 10

// cleanupBackoffMax is the longest wait between cleanup retries, about 5 minutes at the 2-second tick.
const cleanupBackoffMax = 150

// cleanupTellAfter is how many cleanup retries in a row fail before sfy tells the lead.
const cleanupTellAfter = 3

// cleanupWaitAfter returns the number of ticks to skip after the given number of failures in a row.
func cleanupWaitAfter(fails int) int {
	wait := cleanupBackoff
	for i := 1; i < fails && wait < cleanupBackoffMax; i++ {
		wait *= 2
	}
	return min(wait, cleanupBackoffMax)
}

// retryOne runs the cleanup step run for one task. After a failure it skips a growing number of ticks, and a change to the task resets the wait.
func (e *Engine) retryOne(t task.Task, run func(task.Task) error) error {
	r := e.cleanupRetries[t.ID]
	if r != nil && !r.updated.Equal(t.Updated) {
		r.fails, r.skip = 0, 0
	}
	if r != nil && r.skip > 0 {
		r.skip--
		return nil
	}
	err := run(t)
	if err == nil {
		delete(e.cleanupRetries, t.ID)
		return nil
	}
	if r == nil {
		r = &cleanupRetry{}
		e.cleanupRetries[t.ID] = r
	}
	r.updated = t.Updated
	r.fails++
	r.skip = cleanupWaitAfter(r.fails)
	if r.fails >= cleanupTellAfter && !r.told {
		merr := e.message("lead", "sfy", fmt.Sprintf("sfy cannot clean up task %s: %v", t.ID, err))
		r.told = merr == nil
		err = errors.Join(err, merr)
	}
	return err
}
