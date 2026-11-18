package engine

import (
	"errors"
	"fmt"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// active reports whether a task holds a worker slot.
func active(t task.Task) bool {
	return t.State == task.Working || t.State == task.Review || t.State == task.Feedback
}

// Tick does the periodic work. A step that fails does not stop the later steps, and Tick returns every error.
func (e *Engine) Tick() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.down.Load() {
		return nil
	}
	var errs []error
	for _, step := range []func() error{e.startQueued, e.resendNotes, e.retryCleanup, e.checkWorkers} {
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
		if active(t) {
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

// noteCancelledDeps tells main once for each queued task that waits for a cancelled task.
// The waiting task stays queued, and main edits or cancels it.
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
			if err := e.message("main", "sfy", note); err != nil {
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
func (e *Engine) startWorker(t task.Task) error {
	prompt, pending := workerPrompt(t), t.Pending
	dir := t.Dir
	if err := e.chooseBranch(&t); err != nil {
		return e.startFailed(t, fmt.Errorf("task %s: choose branch: %w", t.ID, err))
	}
	if dir == "" {
		create := e.w.Create
		if t.Reopened {
			create = e.w.Restore
		}
		var err error
		if dir, err = create(t); err != nil {
			return e.startFailed(t, fmt.Errorf("task %s: create workspace: %w", t.ID, err))
		}
	}
	t.State, t.Dir, t.Pending = task.Working, dir, nil
	if err := e.save(t); err != nil {
		return errors.Join(err, e.discard(t))
	}
	name := "worker-" + t.ID
	err := e.l.Start(SessionSpec{
		Name: name, Kind: "worker", Task: t.ID, Dir: dir,
		Model: t.WorkerModel(e.cfg.Models.Worker), Prompt: prompt, Protected: t.Protected, HasRepo: t.Repo != "",
		Branch: t.Branch, Lease: t.Lease, NoPush: t.HoldsPush(),
	})
	if err != nil {
		rmErr := e.discard(t)
		t.State, t.Pending = task.Queued, pending
		if rmErr == nil && !t.Reopened {
			t.Dir = ""
		}
		return e.startFailed(t, errors.Join(fmt.Errorf("task %s: start worker: %w", t.ID, err), rmErr, e.save(t)))
	}
	delete(e.startFails, t.ID)
	e.agents[name] = &agentState{}
	return nil
}

// branchOf returns the task's branch. A task with a repo that started before tasks carried a branch keeps its old name.
// It returns "" for a task with no repo and for a task that has not started and has no branch yet.
func (e *Engine) branchOf(t task.Task) string {
	if t.Branch == "" && t.Repo != "" && (t.Dir != "" || t.Reopened) {
		return e.w.LegacyBranch(t.ID)
	}
	return t.Branch
}

// chooseBranch fixes the branch of a task with a repo, once.
func (e *Engine) chooseBranch(t *task.Task) error {
	if t.Repo == "" {
		return nil
	}
	if t.Existing != "" {
		t.Branch = t.Existing
		if t.Lease != "" {
			return nil
		}
		// The checkout records the remote sha, which later pushes must still find on the remote.
		sha, err := e.w.RemoteBranch(*t)
		t.Lease = sha
		return err
	}
	if t.Branch = e.branchOf(*t); t.Branch != "" {
		return nil
	}
	branch, err := e.w.NewBranch(*t)
	t.Branch = branch
	return err
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

// startFailed counts a failed start. After maxStartFailures in a row the task moves to escalated and main hears of it.
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

// hookGrace returns how long the hook has to take queued messages before sfy types the note.
// A config without the setting, as tests build it, gets the default.
func (e *Engine) hookGrace() time.Duration {
	if e.cfg.NoteFallbackAfter > 0 {
		return e.cfg.NoteFallbackAfter
	}
	return config.DefaultNoteFallbackAfter
}

// typeAfterHook types the new-message note for an agent whose hook left the queued messages unread.
func (e *Engine) typeAfterHook(name string, s *agentState) error {
	s.hookedAt, s.lastNote = time.Time{}, e.c.Now()
	e.logf("note typed to %s: the hook left its messages unread", name)
	return e.ignoreGone(name, e.l.Send(name, newMessageNote))
}

// resendNotes types the note again for an agent whose messages stay unread.
// It does so only when the agent waits, or when no hook event came since the last note, which means the note was lost.
func (e *Engine) resendNotes() error {
	tasks, err := e.st.Tasks()
	if err != nil {
		return err
	}
	names := []string{"main"}
	for _, t := range tasks {
		if active(t) {
			names = append(names, "worker-"+t.ID)
		}
		if t.State == task.Review {
			names = append(names, reviewerOf(t))
		}
	}
	wait := time.Duration(e.cfg.ResendMinutes) * time.Minute
	var errs []error
	for _, name := range names {
		s := e.agent(name)
		down := false
		if name == "main" {
			alive, known := e.probe(name)
			down = known && !alive
			if !down {
				s.skipLogged = false
			}
		}
		unread, err := e.st.Unread(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(unread) == 0 {
			if !s.hookedAt.IsZero() {
				e.logf("messages taken by the hook of %s", name)
				// The hook woke the agent, so a turn runs now.
				s.hookedAt, s.waiting, s.hookSince = time.Time{}, false, false
			}
			continue
		}
		if !down && !s.hookedAt.IsZero() {
			// A busy session reads its notes between tool calls or at the turn end, however long the turn runs.
			if s.waiting && e.c.Now().Sub(s.hookedAt) >= e.hookGrace() {
				errs = append(errs, e.typeAfterHook(name, s))
			}
			continue
		}
		if down {
			if !s.skipLogged {
				e.logf("note skipped for %s: its session is not alive", name)
				s.skipLogged = true
			}
			continue
		}
		last := s.lastNote
		if last.IsZero() {
			last = unread[0].Created
		}
		if e.c.Now().Sub(last) >= wait && (s.waiting || !s.hookSince) {
			errs = append(errs, e.noteMessages(name, "resend"))
		}
	}
	return errors.Join(errs...)
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

// aliveErrTicks is how many ticks in a row an alive check may fail before sfy tells main.
const aliveErrTicks = 3

// probe runs the alive check of a session and counts its failures. It logs the first failure of a run.
// known is false when the check failed, so the caller cannot tell whether the session runs.
func (e *Engine) probe(name string) (alive, known bool) {
	s := e.agent(name)
	alive, err := e.l.Alive(name)
	if err == nil {
		s.aliveErrs, s.aliveErr, s.aliveTold = 0, nil, false
		return alive, true
	}
	s.aliveErrs++
	s.aliveErr = err
	if s.aliveErrs == 1 {
		e.logf("alive check of %s failed: %v", name, err)
	}
	return false, false
}

// checkWorkers tells main once when a worker session is gone, and again only after the worker runs again.
// sfy does not restart a worker that died. The main agent decides what to do.
func (e *Engine) checkWorkers() error {
	tasks, err := e.st.Tasks()
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tasks {
		if !active(t) {
			continue
		}
		name := "worker-" + t.ID
		alive, known := e.probe(name)
		s := e.agent(name)
		if !known {
			if s.aliveErrs >= aliveErrTicks && !s.aliveTold {
				s.aliveTold = true
				errs = append(errs, e.message("main", "sfy", fmt.Sprintf("sfy cannot tell whether %s runs: %v", name, s.aliveErr)))
			}
			continue
		}
		switch {
		case alive:
			s.gone = false
		case !s.gone:
			s.gone = true
			errs = append(errs, e.message("main", "sfy", fmt.Sprintf("The worker of task %s is gone. Its session no longer runs.", t.ID)))
		}
	}
	return errors.Join(errs...)
}
