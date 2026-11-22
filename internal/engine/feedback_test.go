package engine_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

const fix = "Handle the empty case."

func (v env) feedback(t *testing.T, text string) {
	t.Helper()
	mustOK(t, v.run("lead", "", "task feedback", "", "t1", text))
}

// workerBodies returns the unread messages of the task's worker.
func (v env) workerBodies(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, m := range v.inbox(t, "worker-t1") {
		out = append(out, m.Body)
	}
	return out
}

func TestFeedbackOnNewAndQueuedTasksKeepsTheStateAndGoesInTheFirstMessage(t *testing.T) {
	for _, state := range []task.State{task.New, task.Queued} {
		v := newEnv(t)
		v.seed(t, state, 0)

		v.feedback(t, fix)

		if tk := v.state(t, "t1"); tk.State != state || tk.Round != 0 || tk.Reopened {
			t.Fatalf("%s: got %+v", state, tk)
		}
		if state == task.New {
			continue
		}
		v.tick(t)
		prompt := v.l.started[0].Prompt
		if !strings.Contains(prompt, "# Title") || strings.Index(prompt, fix) < strings.Index(prompt, "# Title") {
			t.Errorf("first message lacks the feedback after the task file:\n%s", prompt)
		}
		if strings.Contains(prompt, "earlier work") {
			t.Errorf("a task that never ran has no earlier work:\n%s", prompt)
		}
		if tk := v.state(t, "t1"); len(tk.Pending) != 0 {
			t.Errorf("feedback stays pending after the start: %v", tk.Pending)
		}
	}
}

func TestFeedbackOnARunningWorkerGoesAsAMessageAndKeepsTheState(t *testing.T) {
	for _, state := range []task.State{task.Working, task.Feedback} {
		v := newEnv(t)
		v.seed(t, state, 1)

		v.feedback(t, fix)

		if tk := v.state(t, "t1"); tk.State != state || tk.Round != 1 {
			t.Fatalf("%s: got %+v", state, tk)
		}
		if got := v.workerBodies(t); len(got) != 1 || !strings.Contains(got[0], fix) {
			t.Fatalf("%s: worker got %v", state, got)
		}
	}
}

func TestFeedbackInReviewEndsTheReviewerAndMovesToFeedback(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	v.feedback(t, fix)

	if tk := v.state(t, "t1"); tk.State != task.Feedback || tk.Round != 1 {
		t.Fatalf("got %+v", tk)
	}
	if !slices.Contains(v.l.stopped, "review-t1-1") {
		t.Fatalf("stopped %v", v.l.stopped)
	}
	if got := v.workerBodies(t); len(got) != 1 || !strings.Contains(got[0], fix) {
		t.Fatalf("worker got %v", got)
	}
	mustFail(t, v.run("review-t1-1", "t1", "verdict pass", "Looks right."), "needs review")
}

func TestFeedbackOnAnEscalatedTaskWaitsForTheResume(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 1)
	mustOK(t, v.run("lead", "", "escalate", "", "t1", "stuck"))

	v.feedback(t, fix)

	if tk := v.state(t, "t1"); tk.State != task.Escalated || tk.Round != 1 {
		t.Fatalf("got %+v", tk)
	}
	if got := v.workerBodies(t); len(got) != 0 {
		t.Fatalf("worker got %v before the resume", got)
	}
	mustOK(t, v.run("", "", "task resume", "", "t1"))
	got := v.workerBodies(t)
	if len(got) != 1 || !strings.Contains(got[0], "is resumed") || !strings.Contains(got[0], fix) {
		t.Fatalf("worker got %v", got)
	}
}

func TestFeedbackOnAnEscalatedQueuedTaskReachesItsFirstWorker(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)
	mustOK(t, v.run("lead", "", "escalate", "", "t1", "stuck"))
	v.feedback(t, fix)
	mustOK(t, v.run("", "", "task resume", "", "t1"))

	v.tick(t)

	if len(v.l.started) != 1 || !strings.Contains(v.l.started[0].Prompt, fix) {
		t.Fatalf("started %+v", v.l.started)
	}
}

