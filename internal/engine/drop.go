package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/drop"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// SetDrops makes the engine take the drops in the folder on each tick. Without it, the engine reads no drops.
func (e *Engine) SetDrops(f drop.Folder) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.drops = &f
}

// PickUpDrops takes the drops that wait in the drop folder. The sfy process runs it once at start, before the first tick.
func (e *Engine) PickUpDrops() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pickUpDrops()
}

// pickUpDrops takes each file in new/ in name order. A drop reaches the lead only, as a message or as a task in new.
func (e *Engine) pickUpDrops() error {
	if e.drops == nil {
		return nil
	}
	paths, err := e.drops.New()
	if err != nil {
		return err
	}
	var errs []error
	for _, path := range paths {
		errs = append(errs, e.takeDrop(path))
	}
	return errors.Join(errs...)
}

// takeDrop delivers one drop, or rejects it, or removes it as a duplicate. sfy takes the sender and the id from the headers, never from the file name.
func (e *Engine) takeDrop(path string) error {
	f := e.drops
	raw, err := f.Read(path)
	if err != nil {
		return err
	}
	d, err := drop.Parse(raw)
	if err != nil {
		return e.rejectDrop(path, d, err.Error())
	}
	if f.Seen(d.From, d.ID) {
		e.logf("drop %s skipped: %s already dropped the id %s", path, d.Sender(), d.ID)
		return f.Remove(path)
	}
	if d.Kind == drop.KindTask {
		return e.dropTask(path, d)
	}
	if err := e.storeMessage(protocol.Lead, d.Sender(), d.Body); err != nil {
		return err
	}
	// The record comes before the note, so a failed note never makes the next tick deliver the drop twice.
	if err := f.Done(path, d); err != nil {
		return err
	}
	e.reply(d.From, drop.Reply{Kind: drop.ReplyReceipt, Re: d.ID, Body: "The lead got the message."})
	return e.notes.Message(protocol.Lead, "new message")
}

// dropTask creates a task in new from a drop. A body that is no valid task file rejects the drop.
func (e *Engine) dropTask(path string, d drop.Drop) error {
	t, err := task.Parse(d.Body)
	if err != nil {
		return e.rejectDrop(path, d, "the task file is not valid: "+err.Error())
	}
	t.SourceID = d.ID
	t, _, err = e.createTask(t, d.Sender())
	created := err == nil
	if err != nil && t.ID != "" {
		// A task that exists and only lacks its note counts as taken.
		_, lookErr := e.st.Task(t.ID)
		created = lookErr == nil
	}
	if !created {
		return e.rejectDrop(path, d, "sfy could not create the task: "+err.Error())
	}
	receipt := drop.Reply{Kind: drop.ReplyReceipt, Re: d.ID, Task: t.ID, Body: fmt.Sprintf("sfy created task %s, and it waits for the lead.", t.ID)}
	if doneErr := e.drops.Done(path, d); doneErr != nil {
		return errors.Join(err, doneErr)
	}
	e.reply(d.From, receipt)
	return err
}

// rejectDrop moves a drop to rejected/ with the reason, logs it, and tells a sender whose name passed its check.
func (e *Engine) rejectDrop(path string, d drop.Drop, why string) error {
	e.logf("drop %s rejected: %s", path, why)
	if err := e.drops.Reject(path, why); err != nil {
		return err
	}
	if d.From != "" {
		e.reply(d.From, drop.Reply{Kind: drop.ReplyRejected, Re: d.ID, Body: why})
	}
	return nil
}

// reply writes a reply for the sender of a drop. A failed reply goes to the log only, because it must not undo what the drop did.
func (e *Engine) reply(to string, r drop.Reply) {
	if err := e.writeReply(to, r); err != nil {
		e.logf("reply to %s%s failed: %v", drop.SenderPrefix, to, err)
	}
}

// writeReply gives a reply an id that sorts in write order, and writes it. A reply without a sender comes from sfy.
func (e *Engine) writeReply(to string, r drop.Reply) error {
	if e.drops == nil {
		return errors.New("this sfy process has no drop folder")
	}
	e.counter++
	r.ID = fmt.Sprintf("%019d-%06d", e.c.Now().UnixNano(), e.counter)
	if r.From == "" {
		r.From = "sfy"
	}
	return e.drops.Reply(to, r)
}

// replyEnd tells the sender of a drop that its task reached done, cancelled or escalated.
func (e *Engine) replyEnd(t task.Task) {
	source := t.StatedSource()
	if source == "" {
		return
	}
	e.reply(strings.TrimPrefix(source, drop.SenderPrefix), drop.Reply{
		Kind: drop.ReplyStatus, Re: t.SourceID, Task: t.ID, State: string(t.State),
		Body: fmt.Sprintf("Task %s is %s.", t.ID, t.State),
	})
}

// answerDrop writes the lead's answer to a program that dropped a file. A name with no earlier drop fails, because no program reads its folder.
func (e *Engine) answerDrop(name, body string) error {
	if e.drops == nil {
		return errors.New("this sfy process has no drop folder")
	}
	if !e.drops.Known(name) {
		return fmt.Errorf("no program dropped a file as %s%s, so nobody would read the answer", drop.SenderPrefix, name)
	}
	return e.writeReply(name, drop.Reply{From: protocol.Lead, Kind: drop.ReplyMessage, Body: body})
}
