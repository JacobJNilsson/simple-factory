package engine

import (
	"fmt"

	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// save stamps and writes a task, then rewrites status.md.
func (e *Engine) save(t task.Task) error {
	t.Updated = e.c.Now()
	if err := e.st.Save(t); err != nil {
		return err
	}
	return e.refreshStatus()
}

func (e *Engine) refreshStatus() error {
	tasks, err := e.st.Tasks()
	if err != nil {
		return err
	}
	return e.st.WriteStatus(store.RenderStatus(tasks))
}

// storeMessage stores a message without a note. Its id sorts in send order and never repeats, because the counter breaks ties within one clock tick.
func (e *Engine) storeMessage(to, from, body string) error {
	e.counter++
	now := e.c.Now()
	return e.st.Send(store.Message{
		ID: fmt.Sprintf("%019d-%06d", now.UnixNano(), e.counter), From: from, To: to, Body: body, Created: now,
	})
}

// message stores a message and types the note when the receiver waits.
func (e *Engine) message(to, from, body string) error {
	if err := e.storeMessage(to, from, body); err != nil {
		return err
	}
	if e.agent(to).waiting {
		return e.typeNote(to)
	}
	return nil
}

const (
	newMessageNote = "New message. Run: sfy inbox"
	noReportNote   = "Your turn ended without a report. If you need an answer, run sfy report blocked <question>."
)

// typeNote types the new-message note into a pane. The agent is busy until its next turn ends.
func (e *Engine) typeNote(name string) error {
	s := e.agent(name)
	s.waiting, s.hookSince, s.lastNote = false, false, e.c.Now()
	return e.l.Send(name, newMessageNote)
}

// ignoreGone drops a note error from a session that no longer runs, because the note cannot arrive.
func (e *Engine) ignoreGone(name string, err error) error {
	if err == nil {
		return nil
	}
	if alive, aerr := e.l.Alive(name); aerr == nil && !alive {
		return nil
	}
	return err
}

// stop ends a session. A failure counts only while the session still runs, because a session may already be gone.
func (e *Engine) stop(name string) error {
	err := e.l.Stop(name)
	if err == nil {
		return nil
	}
	if alive, aerr := e.l.Alive(name); aerr == nil && !alive {
		return nil
	}
	return err
}

// cleanup ends the sessions of a task that ran, then removes its workspace unless the workspace holds work, and clears Dir.
// Tick retries it for done and cancelled tasks that still have a Dir.
func (e *Engine) cleanup(t task.Task) error {
	if t.Dir == "" {
		return nil
	}
	if err := e.stop("worker-" + t.ID); err != nil {
		return err
	}
	delete(e.agents, "worker-"+t.ID)
	for r := 1; r <= t.Round; r++ {
		name := fmt.Sprintf("review-%s-%d", t.ID, r)
		if err := e.stop(name); err != nil {
			return err
		}
		delete(e.agents, name)
	}
	holds, err := e.w.HoldsWork(t)
	if err != nil {
		return err
	}
	if !holds {
		if err := e.w.Remove(t); err != nil {
			return err
		}
	}
	dir := t.Dir
	t.Dir = ""
	// The save comes first, so a failed save retries the whole cleanup and main hears of a kept workspace once.
	if err := e.save(t); err != nil {
		return err
	}
	if holds {
		return e.message("main", "sfy", fmt.Sprintf("Task %s is %s, but its workspace %s still holds work, so sfy kept it.", t.ID, t.State, dir))
	}
	return nil
}
