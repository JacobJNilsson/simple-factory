package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// oneSlot replaces the engine with one that has one worker slot and the given queue_wait_note.
func (v *env) oneSlot(wait time.Duration) {
	cfg := config.Config{ReviewRounds: 3, MaxWorkers: 1, ResendMinutes: 5, QueueWaitNote: wait}
	v.e = engine.New(v.st, v.l, v.ch, v.ws, cfg, v.clock)
}

// slotNotes returns the lead's messages that say a queued task waits for a slot.
func (v env) slotNotes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, m := range v.inbox(t, "lead") {
		if strings.Contains(m.Body, "for a worker slot") {
			out = append(out, m.Body)
		}
	}
	return out
}

// after moves the clock on and ticks.
func (v env) after(t *testing.T, d time.Duration) {
	t.Helper()
	v.clock.now = v.clock.now.Add(d)
	v.tick(t)
}

// blockedFirst queues t1 and t2, starts the worker of t1, and lets it report blocked.
func (v env) blockedFirst(t *testing.T) {
	t.Helper()
	mustOK(t, v.run("lead", "", "task new", taskFile))
	mustOK(t, v.run("lead", "", "task new", taskFile))
	v.tick(t)
	mustOK(t, v.run("worker-t1", "t1", "report blocked", "Which branch?"))
	v.tick(t)
}

func TestALeadHearsOnceWhenAQueuedTaskWaitsBehindABlockedTask(t *testing.T) {
	v := newEnv(t)
	v.oneSlot(10 * time.Minute)
	v.blockedFirst(t)

	v.after(t, 9*time.Minute)
	if got := v.slotNotes(t); len(got) != 0 {
		t.Fatalf("the lead got %q", got)
	}
	v.after(t, time.Minute)
	got := v.slotNotes(t)
	if len(got) != 1 {
		t.Fatalf("the lead got %q", got)
	}
	for _, want := range []string{"Task t2 has waited 10 minutes for a worker slot.", "Task t1 holds a slot", "reported blocked"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("the note lacks %q: %s", want, got[0])
		}
	}
	v.after(t, 10*time.Minute)
	if got := v.slotNotes(t); len(got) != 1 {
		t.Fatalf("the lead got %q", got)
	}
}

func TestALeadHearsWhenAQueuedTaskWaitsBehindAPush(t *testing.T) {
	v := newEnv(t)
	v.oneSlot(10 * time.Minute)
	v.firstReview(t)
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Looks right."))
	if err := v.st.Create(task.Task{ID: "t2", Title: "Title", Description: "Do it.", Goal: "Done.", State: task.Queued, Source: "lead"}); err != nil {
		t.Fatal(err)
	}
	v.tick(t)

	v.after(t, 10*time.Minute)

	got := v.slotNotes(t)
	if len(got) != 1 || !strings.Contains(got[0], "Task t1 holds a slot and has waited 10 minutes for its worker to push.") {
		t.Fatalf("the lead got %q", got)
	}
	for _, s := range v.l.started {
		if s.Name == "worker-t2" {
			t.Fatal("the worker of t2 started")
		}
	}
}

func TestALaterReportClearsTheBlock(t *testing.T) {
	v := newEnv(t)
	v.oneSlot(10 * time.Minute)
	v.blockedFirst(t)
	mustOK(t, v.run("worker-t1", "t1", "report progress", "Going on."))

	v.after(t, 10*time.Minute)

	if got := v.slotNotes(t); len(got) != 0 {
		t.Fatalf("the lead got %q", got)
	}
}

func TestAnAnswerOfTheLeadClearsTheBlock(t *testing.T) {
	for _, answer := range []func(v env) string{
		func(v env) string { return v.run("lead", "", "send", "Use main.", "worker-t1").Error },
		func(v env) string { return v.run("lead", "", "task feedback", "Use main.", "t1").Error },
	} {
		v := newEnv(t)
		v.oneSlot(10 * time.Minute)
		v.blockedFirst(t)
		if err := answer(v); err != "" {
			t.Fatal(err)
		}

		v.after(t, 10*time.Minute)

		if got := v.slotNotes(t); len(got) != 0 {
			t.Fatalf("the lead got %q", got)
		}
	}
}

func TestNoNoteWhenTheSlotsHoldOnlyRunningWork(t *testing.T) {
	v := newEnv(t)
	v.oneSlot(10 * time.Minute)
	mustOK(t, v.run("lead", "", "task new", taskFile))
	mustOK(t, v.run("lead", "", "task new", taskFile))
	v.tick(t)

	v.after(t, 10*time.Minute)

	if got := v.slotNotes(t); len(got) != 0 {
		t.Fatalf("the lead got %q", got)
	}
}

func TestAQueueWaitNoteOfZeroSendsNoNote(t *testing.T) {
	v := newEnv(t)
	v.oneSlot(0)
	v.blockedFirst(t)

	v.after(t, time.Hour)

	if got := v.slotNotes(t); len(got) != 0 {
		t.Fatalf("the lead got %q", got)
	}
}

func TestAQueuedTaskThatWaitsForADependencyGetsNoSlotNote(t *testing.T) {
	v := newEnv(t)
	v.oneSlot(10 * time.Minute)
	v.blockedFirst(t)
	mustOK(t, v.run("lead", "", "task new", withAfter("t2")))
	v.tick(t)

	v.after(t, 10*time.Minute)

	for _, n := range v.slotNotes(t) {
		if strings.Contains(n, "Task t3 has waited") {
			t.Fatalf("the lead got %q", n)
		}
	}
}
