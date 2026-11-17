package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

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
		return e.noteMessages(to, "new message")
	}
	return nil
}

const (
	newMessageNote = "New message. Run: sfy inbox"
	stuckNote      = "You have undelivered notes. Run: sfy inbox"
	noReportNote   = "Your turn ended without a report. If you need an answer, run sfy report blocked <question>."
)

// noteMessages tells an agent about its unread messages. The agent is busy until its next turn ends.
// The provider takes the sender and text of each message. The message stays unread until the session takes the text,
// so a message that never arrives still shows in sfy inbox. When the provider cannot deliver, sfy types the new-message note.
// A hook may not wake an idle session. So a provider delivery only queues the text, and a later tick types the note when the text stays unread.
func (e *Engine) noteMessages(name, why string) error {
	if !e.reachable(name) {
		return nil
	}
	s := e.agent(name)
	s.waiting, s.hookSince, s.lastNote, s.hookedAt = false, false, e.c.Now(), time.Time{}
	unread, err := e.st.Unread(name)
	if err == nil {
		s.notedID = newestID(unread)
	}
	if err == nil && len(unread) > 0 && e.deliverMessages(name, unread) {
		s.hookedAt = e.c.Now()
		e.logf("messages queued in the note file of %s: %s", name, why)
		return nil
	}
	e.logf("note typed to %s: %s", name, why)
	return e.l.Send(name, newMessageNote)
}

// deliverMessages hands each message to the provider and reports whether all of them went through.
func (e *Engine) deliverMessages(name string, msgs []store.Message) bool {
	for _, m := range msgs {
		delivered, err := e.deliver(name, strings.TrimRight(formatMessages([]store.Message{m}), "\n"), m.ID)
		if err != nil {
			e.logf("delivery to %s failed, so sfy types the note: %v", name, err)
		}
		if !delivered {
			return false
		}
	}
	return true
}

// note tells an agent a line of text. The provider delivers it, and sfy types it when the provider cannot.
// A session that cannot take delivered text also hears about the text that waits for it.
func (e *Engine) note(name, text string) error {
	if !e.reachable(name) {
		return nil
	}
	delivered, err := e.deliver(name, text, "")
	if err != nil {
		e.logf("delivery to %s failed, so sfy types the note: %v", name, err)
	}
	if delivered {
		return nil
	}
	if err := e.l.Send(name, text); err != nil {
		return err
	}
	if e.l.Stuck(name) {
		return e.l.Send(name, stuckNote)
	}
	return nil
}

// deliver asks the provider to deliver text. A registered main agent has no sfy session folder, so it gets no delivery.
func (e *Engine) deliver(name, text, messageID string) (bool, error) {
	if name == "main" && e.registeredMain() {
		return false, nil
	}
	return e.l.Deliver(name, text, messageID)
}

// formatMessages renders messages with their senders, as sfy inbox prints them.
func formatMessages(msgs []store.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "From %s:\n%s\n\n", m.From, m.Body)
	}
	return b.String()
}

func messageIDs(msgs []store.Message) []string {
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	return ids
}

// newestID returns the highest message id. Ids sort in send order.
func newestID(msgs []store.Message) string {
	id := ""
	for _, m := range msgs {
		id = max(id, m.ID)
	}
	return id
}

// noteDue reports whether a message arrived after the last note. An agent that ignores a note must not get it at every turn end.
// The resend after resend_minutes covers the messages that stay unread.
func (e *Engine) noteDue(name string, unread []store.Message) bool {
	return newestID(unread) > e.agent(name).notedID
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
	alive, aerr := e.l.Alive(name)
	if aerr != nil {
		return errors.Join(err, fmt.Errorf("%w of %s: %w", errAliveUnknown, name, aerr))
	}
	if !alive {
		return nil
	}
	return err
}

// errAliveUnknown marks a failure that follows from an alive check that could not tell whether a session runs.
var errAliveUnknown = errors.New("cannot tell whether the session runs")

// reachable reports whether a note can arrive. A session that is gone gets no note, and its messages stay unread.
// The log names the skip once, until the session lives again.
func (e *Engine) reachable(name string) bool {
	s := e.agent(name)
	if alive, err := e.l.Alive(name); err != nil || alive {
		s.skipLogged = false
		return true
	}
	if !s.skipLogged {
		e.logf("note skipped for %s: its session is not alive", name)
		s.skipLogged = true
	}
	return false
}

// endSessions stops the worker and every reviewer of a task, and forgets their state.
func (e *Engine) endSessions(t task.Task) error {
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
	// The save comes first, so a failed save retries the whole cleanup and main hears of a kept workspace once.
	if err := e.save(t); err != nil {
		return err
	}
	if holds {
		return e.message("main", "sfy", fmt.Sprintf("Task %s is %s, but its workspace %s still holds work, so sfy kept it.", t.ID, t.State, dir))
	}
	return nil
}
