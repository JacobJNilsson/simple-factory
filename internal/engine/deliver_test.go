package engine_test

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestAMessageForAnAgentThatWaitsGoesThroughTheProviderWithItsSenderAndText(t *testing.T) {
	v := newEnv(t)
	v.l.canDeliver = true
	waitingWorker(v, t)

	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))

	if want := []string{"worker-t1: From main:\nPlease rebase."}; !slices.Equal(v.l.delivered, want) {
		t.Fatalf("delivered %q, want %q", v.l.delivered, want)
	}
	if len(v.l.sent) != 0 {
		t.Fatalf("typed %v", v.l.sent)
	}
}

func TestADeliveredMessageStaysUnreadUntilTheSessionTakesIt(t *testing.T) {
	v := newEnv(t)
	v.l.canDeliver = true
	waitingWorker(v, t)

	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))

	if got := v.inbox(t, "worker-t1"); len(got) != 1 {
		t.Fatalf("unread after delivery: %+v", got)
	}
	if a := v.run("worker-t1", "", "inbox", ""); a.Text != "From main:\nPlease rebase.\n\n" {
		t.Fatalf("sfy inbox shows %q", a.Text)
	}
}

func TestQueuedMessagesAreLoggedAsQueuedAndNotAsDelivered(t *testing.T) {
	v := newEnv(t)
	var log bytes.Buffer
	v.e.SetLog(&log)
	v.l.canDeliver = true
	waitingWorker(v, t)

	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))

	if got := log.String(); !strings.Contains(got, "messages queued in the note file of worker-t1") || strings.Contains(got, "delivered to") {
		t.Fatalf("log %q", got)
	}
}

func TestAnIdleSessionWhoseHookLeavesTheMessageUnreadGetsTheNoteTyped(t *testing.T) {
	v := newEnv(t)
	var log bytes.Buffer
	v.e.SetLog(&log)
	v.l.canDeliver = true
	waitingWorker(v, t)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))
	v.clock.now = v.clock.now.Add(5 * time.Second)

	v.tick(t)

	if want := []string{"worker-t1: " + note}; !slices.Equal(v.l.sent, want) {
		t.Fatalf("typed %q, want %q", v.l.sent, want)
	}
	if !strings.Contains(log.String(), "note typed to worker-t1: the hook left its messages unread") {
		t.Fatalf("log %q", log.String())
	}
}

func TestNoNoteIsTypedWhileTheHookStillHasTime(t *testing.T) {
	v := newEnv(t)
	v.l.canDeliver = true
	waitingWorker(v, t)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))
	v.clock.now = v.clock.now.Add(time.Second)

	v.tick(t)

	if len(v.l.sent) != 0 {
		t.Fatalf("typed %q", v.l.sent)
	}
}

func TestAMessageThatTheHookTookIsLoggedAndNeverTyped(t *testing.T) {
	v := newEnv(t)
	var log bytes.Buffer
	v.e.SetLog(&log)
	v.l.canDeliver = true
	waitingWorker(v, t)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))
	msgs := v.inbox(t, "worker-t1")
	if err := v.st.MarkRead("worker-t1", []string{msgs[0].ID}); err != nil {
		t.Fatal(err)
	}
	v.clock.now = v.clock.now.Add(time.Minute)

	v.tick(t)

	if len(v.l.sent) != 0 || !strings.Contains(log.String(), "messages taken by the hook of worker-t1") {
		t.Fatalf("typed %q, log %q", v.l.sent, log.String())
	}
}

func TestInboxShowsTheTextThatNoHookDrained(t *testing.T) {
	v := newEnv(t)
	v.l.drained = "Task t1 is approved."

	a := v.run("worker-t1", "", "inbox", "")

	if a.Text != "Task t1 is approved.\n" {
		t.Fatalf("sfy inbox shows %q", a.Text)
	}
}

func TestInboxShowsTheMessagesWhenTheDrainFails(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))
	v.l.drainErr = errors.New("no folder")

	a := v.run("worker-t1", "", "inbox", "")

	mustOK(t, a)
	if a.Text != "From main:\nPlease rebase.\n\n" {
		t.Fatalf("sfy inbox shows %q", a.Text)
	}
	if got := v.inbox(t, "worker-t1"); len(got) != 0 {
		t.Fatalf("unread %+v", got)
	}
}

func TestATypedNoteForASessionWithStuckTextPointsToInbox(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.l.stuck = true

	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	want := []string{"worker-t1: " + reminder, "worker-t1: You have undelivered notes. Run: sfy inbox"}
	if !slices.Equal(v.l.sent, want) {
		t.Fatalf("typed %q, want %q", v.l.sent, want)
	}
}

func TestSeveralUnreadMessagesAreDeliveredOneByOne(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "one"))
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "two"))
	v.l.canDeliver = true

	mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	if want := []string{"worker-t1: From main:\none", "worker-t1: From main:\ntwo"}; !slices.Equal(v.l.delivered, want) {
		t.Fatalf("delivered %q, want %q", v.l.delivered, want)
	}
}

func TestAMessageTheProviderCannotDeliverIsTypedAndStaysUnread(t *testing.T) {
	v := newEnv(t)
	waitingWorker(v, t)

	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))

	if !slices.Equal(v.l.sent, []string{"worker-t1: " + note}) {
		t.Fatalf("typed %v", v.l.sent)
	}
	if got := v.inbox(t, "worker-t1"); len(got) != 1 {
		t.Fatalf("unread %+v", got)
	}
}

