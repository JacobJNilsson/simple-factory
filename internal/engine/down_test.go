package engine_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// sfy down stops the sfy process and each worker and reviewer that runs. It never stops the lead and keeps the lead's registration.

func TestDownStopsEveryWorkerAndReviewerThatRunsAndNeverTheLead(t *testing.T) {
	for _, tc := range []struct {
		name       string
		registered bool
	}{
		{name: "lead that sfy started", registered: false},
		{name: "registered lead", registered: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newEnv(t)
			v.seed(t, task.Review, 2)
			v.l.dead = map[string]bool{"review-t1-1": true}
			reg := &fakeRegistration{registered: tc.registered}
			v.e.SetRegistration(reg)

			a := v.run("", "", "down", "")

			mustOK(t, a)
			if want := []string{"worker-t1", "review-t1-2"}; !slices.Equal(v.l.stopped, want) {
				t.Fatalf("stopped %v, want %v", v.l.stopped, want)
			}
			if reg.registered != tc.registered {
				t.Errorf("registered %v, want %v", reg.registered, tc.registered)
			}
			want := "Stopped worker-t1, review-t1-2 and the sfy process. Left the lead running. sfy down stops only the sfy process, the workers and the reviewers."
			if a.Text != want {
				t.Fatalf("text %q", a.Text)
			}
			if !v.e.Stopping() {
				t.Fatal("the engine does not report that it stops")
			}
		})
	}
}

func TestDownWithOnlyTheLeadRunningStopsNoSession(t *testing.T) {
	v := newEnv(t)

	a := v.run("", "", "down", "")

	mustOK(t, a)
	if len(v.l.stopped) != 0 {
		t.Fatalf("stopped %v", v.l.stopped)
	}
	want := "Stopped the sfy process. Left the lead running. sfy down stops only the sfy process, the workers and the reviewers."
	if a.Text != want {
		t.Fatalf("text %q", a.Text)
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
	for _, agent := range []string{"lead", "worker-t1", "review-t1-1"} {
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
	v.l.dead = map[string]bool{"lead": true}

	a := v.run("", "", "down", "")

	mustOK(t, a)
	if a.Text != "No agent session ran. Stopped the sfy process." || !v.e.Stopping() {
		t.Fatalf("text %q", a.Text)
	}
}

func TestDownFailsAndKeepsRunningWhenAStopFails(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 1)
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

	if got := v.inbox(t, "lead"); len(got) != 0 {
		t.Fatalf("the lead got %v", got)
	}
}
