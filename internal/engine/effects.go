package engine

import (
	"fmt"

	"github.com/JacobJNilsson/factory/internal/store"
	"github.com/JacobJNilsson/factory/internal/task"
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

// message stores a message. Its id sorts in send order and never repeats, because the counter breaks ties within one clock tick.
func (e *Engine) message(to, from, body string) error {
	e.counter++
	now := e.c.Now()
	err := e.st.Send(store.Message{
		ID: fmt.Sprintf("%019d-%06d", now.UnixNano(), e.counter), From: from, To: to, Body: body, Created: now,
	})
	if err != nil {
		return err
	}
	if e.agent(to).waiting {
		return e.typeNote(to)
	}
	return nil
}

const (
	newMessageNote = "New message. Run: factory inbox"
	noReportNote   = "Your turn ended without a report. If you need an answer, run factory report blocked <question>."
)

// typeNote types the new-message note into a pane. The agent is busy until its next turn ends.
func (e *Engine) typeNote(name string) error {
	s := e.agent(name)
	s.waiting, s.hookSince, s.lastNote = false, false, e.c.Now()
	return e.l.Send(name, newMessageNote)
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

// cleanup ends the sessions of a task that ran and removes its workspace, then clears Dir.
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
	if err := e.w.Remove(t); err != nil {
		return err
	}
	t.Dir = ""
	return e.save(t)
}
