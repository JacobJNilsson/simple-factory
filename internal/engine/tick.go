package engine

import (
	"errors"
	"fmt"
	"time"

	"github.com/JacobJNilsson/factory/internal/task"
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
func (e *Engine) startWorker(t task.Task) error {
	dir := t.Dir
	if dir == "" {
		var err error
		if dir, err = e.w.Create(t); err != nil {
			return e.startFailed(t, fmt.Errorf("task %s: create workspace: %w", t.ID, err))
		}
	}
	t.State, t.Dir = task.Working, dir
	if err := e.save(t); err != nil {
		return errors.Join(err, e.w.Remove(t))
	}
	name := "worker-" + t.ID
	err := e.l.Start(SessionSpec{
		Name: name, Kind: "worker", Task: t.ID, Dir: dir,
		Model: e.cfg.Models.Worker, Prompt: "Do this task.\n\n" + t.Markdown(),
	})
	if err != nil {
		rmErr := e.w.Remove(t)
		t.State = task.Queued
		if rmErr == nil {
			t.Dir = ""
		}
		return e.startFailed(t, errors.Join(fmt.Errorf("task %s: start worker: %w", t.ID, err), rmErr, e.save(t)))
	}
	delete(e.startFails, t.ID)
	e.agents[name] = &agentState{}
	return nil
}

// startFailed counts a failed start. After maxStartFailures in a row the task moves to escalated and main hears of it.
func (e *Engine) startFailed(t task.Task, err error) error {
	e.startFails[t.ID]++
	if e.startFails[t.ID] < maxStartFailures {
		return err
	}
	delete(e.startFails, t.ID)
	why := fmt.Sprintf("Its worker failed to start %d times. Last error: %v", maxStartFailures, err)
	return errors.Join(err, e.escalateTask(t, why))
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
		if name == "main" {
			if alive, err := e.l.Alive(name); err == nil && !alive {
				continue
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
		s := e.agent(name)
		last := s.lastNote
		if last.IsZero() {
			last = unread[0].Created
		}
		if e.c.Now().Sub(last) >= wait && (s.waiting || !s.hookSince) {
			errs = append(errs, e.typeNote(name))
		}
	}
	return errors.Join(errs...)
}

// retryCleanup repeats the cleanup of done and cancelled tasks that still have a workspace.
func (e *Engine) retryCleanup() error {
	tasks, err := e.st.Tasks()
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tasks {
		if (t.State == task.Done || t.State == task.Cancelled) && t.Dir != "" {
			errs = append(errs, e.cleanup(t))
		}
	}
	return errors.Join(errs...)
}

// checkWorkers tells main once when a worker session is gone, and again only after the worker runs again.
// Factory does not restart a worker that died. The main agent decides what to do.
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
			errs = append(errs, e.message("main", "factory", fmt.Sprintf("The worker of task %s is gone. Its session no longer runs.", t.ID)))
		}
	}
	return errors.Join(errs...)
}