func TestAFailedDeliveryFallsBackToTheTypedNote(t *testing.T) {
	v := newEnv(t)
	v.l.deliverErr = errors.New("disk full")
	waitingWorker(v, t)

	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))

	if !slices.Equal(v.l.sent, []string{"worker-t1: " + note}) {
		t.Fatalf("typed %v", v.l.sent)
	}
	if got := v.inbox(t, "worker-t1"); len(got) != 1 {
		t.Fatalf("unread %+v", got)
	}
}

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

// busyWorker puts a worker in the middle of a turn that the operator or a message started.
func busyWorker(v env, t *testing.T) {
	t.Helper()
	waitingWorker(v, t)
	mustOK(t, v.run("worker-t1", "t1", "hook busy", ""))
}

func TestABusySessionWithAHookGetsItsMessageQueuedAndNeverTyped(t *testing.T) {
	v := newEnv(t)
	v.l.canDeliver = true
	busyWorker(v, t)

	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))
	v.clock.now = v.clock.now.Add(time.Minute)
	v.tick(t)

	if want := []string{"worker-t1: From main:\nPlease rebase."}; !slices.Equal(v.l.delivered, want) {
		t.Fatalf("delivered %q, want %q", v.l.delivered, want)
	}
	if len(v.l.sent) != 0 {
		t.Fatalf("typed %q", v.l.sent)
	}
}

func TestABusySessionWithoutAHookGetsNothingTypedUntilItsTurnEnds(t *testing.T) {
	v := newEnv(t)
	busyWorker(v, t)

	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))
	v.clock.now = v.clock.now.Add(time.Minute)
	v.tick(t)
	if len(v.l.sent) != 0 {
		t.Fatalf("typed %q during the turn", v.l.sent)
	}

	mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))
	if want := []string{"worker-t1: " + note}; !slices.Equal(v.l.sent, want) {
		t.Fatalf("typed %q, want %q", v.l.sent, want)
	}
}

func TestANoteStaysQueuedAfterTheGraceWhileTheTurnRuns(t *testing.T) {
	v := newEnv(t)
	v.l.canDeliver = true
	waitingWorker(v, t)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))
	mustOK(t, v.run("worker-t1", "t1", "hook busy", ""))

	v.clock.now = v.clock.now.Add(time.Minute)
	v.tick(t)

	if len(v.l.sent) != 0 {
		t.Fatalf("typed %q", v.l.sent)
	}
}

func TestTheTurnEndGivesTheHookAFreshGraceBeforeTheNoteIsTyped(t *testing.T) {
	v := newEnv(t)
	v.l.canDeliver = true
	busyWorker(v, t)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "Please rebase."))
	v.clock.now = v.clock.now.Add(time.Minute)
	mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	v.tick(t)
	if len(v.l.sent) != 0 {
		t.Fatalf("typed %q before the grace ended", v.l.sent)
	}
	v.clock.now = v.clock.now.Add(5 * time.Second)
	v.tick(t)

	if want := []string{"worker-t1: " + note}; !slices.Equal(v.l.sent, want) {
		t.Fatalf("typed %q, want %q", v.l.sent, want)
	}
}

func TestTheNoteFallbackWaitsForTheConfiguredTimeAndTheDefaultIsLong(t *testing.T) {
	cases := []struct {
		name    string
		after   time.Duration
		elapsed time.Duration
		typed   bool
	}{
		{"configured, not yet", 10 * time.Minute, 9 * time.Minute, false},
		{"configured, reached", 10 * time.Minute, 10 * time.Minute, true},
		{"default, not yet", 0, 4*time.Minute + 59*time.Second, false},
		{"default, reached", 0, 5 * time.Minute, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := newEnv(t)
			cfg := config.Config{ResendMinutes: 60, MaxWorkers: 2, NoteFallbackAfter: c.after}
			v.e = engine.New(v.st, v.l, v.ws, cfg, v.clock)
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

func TestABurstOfMessagesToAWaitingSessionIsDeliveredOnceEach(t *testing.T) {
	v := newEnv(t)
	v.l.canDeliver = true
	waitingWorker(v, t)

	for _, body := range []string{"one", "two", "three"} {
		mustOK(t, v.run("main", "", "send", "", "worker-t1", body))
	}

	want := []string{"worker-t1: From main:\none", "worker-t1: From main:\ntwo", "worker-t1: From main:\nthree"}
	if !slices.Equal(v.l.delivered, want) || len(v.l.sent) != 0 {
		t.Fatalf("delivered %q, typed %q", v.l.delivered, v.l.sent)
	}
}

func TestABurstOfMessagesToABusySessionIsDeliveredOnceEach(t *testing.T) {
	v := newEnv(t)
	v.l.canDeliver = true
	busyWorker(v, t)

	for _, body := range []string{"one", "two", "three"} {
		mustOK(t, v.run("main", "", "send", "", "worker-t1", body))
	}
	v.clock.now = v.clock.now.Add(time.Minute)
	v.tick(t)

	want := []string{"worker-t1: From main:\none", "worker-t1: From main:\ntwo", "worker-t1: From main:\nthree"}
	if !slices.Equal(v.l.delivered, want) || len(v.l.sent) != 0 {
		t.Fatalf("delivered %q, typed %q", v.l.delivered, v.l.sent)
	}
}

func TestARegisteredMainAgentKeepsTheTypedNote(t *testing.T) {
	v := newEnv(t)
	v.l.canDeliver = true
	v.e.SetRegistration(&fakeRegistration{registered: true})
	mustOK(t, v.run("main", "", "hook turn-ended", ""))

	mustOK(t, v.run("", "", "task new", taskFile))

	if len(v.l.delivered) != 0 || !slices.Equal(v.l.sent, []string{"main: " + note}) {
		t.Fatalf("delivered %q, typed %q", v.l.delivered, v.l.sent)
	}
}
