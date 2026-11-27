package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
)

// wakeNote is the first message of a woken worker or reviewer.
const wakeNote = "sfy woke this session, because a message arrived for it. Run: sfy inbox"

// leadWokeNote is the notify text after a lead wake that worked, so the operator looks at the lead's dialogs.
const leadWokeNote = "sfy woke the lead, because a message arrived for it."

// wakeWindow is the span in which wake.max_per_hour counts the wakes of one session.
const wakeWindow = time.Hour

// wakes reports whether the kind of the named session has waking on.
func (e *Engine) wakes(name string) bool {
	k, ok := e.kindOf(name)
	if !ok {
		return false
	}
	switch k {
	case worker:
		return e.cfg.Wake.Worker
	case reviewer:
		return e.cfg.Wake.Reviewer
	case lead:
		// sfy did not start a registered lead, so it has no settings or conversation to resume.
		return name == protocol.Lead && e.cfg.Wake.Lead && e.leadSpec != nil && !e.registeredLead()
	}
	return false
}

// markWake makes the next tick wake the named session, when its kind has waking on.
func (e *Engine) markWake(name string) {
	if e.wakes(name) {
		e.wakeDue[name] = true
	}
}

// wakeSessions is the tick step that wakes each session that got a message since the last tick.
// It checks each session again, because its task may have moved on since the message.
func (e *Engine) wakeSessions() error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(e.wakeDue)) {
		delete(e.wakeDue, name)
		ok, err := e.canWake(name)
		if err != nil {
			errs = append(errs, err)
		}
		if ok {
			errs = append(errs, e.wakeOne(name))
		}
	}
	return errors.Join(errs...)
}

// canWake reports whether sfy may wake the named session now.
// The session's task must still need it, its alive check must answer gone, it must have an unread message, and the cap must allow one more wake.
// The error comes from the inbox or from the note that tells the lead about the cap.
func (e *Engine) canWake(name string) (bool, error) {
	if e.down.Load() || !e.wakes(name) || e.live(name) != nil || !e.needsReviewer(name) {
		return false, nil
	}
	if e.sess.probe(name) != sessionGone {
		return false, nil
	}
	unread, err := e.st.Unread(name)
	if err != nil || len(unread) == 0 {
		return false, err
	}
	return e.underCap(name)
}

// needsReviewer reports whether a session is no reviewer, or the reviewer that its task waits for.
// A reviewer that gave its verdict is still the reviewer of the round, but no task needs it any more.
func (e *Engine) needsReviewer(name string) bool {
	id, ok := e.sess.parse(name)
	if !ok || id.kind != reviewer {
		return true
	}
	t, err := e.st.Task(id.task)
	return err == nil && e.waitsForVerdict(t)
}

// underCap reports whether the session woke fewer than wake.max_per_hour times in the last hour.
// When the cap stops a wake, it tells the lead once for each window.
func (e *Engine) underCap(name string) (bool, error) {
	now := e.c.Now()
	times := slices.DeleteFunc(e.wakeTimes[name], func(at time.Time) bool { return now.Sub(at) >= wakeWindow })
	e.wakeTimes[name] = times
	if len(times) < e.cfg.Wake.MaxPerHour {
		delete(e.wakeCapTold, name)
		return true, nil
	}
	if e.wakeCapTold[name] {
		return false, nil
	}
	e.wakeCapTold[name] = true
	until := times[0].Add(wakeWindow).Format("15:04")
	e.logf("woke %s %d times in the last hour, so it wakes it no more before %s", name, len(times), until)
	// The lead cannot hear of its own cap, so only the log names it.
	if name == protocol.Lead {
		return false, nil
	}
	return false, e.message("lead", "sfy", fmt.Sprintf(
		"sfy woke %s %d times in the last hour, so it wakes it no more before %s. Its messages wait in its inbox.", name, len(times), until))
}

// wakeOne resumes the recorded conversation of a session, and counts the wake toward the cap.
// A failed wake of a worker or a reviewer leaves the message unread and the task as it is, and tells the lead.
func (e *Engine) wakeOne(name string) error {
	e.wakeTimes[name] = append(e.wakeTimes[name], e.c.Now())
	if name == protocol.Lead {
		e.wakeLead()
		return nil
	}
	id, _ := e.sess.parse(name)
	t, err := e.st.Task(id.task)
	if err == nil {
		err = e.sess.resume(t, e.sess.sessionAgent(t, id.kind == reviewer), wakeNote)
	}
	if err != nil {
		e.logf("could not wake %s: %v", name, err)
		return e.message("lead", "sfy", fmt.Sprintf(
			"sfy could not wake %s: %v. Its messages wait in its inbox. Run sfy task feedback %s <text> to start a new worker, or sfy task cancel %s.",
			name, err, id.task, id.task))
	}
	e.logf("woke %s: a message waits", name)
	return nil
}

// wakeLead resumes the lead's conversation and tells the operator through the notify template.
// A failure stores no message, because no session can hear it. A failed notify goes to the log only.
func (e *Engine) wakeLead() {
	text := leadWokeNote
	spec, err := e.leadSpec()
	if err == nil {
		err = e.sess.resumeLead(spec)
	}
	if err != nil {
		e.logf("could not wake lead: %v", err)
		text = "sfy could not wake the lead: " + err.Error()
	} else {
		e.logf("woke lead: a message waits")
	}
	if err := e.l.Notify(protocol.Lead, text); err != nil {
		e.logf("notify of lead failed: %v", err)
	}
}
