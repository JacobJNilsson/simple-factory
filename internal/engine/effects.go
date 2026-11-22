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
	tasks, err := e.shownTasks()
	if err != nil {
		return err
	}
	return e.st.WriteStatus(e.renderStatus(tasks))
}

// storeMessage stores a message without a note. Its id sorts in send order and never repeats, because the counter breaks ties within one clock tick.
func (e *Engine) storeMessage(to, from, body string) error {
	e.counter++
	now := e.c.Now()
	return e.st.Send(store.Message{
		ID: fmt.Sprintf("%019d-%06d", now.UnixNano(), e.counter), From: from, To: to, Body: body, Created: now,
	})
}

// message stores a message and tells the receiver.
func (e *Engine) message(to, from, body string) error {
	if err := e.storeMessage(to, from, body); err != nil {
		return err
	}
	return e.notes.Message(to, "new message")
}

// noReportNote is the reminder for a turn that ended without a report.
const noReportNote = "Your turn ended without a report. If you need an answer, run sfy report blocked <question>."

// endSessions stops the worker and every reviewer of a task, and forgets their state.
func (e *Engine) endSessions(t task.Task) error {
	if err := e.sess.end(workerOf(t)); err != nil {
		return err
	}
	delete(e.checking, t.ID)
	for r := 1; r <= t.Round; r++ {
		if err := e.sess.end(reviewerOfRound(t.ID, r)); err != nil {
			return err
		}
	}
	return nil
}

// cleanup ends the sessions of a task that ran, then removes its workspace unless the workspace holds work, and clears Dir.
// Tick retries it for done and cancelled tasks that still have a Dir.
func (e *Engine) cleanup(t task.Task) error {
	if t.Dir == "" {
		return nil
	}
	if err := e.endSessions(t); err != nil {
		return err
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
	// The save comes first, so a failed save retries the whole cleanup and the lead hears of a kept workspace once.
	if err := e.save(t); err != nil {
		return err
	}
	if holds {
		return e.message("lead", "sfy", fmt.Sprintf("Task %s is %s, but its workspace %s still holds work, so sfy kept it.", t.ID, t.State, dir))
	}
	return nil
}
