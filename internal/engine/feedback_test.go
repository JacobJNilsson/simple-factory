package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestFeedbackQueuesAnApprovedTaskAndKeepsTheRound(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 2)

	mustOK(t, v.run("main", "", "task feedback", "", "t1", "Handle", "the", "empty", "case."))

	tk := v.state(t, "t1")
	if tk.State != task.Queued || tk.Round != 2 || tk.Dir != "/work/t1" {
		t.Fatalf("got %+v", tk)
	}
}

func TestFeedbackWorkerStartsInTheKeptWorkspaceWithTheFeedback(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)
	mustOK(t, v.run("", "", "task feedback", "", "t1", "Handle the empty case."))

	v.tick(t)

	if len(v.l.started) != 1 || len(v.ws.removed) != 0 {
		t.Fatalf("started %v removed %v", v.l.started, v.ws.removed)
	}
	s := v.l.started[0]
	if s.Name != "worker-t1" || s.Dir != "/work/t1" {
		t.Fatalf("got %+v", s)
	}
	for _, want := range []string{"# Title", "Handle the empty case.", "earlier work", "open pull request"} {
		if !strings.Contains(s.Prompt, want) {
			t.Errorf("first message lacks %q:\n%s", want, s.Prompt)
		}
	}
	if tk := v.state(t, "t1"); tk.State != task.Working || tk.Round != 1 {
		t.Fatalf("got %+v", tk)
	}
}

func TestFeedbackFailedStartKeepsTheWorkspace(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)
	mustOK(t, v.run("main", "", "task feedback", "", "t1", "Fix it."))
	v.l.startErr = errors.New("no pane")

	if err := v.e.Tick(); err == nil {
		t.Fatal("want an error")
	}

	if tk := v.state(t, "t1"); tk.State != task.Queued || tk.Dir != "/work/t1" || len(v.ws.removed) != 0 {
		t.Fatalf("got %+v removed %v", tk, v.ws.removed)
	}
}

func TestFeedbackReviewerGetsTheFeedbackAndTheRoundGoesUpByOne(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)
	mustOK(t, v.run("main", "", "task feedback", "", "t1", "Handle the empty case."))
	v.tick(t)

	mustOK(t, v.run("worker-t1", "t1", "report done", "Fixed."))

	if tk := v.state(t, "t1"); tk.State != task.Review || tk.Round != 2 {
		t.Fatalf("got %+v", tk)
	}
	last := v.l.started[len(v.l.started)-1]
	if last.Name != "review-t1-2" || !strings.Contains(last.Prompt, "Handle the empty case.") {
		t.Fatalf("got %+v", last)
	}
}

func TestFeedbackFailsInEveryOtherState(t *testing.T) {
	for _, state := range []task.State{task.New, task.Queued, task.Working, task.Review, task.Feedback, task.Done, task.Escalated, task.Cancelled} {
		v := newEnv(t)
		v.seed(t, state, 1)

		mustFail(t, v.run("main", "", "task feedback", "", "t1", "Fix it."), "needs approved")

		if got := v.state(t, "t1").State; got != state {
			t.Errorf("%s: task moved to %s", state, got)
		}
	}
}

func TestFeedbackFailsForATaskWithNoWorkspace(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)
	tk := v.state(t, "t1")
	tk.Dir = ""
	if err := v.st.Save(tk); err != nil {
		t.Fatal(err)
	}

	mustFail(t, v.run("main", "", "task feedback", "", "t1", "Fix it."), "no workspace")

	if got := v.state(t, "t1").State; got != task.Approved {
		t.Fatalf("task moved to %s", got)
	}
}

func TestFeedbackNeedsATextAndIsNotForWorkers(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)

	mustFail(t, v.run("main", "", "task feedback", "", "t1", "  "), "needs a text")
	mustFail(t, v.run("worker-t1", "t1", "task feedback", "", "t1", "Fix it."), "not for a worker")
}

func TestFeedbackIsNoFailedReview(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 3)
	mustOK(t, v.run("main", "", "task feedback", "", "t1", "Fix it."))
	v.tick(t)
	mustOK(t, v.run("worker-t1", "t1", "report done", "Fixed."))

	if tk := v.state(t, "t1"); tk.Round != 4 {
		t.Fatalf("got round %d", tk.Round)
	}
	mustOK(t, v.run("review-t1-4", "t1", "verdict fail", "Still wrong."))

	// Round 4 passes the limit of 3, so the failed review escalates. The feedback itself changed nothing.
	if got := v.state(t, "t1").State; got != task.Escalated {
		t.Fatalf("got %s", got)
	}
}

func TestTaskShowListsTheFeedbackWithTheVerdicts(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Looks right."))
	mustOK(t, v.run("main", "", "task feedback", "", "t1", "Handle the empty case."))

	a := v.run("", "", "task show", "", "t1")

	mustOK(t, a)
	pass := strings.Index(a.Text, "## Verdict, round 1: pass")
	feedback := strings.Index(a.Text, "## Feedback, after round 1\n\nHandle the empty case.")
	if pass < 0 || feedback < pass {
		t.Fatalf("got:\n%s", a.Text)
	}
}