func TestFeedbackReopensAnApprovedDoneOrCancelledTask(t *testing.T) {
	for _, state := range []task.State{task.Approved, task.Done, task.Cancelled} {
		v := newEnv(t)
		v.seed(t, state, 2)
		tk := v.state(t, "t1")
		tk.Archived = state != task.Approved
		if err := v.st.Save(tk); err != nil {
			t.Fatal(err)
		}

		v.feedback(t, fix)

		got := v.state(t, "t1")
		if got.State != task.Queued || got.Round != 2 || got.Archived || !got.Reopened {
			t.Fatalf("%s: got %+v", state, got)
		}
		v.tick(t)
		if len(v.l.started) != 1 || len(v.ws.removed) != 0 || len(v.ws.restored) != 0 {
			t.Fatalf("%s: started %d, removed %v, restored %v", state, len(v.l.started), v.ws.removed, v.ws.restored)
		}
		s := v.l.started[0]
		if s.Name != "worker-t1" || s.Dir != "/work/t1" {
			t.Fatalf("%s: got %+v", state, s)
		}
		for _, want := range []string{"# Title", fix, "earlier work", "integrated"} {
			if !strings.Contains(s.Prompt, want) {
				t.Errorf("%s: first message lacks %q:\n%s", state, want, s.Prompt)
			}
		}
	}
}

func TestFeedbackOnATaskWhoseWorkspaceIsGoneRestoresIt(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Done, 1)
	tk := v.state(t, "t1")
	tk.Dir = ""
	if err := v.st.Save(tk); err != nil {
		t.Fatal(err)
	}

	v.feedback(t, fix)
	v.tick(t)

	if !slices.Equal(v.ws.restored, []string{"t1"}) {
		t.Fatalf("restored %v", v.ws.restored)
	}
	if got := v.state(t, "t1"); got.State != task.Working || got.Dir != "/work/t1" || got.Round != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestFeedbackFailedStartKeepsTheWorkspaceAndTheFeedback(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)
	v.feedback(t, fix)
	v.l.startErr = errors.New("no pane")

	if err := v.e.Tick(); err == nil {
		t.Fatal("want an error")
	}

	got := v.state(t, "t1")
	if got.State != task.Queued || got.Dir != "/work/t1" || len(v.ws.removed) != 0 || !slices.Equal(got.Pending, []string{fix}) {
		t.Fatalf("got %+v removed %v", got, v.ws.removed)
	}
}

func TestFeedbackRoundCountsFromTheNextReport(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 3)
	v.feedback(t, fix)
	v.tick(t)

	mustOK(t, v.run("worker-t1", "t1", "report done", "Fixed."))

	if tk := v.state(t, "t1"); tk.State != task.Review || tk.Round != 4 {
		t.Fatalf("got %+v", tk)
	}
	last := v.l.started[len(v.l.started)-1]
	if last.Name != "review-t1-4" || !strings.Contains(last.Prompt, fix) {
		t.Fatalf("got %+v", last)
	}
}

func TestFeedbackIsNoFailedReview(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 2)
	v.feedback(t, fix)

	if tk := v.state(t, "t1"); tk.State != task.Feedback || tk.Round != 2 {
		t.Fatalf("got %+v", tk)
	}
	// Round 2 of a limit of 3 still has a review left, so the next failed review does not escalate.
	mustOK(t, v.run("worker-t1", "t1", "report done", "Fixed."))
	mustOK(t, v.run("review-t1-3", "t1", "verdict fail", "Not yet."))
	if got := v.state(t, "t1").State; got != task.Escalated {
		t.Fatalf("round 3 fails the limit of 3, got %s", got)
	}
}

func TestFeedbackNeedsATextAndIsNotForWorkers(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)

	mustFail(t, v.run("lead", "", "task feedback", "", "t1", "  "), "needs a text")
	mustFail(t, v.run("worker-t1", "t1", "task feedback", "", "t1", "Fix it."), "not for a worker")
	mustFail(t, v.run("lead", "", "task feedback", "", "t9", "Fix it."), "no task")
}

func TestTaskShowListsTheFeedbackWithTheVerdictsInOrder(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Looks right."))
	v.feedback(t, fix)
	v.feedback(t, "Also rename it.")

	a := v.run("", "", "task show", "", "t1")

	mustOK(t, a)
	order := []string{"## Verdict, round 1: pass", "## Feedback, after round 1\n\n" + fix, "## Feedback, after round 1\n\nAlso rename it."}
	last := -1
	for _, want := range order {
		i := strings.Index(a.Text, want)
		if i < last {
			t.Fatalf("%q is missing or out of order in:\n%s", want, a.Text)
		}
		last = i
	}
}

func TestTaskEditKeepsThePendingFeedback(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)
	v.feedback(t, fix)

	mustOK(t, v.run("lead", "", "task edit", taskFile, "t1"))

	if got := v.state(t, "t1").Pending; !slices.Equal(got, []string{fix}) {
		t.Fatalf("got %v", got)
	}
}
