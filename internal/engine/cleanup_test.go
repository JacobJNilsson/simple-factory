package engine_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// An approved task gets one stop for each of its sessions, however many ticks run.
func TestTickStopsTheSessionsOfAnApprovedTaskOnce(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 2)

	for range 3 {
		v.tick(t)
	}

	want := []string{"worker-t1", "review-t1-1", "review-t1-2"}
	if !slices.Equal(v.l.stopped, want) {
		t.Fatalf("stopped %v", v.l.stopped)
	}
}

func TestTickStopsNothingAfterAPassWhoseStopsWorked(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))

	v.tick(t)
	v.tick(t)

	if want := []string{"worker-t1", "review-t1-1"}; !slices.Equal(v.l.stopped, want) {
		t.Fatalf("stopped %v", v.l.stopped)
	}
}

func TestTickEndsTheSessionsOnceMoreWhenTheStopAfterThePassFailed(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)
	v.l.stopErr = errors.New("stop failed")
	mustFail(t, v.run("review-t1-1", "t1", "verdict pass", "Good."), "stop failed")
	if s := v.state(t, "t1").State; s != task.Approved {
		t.Fatalf("state %s", s)
	}
	v.l.stopErr = nil

	v.tick(t)
	v.tick(t)

	if want := []string{"worker-t1", "review-t1-1"}; !slices.Equal(v.l.stopped, want) {
		t.Fatalf("stopped %v", v.l.stopped)
	}
}

func TestARestartStopsTheSessionsOfAnApprovedTaskOnceMore(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)
	v.tick(t)

	cfg := config.Config{ReviewRounds: 3, MaxWorkers: 2, ResendMinutes: 5, NoteFallbackAfter: 4 * time.Second}
	e := engine.New(v.st, v.l, v.ch, v.ws, cfg, v.clock)
	for range 2 {
		if err := e.Tick(); err != nil {
			t.Fatal(err)
		}
	}

	want := []string{"worker-t1", "review-t1-1", "worker-t1", "review-t1-1"}
	if !slices.Equal(v.l.stopped, want) {
		t.Fatalf("stopped %v", v.l.stopped)
	}
}

func TestAnApprovedTaskThatIsReopenedAndApprovedAgainIsStoppedAgainAfterAFailedStop(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)
	v.tick(t)
	v.feedback(t, "Fix it.")
	v.tick(t)
	mustOK(t, v.run("worker-t1", "t1", "report done", "Fixed."))
	reviewer := v.l.started[len(v.l.started)-1].Name
	v.l.stopErr = errors.New("stop failed")
	mustFail(t, v.run(reviewer, "t1", "verdict pass", "Good."), "stop failed")
	v.l.stopErr = nil
	v.l.stopped = nil

	v.tick(t)
	if len(v.l.stopped) == 0 {
		t.Fatal("the tick stopped nothing")
	}
	v.l.stopped = nil
	v.tick(t)
	if len(v.l.stopped) != 0 {
		t.Fatalf("the second tick stopped %v", v.l.stopped)
	}
}

// A stop of an approved task that keeps failing waits as a cleanup retry does, and the lead hears of it once.
func TestTheStopsOfAnApprovedTaskBackOffAndTellTheLead(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)
	v.l.stopErr = errors.New("stop failed")

	for range 33 {
		_ = v.e.Tick()
	}

	if v.l.stopCalls != 3 {
		t.Fatalf("stop calls %d", v.l.stopCalls)
	}
	want := []string{"sfy cannot clean up task t1: stop failed"}
	if n := leadNotes(t, v); !slices.Equal(n, want) {
		t.Fatalf("the lead got %q", n)
	}
}

func TestCleanupDoesNotStopASessionThatAFailedStopFoundGone(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Done, 1)
	v.l.stopErr = errors.New("no such window")
	v.l.dead = map[string]bool{"worker-t1": true, "review-t1-1": true}
	v.ws.removeErr = errors.New("permission denied")

	for range 12 {
		_ = v.e.Tick()
	}

	if v.l.stopCalls != 2 {
		t.Fatalf("stop calls %d", v.l.stopCalls)
	}
}

func TestCleanupDoesNotStopAWorkerThatTheTickFoundGone(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.l.dead = map[string]bool{"worker-t1": true}
	v.tick(t)

	mustOK(t, v.run("lead", "", "task cancel", "", "t1"))

	if v.l.stopCalls != 0 || !slices.Equal(v.ws.removed, []string{"t1"}) {
		t.Fatalf("stop calls %d, removed %v", v.l.stopCalls, v.ws.removed)
	}
}

func TestCleanupStopsAWorkerThatAnAliveCheckFoundRunningAgain(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.l.dead = map[string]bool{"worker-t1": true}
	v.tick(t)
	v.l.dead = nil
	v.tick(t)

	mustOK(t, v.run("lead", "", "task cancel", "", "t1"))

	if !slices.Equal(v.l.stopped, []string{"worker-t1"}) {
		t.Fatalf("stopped %v", v.l.stopped)
	}
}

func TestASessionThatStartsAgainIsStoppedAgain(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)
	v.l.stopErr = errors.New("no such window")
	v.l.dead = map[string]bool{"worker-t1": true, "review-t1-1": true}
	v.tick(t)
	v.l.stopErr, v.l.dead = nil, nil
	v.feedback(t, "Fix it.")
	v.tick(t)

	mustOK(t, v.run("lead", "", "task cancel", "", "t1"))

	if !slices.Contains(v.l.stopped, "worker-t1") {
		t.Fatalf("stopped %v", v.l.stopped)
	}
}

// slowStop makes each Stop take a while, as a multiplexer command does.
type slowStop struct {
	*fakeLauncher
	delay time.Duration
}

func (s slowStop) Stop(name string) error {
	time.Sleep(s.delay)
	return s.fakeLauncher.Stop(name)
}

// After the first tick ended the sessions of an approved task, a hook no longer waits for stops during a tick.
func TestHookBusyDoesNotWaitForTheSessionsOfAnApprovedTaskAfterTheFirstTick(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 4)
	const delay = 100 * time.Millisecond
	cfg := config.Config{ReviewRounds: 3, MaxWorkers: 2, ResendMinutes: 5, NoteFallbackAfter: 4 * time.Second}
	e := engine.New(v.st, slowStop{v.l, delay}, v.ch, v.ws, cfg, v.clock)
	if err := e.Tick(); err != nil {
		t.Fatal(err)
	}

	tickDone := make(chan struct{})
	go func() { _ = e.Tick(); close(tickDone) }()
	time.Sleep(delay / 2)
	began := time.Now()
	a := e.Handle(protocol.Request{Caller: protocol.Caller{Agent: "lead"}, Command: "hook busy"})
	waited := time.Since(began)
	<-tickDone

	mustOK(t, a)
	// The first tick stopped the five sessions, and the second tick stops none.
	if v.l.stopCalls != 5 {
		t.Fatalf("stop calls %d", v.l.stopCalls)
	}
	if waited >= delay {
		t.Fatalf("hook busy answered after %v", waited)
	}
}

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
