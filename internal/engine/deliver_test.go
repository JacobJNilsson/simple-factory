package engine_test

import (
	"errors"
	"slices"
	"testing"

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

func TestInboxShowsTheTextThatNoHookDrained(t *testing.T) {
	v := newEnv(t)
	v.l.drained = "Task t1 is approved."

	a := v.run("worker-t1", "", "inbox", "")

	if a.Text != "Task t1 is approved.\n" {
		t.Fatalf("sfy inbox shows %q", a.Text)
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
