package engine_test

import (
	"slices"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

const (
	restartNote      = "sfy restarted."
	typedRestartNote = "lead: " + restartNote
	heldAnswer       = "The lead gets the note when it waits."
)

// Each test builds a fresh engine, so it knows nothing about the lead, as after sfy restart.

func TestTheRestartNoteGoesThroughTheProviderAndNothingIsTyped(t *testing.T) {
	v := newEnv(t)
	v.ch.canDeliver = true

	a := v.run("", "", "restarted", restartNote)

	mustOK(t, a)
	if want := []string{"lead: " + restartNote}; !slices.Equal(v.ch.delivered, want) || len(v.l.sent) != 0 || a.Text != "" {
		t.Fatalf("delivered %q, typed %q, text %q", v.ch.delivered, v.l.sent, a.Text)
	}
}

func TestTheRestartNoteWaitsForTheTurnEndOfLead(t *testing.T) {
	v := newEnv(t)

	a := v.run("", "", "restarted", restartNote)

	mustOK(t, a)
	if len(v.l.sent) != 0 || a.Text != heldAnswer {
		t.Fatalf("typed %q, text %q", v.l.sent, a.Text)
	}
	mustOK(t, v.run("lead", "", "hook turn-ended", ""))
	if want := []string{typedRestartNote}; !slices.Equal(v.l.sent, want) {
		t.Fatalf("typed %q, want %q", v.l.sent, want)
	}
}

func TestTheRestartNoteWaitsForABusyRegisteredLeadEvenWhenTheProviderCanDeliver(t *testing.T) {
	v := newEnv(t)
	v.ch.canDeliver = true
	v.e.SetRegistration(&fakeRegistration{registered: true})
	mustOK(t, v.run("lead", "", "hook busy", ""))

	mustOK(t, v.run("", "", "restarted", restartNote))

	if len(v.l.sent) != 0 || len(v.ch.delivered) != 0 {
		t.Fatalf("typed %q, delivered %q", v.l.sent, v.ch.delivered)
	}
	mustOK(t, v.run("lead", "", "hook idle", ""))
	if want := []string{typedRestartNote}; !slices.Equal(v.l.sent, want) {
		t.Fatalf("typed %q, want %q", v.l.sent, want)
	}
}

func TestTheRestartNoteToALeadThatDoesNotRunSaysSo(t *testing.T) {
	v := newEnv(t)
	v.l.dead = map[string]bool{"lead": true}

	a := v.run("", "", "restarted", restartNote)

	mustOK(t, a)
	if len(v.l.sent) != 0 || a.Text != "No lead runs, so no note went out." {
		t.Fatalf("typed %q, text %q", v.l.sent, a.Text)
	}
}

func TestAWorkerCannotSendTheRestartNote(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	mustFail(t, v.run("worker-t1", "t1", "restarted", restartNote), "not for a worker")
}

func TestTheRestartNeedsANote(t *testing.T) {
	v := newEnv(t)

	mustFail(t, v.run("", "", "restarted", " "), "needs a note")
}
