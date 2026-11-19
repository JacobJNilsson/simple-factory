package engine_test

import (
	"slices"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

const (
	restartNote      = "sfy restarted."
	typedRestartNote = "main: " + restartNote
	heldAnswer       = "The main agent gets the note when it waits."
)

// Each test builds a fresh engine, so it knows nothing about main, as after sfy restart.

func TestTheRestartNoteGoesThroughTheProviderAndNothingIsTyped(t *testing.T) {
	v := newEnv(t)
	v.l.canDeliver = true

	a := v.run("", "", "restarted", restartNote)

	mustOK(t, a)
	if want := []string{"main: " + restartNote}; !slices.Equal(v.l.delivered, want) || len(v.l.sent) != 0 || a.Text != "" {
		t.Fatalf("delivered %q, typed %q, text %q", v.l.delivered, v.l.sent, a.Text)
	}
}

func TestTheRestartNoteWaitsForTheTurnEndOfMain(t *testing.T) {
	v := newEnv(t)

	a := v.run("", "", "restarted", restartNote)

	mustOK(t, a)
	if len(v.l.sent) != 0 || a.Text != heldAnswer {
		t.Fatalf("typed %q, text %q", v.l.sent, a.Text)
	}
	mustOK(t, v.run("main", "", "hook turn-ended", ""))
	if want := []string{typedRestartNote}; !slices.Equal(v.l.sent, want) {
		t.Fatalf("typed %q, want %q", v.l.sent, want)
	}
}

func TestTheRestartNoteWaitsForABusyRegisteredMainEvenWhenTheProviderCanDeliver(t *testing.T) {
	v := newEnv(t)
	v.l.canDeliver = true
	v.e.SetRegistration(&fakeRegistration{registered: true})
	mustOK(t, v.run("main", "", "hook busy", ""))

	mustOK(t, v.run("", "", "restarted", restartNote))

	if len(v.l.sent) != 0 || len(v.l.delivered) != 0 {
		t.Fatalf("typed %q, delivered %q", v.l.sent, v.l.delivered)
	}
	mustOK(t, v.run("main", "", "hook idle", ""))
	if want := []string{typedRestartNote}; !slices.Equal(v.l.sent, want) {
		t.Fatalf("typed %q, want %q", v.l.sent, want)
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
