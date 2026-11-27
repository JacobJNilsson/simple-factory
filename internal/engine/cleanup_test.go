package engine_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// cleanupRun counts the ticks of an engine and records the ticks at which a cleanup try ran.
type cleanupRun struct {
	v     env
	tick  int
	tries []int
}

// failingCleanup seeds a cancelled task whose workspace removal fails, and records each cleanup try.
func failingCleanup(t *testing.T, v env) *cleanupRun {
	t.Helper()
	v.seed(t, task.Cancelled, 0)
	v.ws.removeErr = errors.New("permission denied")
	r := &cleanupRun{v: v}
	v.ws.beforeCheck = func() { r.tries = append(r.tries, r.tick) }
	return r
}

// ticks runs n ticks.
func (r *cleanupRun) ticks(n int) {
	for range n {
		r.tick++
		_ = r.v.e.Tick()
	}
}

// leadNotes returns the bodies of the unread messages of the lead.
func leadNotes(t *testing.T, v env) []string {
	t.Helper()
	var bodies []string
	for _, m := range v.inbox(t, "lead") {
		bodies = append(bodies, m.Body)
	}
	return bodies
}

// A done task whose workspace removal fails keeps its Dir, and the next ticks wait before they stop its sessions again.
func TestTickWaitsBeforeItRetriesTheCleanupOfADoneTaskWhoseRemoveFails(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Done, 1)
	v.ws.removeErr = errors.New("permission denied")

	for range 3 {
		_ = v.e.Tick()
	}

	if v.l.stopCalls != 2 || v.state(t, "t1").Dir == "" {
		t.Fatalf("stop calls %d, dir %q", v.l.stopCalls, v.state(t, "t1").Dir)
	}
}

// A stop that fails while alive still answers "runs" waits before the next try.
func TestTickWaitsBeforeItRetriesAFailedStopWhileAliveSaysRuns(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Done, 1)
	v.l.stopErr = errors.New("rex: no such window")

	for range 5 {
		_ = v.e.Tick()
	}

	if v.l.stopCalls != 1 {
		t.Fatalf("stop calls %d", v.l.stopCalls)
	}
}

func TestCleanupRetriesWaitLongerAfterEachFailureUpToAboutFiveMinutes(t *testing.T) {
	v := newEnv(t)
	r := failingCleanup(t, v)

	r.ticks(457)

	if want := []int{1, 12, 33, 74, 155, 306, 457}; !slices.Equal(r.tries, want) {
		t.Fatalf("tries at ticks %v, want %v", r.tries, want)
	}
}

func TestTheLeadHearsOnceWhenThreeCleanupRetriesInARowFail(t *testing.T) {
	v := newEnv(t)
	r := failingCleanup(t, v)

	r.ticks(32)
	if n := leadNotes(t, v); len(n) != 0 {
		t.Fatalf("the lead got %q after 32 ticks", n)
	}
	r.ticks(1)
	want := []string{"sfy cannot clean up task t1: permission denied"}
	if n := leadNotes(t, v); !slices.Equal(n, want) {
		t.Fatalf("the lead got %q after 33 ticks", n)
	}
	r.ticks(457 - 33)
	if n := leadNotes(t, v); !slices.Equal(n, want) {
		t.Fatalf("the lead got %q after 457 ticks", n)
	}
}

func TestAChangeToTheTaskResetsTheCleanupBackoff(t *testing.T) {
	v := newEnv(t)
	r := failingCleanup(t, v)
	r.ticks(33)

	v.clock.now = v.clock.now.Add(time.Minute)
	mustOK(t, v.run("lead", "", "task archive", "", "t1"))
	r.ticks(1)
	if len(r.tries) != 4 {
		t.Fatalf("tries %v after the change", r.tries)
	}
	r.ticks(11)
	if len(r.tries) != 5 {
		t.Fatalf("tries %v 11 ticks after the change", r.tries)
	}
	r.ticks(21)
	if len(r.tries) != 6 {
		t.Fatalf("tries %v 32 ticks after the change", r.tries)
	}
	if n := leadNotes(t, v); len(n) != 1 {
		t.Fatalf("the lead got %q", n)
	}
}

func TestASuccessfulCleanupRetryForgetsTheBackoff(t *testing.T) {
	v := newEnv(t)
	r := failingCleanup(t, v)
	r.ticks(12)

	v.ws.removeErr = nil
	r.ticks(21)

	if d := v.state(t, "t1").Dir; d != "" || !slices.Equal(v.ws.removed, []string{"t1"}) {
		t.Fatalf("dir %q, removed %v", d, v.ws.removed)
	}
	if n := leadNotes(t, v); len(n) != 0 {
		t.Fatalf("the lead got %q", n)
	}
}
