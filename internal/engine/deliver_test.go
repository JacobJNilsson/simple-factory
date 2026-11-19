package engine_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestTheReminderGoesThroughTheProvider(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.ch.canDeliver = true

	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	if !slices.Equal(v.ch.delivered, []string{"worker-t1: " + reminder}) || len(v.l.sent) != 0 {
		t.Fatalf("delivered %q, typed %q", v.ch.delivered, v.l.sent)
	}
}

func TestTheReminderIsTypedWhenTheProviderCannotDeliver(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.ch.deliverErr = errors.New("no session")

	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	if !slices.Equal(v.l.sent, []string{"worker-t1: " + reminder}) {
		t.Fatalf("typed %q", v.l.sent)
	}
}

func TestAMessageToAWaitingWorkerGoesThroughTheProviderWithItsSenderAndText(t *testing.T) {
	v := newEnv(t)
	v.ch.canDeliver = true
	waitingWorker(v, t)

	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))

	if want := []string{"worker-t1: From main:\nPlease rebase."}; !slices.Equal(v.ch.delivered, want) || len(v.l.sent) != 0 {
		t.Fatalf("delivered %q, typed %q", v.ch.delivered, v.l.sent)
	}
}

func TestAMessageFromTheOperatorToMainIsStoredAndDeliveredWithTheOperatorAsSender(t *testing.T) {
	v := newEnv(t)
	v.ch.canDeliver = true
	mustOK(t, v.run("main", "", "hook idle", ""))

	mustOK(t, v.run("", "", "send", "", "main", "Build", "it."))

	if want := []string{"main: From operator:\nBuild it."}; !slices.Equal(v.ch.delivered, want) || len(v.l.sent) != 0 {
		t.Fatalf("delivered %q, typed %q", v.ch.delivered, v.l.sent)
	}
	a := v.run("main", "", "inbox", "")
	mustOK(t, a)
	if !strings.Contains(a.Text, "operator") || !strings.Contains(a.Text, "Build it.") {
		t.Fatalf("inbox %q", a.Text)
	}
}

func TestAMessageFromTheOperatorToABusyMainIsNotTyped(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("main", "", "hook busy", ""))

	mustOK(t, v.run("", "", "send", "", "main", "Build it."))

	if len(v.l.sent) != 0 || len(v.ch.delivered) != 0 {
		t.Fatalf("typed %q, delivered %q", v.l.sent, v.ch.delivered)
	}
}

func TestTheOperatorMaySendOnlyToMain(t *testing.T) {
	v := newEnv(t)
	waitingWorker(v, t)

	for _, to := range []string{"worker-t1", "review-t1-1", "nobody"} {
		mustFail(t, v.run("", "", "send", "", to, "hi"), "only to main")
	}
}

func TestWorkersAndReviewersMayNotSend(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	mustFail(t, v.run("worker-t1", "t1", "send", "", "main", "hi"), "send")
	mustFail(t, v.run("review-t1-1", "t1", "send", "", "main", "hi"), "verdict pass")
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
			v.e = engine.New(v.st, v.l, v.ch, v.ws, config.Config{ResendMinutes: 60, MaxWorkers: 2}, v.clock)
			v.ch.canDeliver = true
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
