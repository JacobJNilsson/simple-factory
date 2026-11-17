package engine_test

import (
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// heldReview seeds a repo task with a push hold and runs its worker to a passed review.
func (v env) heldReview(t *testing.T) {
	t.Helper()
	tk := task.Task{ID: "t1", Title: "Title", Description: "Do it.", Goal: "Done.", Repo: "some/repo", Hold: true, State: task.Queued, Source: "main"}
	if err := v.st.Create(tk); err != nil {
		t.Fatal(err)
	}
	v.tick(t)
	mustOK(t, v.run("worker-t1", "t1", "report done", "Committed."))
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))
}

func TestAPassedReviewOfAHeldTaskAsksForNoPushAndTellsMain(t *testing.T) {
	v := newEnv(t)
	v.heldReview(t)

	if got := v.pushNotes(t); len(got) != 0 {
		t.Fatalf("the worker got a push request: %v", got)
	}
	if tk := v.state(t, "t1"); tk.State != task.Held || tk.Pushing {
		t.Fatalf("got %+v", tk)
	}
	got := v.inbox(t, "main")
	if len(got) != 1 || !strings.Contains(got[0].Body, "waits for the operator") || !strings.Contains(got[0].Body, "sfy task push t1") {
		t.Fatalf("main got %v", got)
	}
}

func TestTheWorkerOfAHeldTaskStartsWithoutPushPermission(t *testing.T) {
	v := newEnv(t)
	v.heldReview(t)

	if s := v.l.started[0]; !s.NoPush || !strings.Contains(s.Prompt, "holds the push") {
		t.Fatalf("started %+v", s)
	}
}

func TestTaskPushReleasesTheHoldAndStartsAWorkerThatMayPush(t *testing.T) {
	v := newEnv(t)
	v.heldReview(t)

	mustOK(t, v.run("main", "", "task push", "", "t1"))

	if tk := v.state(t, "t1"); tk.State != task.Review || !tk.Pushing || !tk.Released {
		t.Fatalf("got %+v", tk)
	}
	last := v.l.started[len(v.l.started)-1]
	if last.Name != "worker-t1" || last.NoPush || !strings.Contains(last.Prompt, "Push the branch now") {
		t.Fatalf("started %+v", last)
	}
	mustOK(t, v.run("worker-t1", "t1", "report pushed", "Pushed."))
	if tk := v.state(t, "t1"); tk.State != task.Approved {
		t.Fatalf("got %+v", tk)
	}
}

func TestTaskPushNeedsAHeldTask(t *testing.T) {
	v := newEnv(t)
	v.seedRepo(t, task.Working, 1)

	mustFail(t, v.run("main", "", "task push", "", "t1"), "needs held")
	mustFail(t, v.run("worker-t1", "t1", "task push", "", "t1"), "")
}

func TestFeedbackOnAHeldTaskKeepsTheHold(t *testing.T) {
	v := newEnv(t)
	v.heldReview(t)

	v.feedback(t, fix)

	if tk := v.state(t, "t1"); tk.State != task.Queued || !tk.Hold || tk.Released || !tk.Reopened {
		t.Fatalf("got %+v", tk)
	}
	v.tick(t)
	if last := v.l.started[len(v.l.started)-1]; !last.NoPush {
		t.Fatalf("started %+v", last)
	}
}

func TestFeedbackAfterTheReleaseRestartsTheWorkerWithoutPush(t *testing.T) {
	v := newEnv(t)
	v.heldReview(t)
	mustOK(t, v.run("main", "", "task push", "", "t1"))

	v.feedback(t, fix)

	if tk := v.state(t, "t1"); tk.State != task.Queued || tk.Released || tk.Pushing {
		t.Fatalf("got %+v", tk)
	}
	v.tick(t)
	if last := v.l.started[len(v.l.started)-1]; !last.NoPush {
		t.Fatalf("started %+v", last)
	}
}

func TestATaskWithoutThePushSectionPushesAsBefore(t *testing.T) {
	v := newEnv(t)
	v.firstReview(t)
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))

	if got := v.pushNotes(t); len(got) != 1 {
		t.Fatalf("worker got %v", v.workerBodies(t))
	}
	if v.l.started[0].NoPush {
		t.Fatalf("started %+v", v.l.started[0])
	}
}

func TestCancelEndsAHeldTask(t *testing.T) {
	v := newEnv(t)
	v.heldReview(t)

	mustOK(t, v.run("main", "", "task cancel", "", "t1"))

	if got := v.state(t, "t1").State; got != task.Cancelled {
		t.Fatalf("got %s", got)
	}
}
