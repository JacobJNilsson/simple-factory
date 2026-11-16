package engine_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestDownStopsMainAndEverySessionThatRuns(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 2)
	v.l.dead = map[string]bool{"review-t1-1": true}

	a := v.run("", "", "down", "")

	mustOK(t, a)
	want := []string{"main", "worker-t1", "review-t1-2"}
	if !slices.Equal(v.l.stopped, want) {
		t.Fatalf("stopped %v, want %v", v.l.stopped, want)
	}
	if a.Text != "Stopped main, worker-t1, review-t1-2 and the sfy process." {
		t.Fatalf("text %q", a.Text)
	}
	if !v.e.Stopping() {
		t.Fatal("the engine does not report that it stops")
	}
}

func TestDownLeavesTaskStatesAlone(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 1)

	mustOK(t, v.run("", "", "down", ""))

	if got := v.state(t, "t1"); got.State != task.Working || got.Dir != "/work/t1" || got.Round != 1 {
		t.Fatalf("task changed: %+v", got)
	}
}

func TestDownIsForTheOperatorOnly(t *testing.T) {
	for _, agent := range []string{"main", "worker-t1", "review-t1-1"} {
		v := newEnv(t)
		v.seed(t, task.Review, 1)

		mustFail(t, v.run(agent, "t1", "down", ""), "not for")

		if len(v.l.stopped) != 0 || v.e.Stopping() {
			t.Fatalf("%s stopped %v", agent, v.l.stopped)
		}
	}
}

func TestDownWithNothingRunningStillEnds(t *testing.T) {
	v := newEnv(t)
	v.l.dead = map[string]bool{"main": true}

	a := v.run("", "", "down", "")

	mustOK(t, a)
	if a.Text != "No agent session ran. Stopped the sfy process." || !v.e.Stopping() {
		t.Fatalf("text %q", a.Text)
	}
}

func TestDownFailsAndKeepsRunningWhenAStopFails(t *testing.T) {
	v := newEnv(t)
	v.l.stopErr = errors.New("stop failed")

	mustFail(t, v.run("", "", "down", ""), "stop failed")

	if v.e.Stopping() {
		t.Fatal("the engine stops although a session still runs")
	}
}

func TestTickDoesNothingAfterDown(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 1)
	mustOK(t, v.run("", "", "down", ""))
	v.l.dead = map[string]bool{"worker-t1": true}

	if err := v.e.Tick(); err != nil {
		t.Fatal(err)
	}

	if got := v.inbox(t, "main"); len(got) != 0 {
		t.Fatalf("main got %v", got)
	}
}
