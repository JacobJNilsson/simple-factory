package engine_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestTheReminderGoesThroughTheProvider(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.l.canDeliver = true

	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	if !slices.Equal(v.l.delivered, []string{"worker-t1: " + reminder}) || len(v.l.sent) != 0 {
		t.Fatalf("delivered %q, typed %q", v.l.delivered, v.l.sent)
	}
}

func TestTheReminderIsTypedWhenTheProviderCannotDeliver(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.l.deliverErr = errors.New("no session")

	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	if !slices.Equal(v.l.sent, []string{"worker-t1: " + reminder}) {
		t.Fatalf("typed %q", v.l.sent)
	}
}

func TestAMessageToAWaitingWorkerGoesThroughTheProviderWithItsSenderAndText(t *testing.T) {
	v := newEnv(t)
	v.l.canDeliver = true
	waitingWorker(v, t)

	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))

	if want := []string{"worker-t1: From main:\nPlease rebase."}; !slices.Equal(v.l.delivered, want) || len(v.l.sent) != 0 {
		t.Fatalf("delivered %q, typed %q", v.l.delivered, v.l.sent)
	}
}

// A config without note_fallback_after, as tests build it, gets the default of five minutes.
func TestTheNoteFallbackDefaultIsFiveMinutes(t *testing.T) {
	cases := []struct {
		name    string
		elapsed time.Duration
		typed   bool
	}{
		{"not yet", 4*time.Minute + 59*time.Second, false},
		{"reached", 5 * time.Minute, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := newEnv(t)
			v.e = engine.New(v.st, v.l, v.ws, config.Config{ResendMinutes: 60, MaxWorkers: 2}, v.clock)
			v.l.canDeliver = true
			waitingWorker(v, t)
			mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))

			v.clock.now = v.clock.now.Add(c.elapsed)
			v.tick(t)

			if got := len(v.l.sent) > 0; got != c.typed {
				t.Fatalf("typed %q, want typed %v", v.l.sent, c.typed)
			}
		})
	}
}
