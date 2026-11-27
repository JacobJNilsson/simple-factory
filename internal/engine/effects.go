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

// endSessions stops every task session and every round session of a task, and forgets their state.
// The shell agent of the task stops after the first task session, so a check result cannot outlive the worker.
func (e *Engine) endSessions(t task.Task) error {
	for i, name := range e.sess.allSessions(t) {
		if err := e.sess.end(name); err != nil {
			return err
		}
		if i == 0 {
			delete(e.checking, t.ID)
		}
	}
	return nil
}

// endApproved ends the sessions of an approved task and records that they ended, so the tick stops them no more.
func (e *Engine) endApproved(t task.Task) error {
	if err := e.endSessions(t); err != nil {
		return err
	}
	e.sess.ended[t.ID] = true
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
