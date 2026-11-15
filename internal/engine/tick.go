package engine

import (
	"errors"
	"fmt"
	"time"

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
	var errs []error
	for _, t := range tasks {
		if running >= e.cfg.MaxWorkers {
			break
		}
		if t.State != task.Queued {
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

// maxStartFailures is how many starts in a row may fail before the task moves to escalated.
const maxStartFailures = 3

// startWorker saves the task as working before it starts the session, so a crash leaves a record of the workspace.
// A failure puts the task back in queued. A workspace that Remove could not delete stays in Dir, and the next try reuses it.
// A reopened task has earlier work in its workspace, so a failed start keeps the workspace.
func (e *Engine) startWorker(t task.Task) error {
	prompt, pending := workerPrompt(t), t.Pending
	dir := t.Dir
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
		Model: e.cfg.Models.Worker, Prompt: prompt, Protected: t.Protected, HasRepo: t.Repo != "",
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
	if t.Reopened {
		prompt += "\nThis workspace and its branch hold the earlier work. The branch may already have an open pull request, which may be merged or closed by now. Address the feedback on top of that work.\n"
	}
	return prompt
}

// startFailed counts a failed start. After maxStartFailures in a row the task moves to escalated and main hears of it.
func (e *Engine) startFailed(t task.Task, err error) error {
	e.startFails[t.ID]++
	if e.startFails[t.ID] < maxStartFailures {
		return err
	}
	delete(e.startFails, t.ID)
	why := fmt.Sprintf("Its worker failed to start %d times. Last error: %v", maxStartFailures, err)
	return errors.Join(err, e.escalateTask(t, why, true))
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
			alive, err := e.l.Alive(name)
			down = err == nil && !alive
			if err != nil && !s.aliveFailLogged {
				e.logf("alive check of %s failed: %v", name, err)
				s.aliveFailLogged = true
			} else if err == nil {
				s.aliveFailLogged = false
			}
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
			errs = append(errs, e.typeNote(name, "resend"))
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
			errs = append(errs, e.cleanup(t))
		}
	}
	return errors.Join(errs...)
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
		alive, err := e.l.Alive(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s := e.agent(name)
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
