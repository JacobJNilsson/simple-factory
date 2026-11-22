package engine

import (
	"errors"

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
		return e.rejectDrop(path, err.Error())
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
	return e.notes.Message(protocol.Lead, "new message")
}

// dropTask creates a task in new from a drop. A body that is no valid task file rejects the drop.
func (e *Engine) dropTask(path string, d drop.Drop) error {
	t, err := task.Parse(d.Body)
	if err != nil {
		return e.rejectDrop(path, "the task file is not valid: "+err.Error())
	}
	t, _, err = e.createTask(t, d.Sender())
	if err != nil && t.ID != "" {
		if _, lookErr := e.st.Task(t.ID); lookErr == nil {
			// The task exists and only its note failed, so the drop counts as taken.
			return errors.Join(err, e.drops.Done(path, d))
		}
	}
	if err != nil {
		return e.rejectDrop(path, "sfy could not create the task: "+err.Error())
	}
	return e.drops.Done(path, d)
}

// rejectDrop moves a drop to rejected/ with the reason, and logs it.
func (e *Engine) rejectDrop(path, why string) error {
	e.logf("drop %s rejected: %s", path, why)
	return e.drops.Reject(path, why)
}
