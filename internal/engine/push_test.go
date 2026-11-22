package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// seedRepo seeds a task that has a repo.
func (v env) seedRepo(t *testing.T, state task.State, round int) {
	t.Helper()
	tk := task.Task{ID: "t1", Title: "Title", Description: "Do it.", Goal: "Done.", Repo: "some/repo", State: state, Round: round, Source: "lead"}
	if state != task.New && state != task.Queued {
		tk.Dir = "/work/t1"
	}
	if err := v.st.Create(tk); err != nil {
		t.Fatal(err)
	}
}

// firstReview seeds a task with a repo in its first round and runs its worker to a review.
func (v env) firstReview(t *testing.T) {
	t.Helper()
	v.seedRepo(t, task.Queued, 0)
	v.tick(t)
	mustOK(t, v.run("worker-t1", "t1", "report done", "Committed."))
}

// reviewing seeds an approved task with a repo, sends feedback, and runs the worker to a review of the next round.
func (v env) reviewing(t *testing.T) {
	t.Helper()
	v.seedRepo(t, task.Approved, 1)
	v.feedback(t, fix)
	v.tick(t)
	mustOK(t, v.run("worker-t1", "t1", "report done", "Committed."))
}

func (v env) pushNotes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, b := range v.workerBodies(t) {
		if strings.Contains(b, "Push the branch now") {
			out = append(out, b)
		}
	}
	return out
}

func TestFeedbackTellsTheWorkerToCommitAndNotPush(t *testing.T) {
	v := newEnv(t)
	v.seedRepo(t, task.Approved, 1)
	v.feedback(t, fix)

	v.tick(t)

	if p := v.l.started[0].Prompt; !strings.Contains(p, "do not push") {
		t.Fatalf("first message lacks the push rule:\n%s", p)
	}
	w := newEnv(t)
	w.seedRepo(t, task.Working, 1)
	w.feedback(t, fix)
	if got := w.workerBodies(t); len(got) != 1 || !strings.Contains(got[0], "do not push") {
		t.Fatalf("worker got %v", got)
	}
}

func TestAPassedReviewAfterFeedbackKeepsTheWorkerAndTellsItToPush(t *testing.T) {
	v := newEnv(t)
	v.reviewing(t)

	mustOK(t, v.run("review-t1-2", "t1", "verdict pass", "Addressed."))

	if got := v.pushNotes(t); len(got) != 1 {
		t.Fatalf("worker got %v", v.workerBodies(t))
	}
	if slices.Contains(v.l.stopped, "worker-t1") {
		t.Fatalf("the worker ended: %v", v.l.stopped)
	}
	if tk := v.state(t, "t1"); tk.State != task.Review || !tk.Pushing || tk.Round != 2 {
		t.Fatalf("got %+v", tk)
	}
	if got := v.inbox(t, "lead"); len(got) != 0 {
		t.Fatalf("the leadheard of an approval before the push: %v", got)
	}
}

func TestTheTaskIsApprovedOnlyAfterTheWorkerReportsThePush(t *testing.T) {
	v := newEnv(t)
	v.reviewing(t)
	mustOK(t, v.run("review-t1-2", "t1", "verdict pass", "Addressed."))

	mustOK(t, v.run("worker-t1", "t1", "report pushed", "Pushed."))

	if tk := v.state(t, "t1"); tk.State != task.Approved || tk.Pushing || tk.Round != 2 {
		t.Fatalf("got %+v", tk)
	}
	got := v.inbox(t, "lead")
	if len(got) != 1 || !strings.Contains(got[0].Body, "task integrate t1") {
		t.Fatalf("the leadgot %v", got)
	}
	if !slices.Contains(v.l.stopped, "worker-t1") {
		t.Fatalf("stopped %v", v.l.stopped)
	}
}

func TestReportPushedFailsBeforeAPassedReview(t *testing.T) {
	v := newEnv(t)
	v.reviewing(t)

	mustFail(t, v.run("worker-t1", "t1", "report pushed", "Pushed."), "no push")

	if got := v.state(t, "t1").State; got != task.Review {
		t.Fatalf("got %s", got)
	}
	w := newEnv(t)
	w.seed(t, task.Working, 1)
	mustFail(t, w.run("worker-t1", "t1", "report pushed", "Pushed."), "needs review")
}

func TestAFailedReviewAfterFeedbackGoesBackToTheWorker(t *testing.T) {
	v := newEnv(t)
	v.reviewing(t)

	mustOK(t, v.run("review-t1-2", "t1", "verdict fail", "Not yet."))

	if tk := v.state(t, "t1"); tk.State != task.Feedback || tk.Pushing {
		t.Fatalf("got %+v", tk)
	}
	if got := v.pushNotes(t); len(got) != 0 {
		t.Fatalf("the worker got a push note after a failed review: %v", got)
	}
	mustOK(t, v.run("worker-t1", "t1", "report done", "Fixed."))
	mustOK(t, v.run("review-t1-3", "t1", "verdict pass", "Good."))
	if got := v.pushNotes(t); len(got) != 1 {
		t.Fatalf("worker got %v", v.workerBodies(t))
	}
}

func TestAReviewerCannotJudgeAgainWhileTheWorkerPushes(t *testing.T) {
	v := newEnv(t)
	v.reviewing(t)
	mustOK(t, v.run("review-t1-2", "t1", "verdict pass", "Addressed."))

	mustFail(t, v.run("review-t1-2", "t1", "verdict fail", "Changed my mind."), "already has its verdict")
}

func TestFeedbackWhileTheWorkerPushesEndsTheWait(t *testing.T) {
	v := newEnv(t)
	v.reviewing(t)
	mustOK(t, v.run("review-t1-2", "t1", "verdict pass", "Addressed."))

	v.feedback(t, "One more thing.")

	if tk := v.state(t, "t1"); tk.State != task.Feedback || tk.Pushing {
		t.Fatalf("got %+v", tk)
	}
}

func TestTheFirstRoundWorkerOfARepoTaskIsToldNotToPush(t *testing.T) {
	v := newEnv(t)
	v.seedRepo(t, task.Queued, 0)

	v.tick(t)

	if p := v.l.started[0].Prompt; !strings.Contains(p, "do not push") {
		t.Fatalf("first message lacks the push rule:\n%s", p)
	}
	if got := v.pushNotes(t); len(got) != 0 {
		t.Fatalf("worker got a push note before a review: %v", got)
	}
}

func TestAPassedFirstReviewTellsTheWorkerToPushAndApprovesAfterTheReport(t *testing.T) {
	v := newEnv(t)
	v.firstReview(t)

	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Looks right."))

	if got := v.pushNotes(t); len(got) != 1 {
		t.Fatalf("worker got %v", v.workerBodies(t))
	}
	if tk := v.state(t, "t1"); tk.State != task.Review || !tk.Pushing {
		t.Fatalf("got %+v", tk)
	}
	if got := v.inbox(t, "lead"); len(got) != 0 {
		t.Fatalf("the leadheard of an approval before the push: %v", got)
	}
	mustOK(t, v.run("worker-t1", "t1", "report pushed", "Pushed the branch."))
	if tk := v.state(t, "t1"); tk.State != task.Approved || tk.Pushing {
		t.Fatalf("got %+v", tk)
	}
	if got := v.inbox(t, "lead"); len(got) != 1 || !strings.Contains(got[0].Body, "task integrate t1") || !strings.Contains(got[0].Body, "worker pushed") {
		t.Fatalf("the leadgot %v", got)
	}
}

func TestAPassedReviewOnARepoWithNoRemoteApprovesWithoutAPush(t *testing.T) {
	v := newEnv(t)
	v.ws.localOnly = true
	v.firstReview(t)

	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Looks right."))

	if got := v.pushNotes(t); len(got) != 0 {
		t.Fatalf("worker got a push note: %v", got)
	}
	if tk := v.state(t, "t1"); tk.State != task.Approved || tk.Pushing {
		t.Fatalf("got %+v", tk)
	}
	got := v.inbox(t, "lead")
	if len(got) != 1 || !strings.Contains(got[0].Body, "no remote") || !strings.Contains(got[0].Body, "task integrate t1") {
		t.Fatalf("the leadgot %v", got)
	}
	if !slices.Contains(v.l.stopped, "worker-t1") {
		t.Fatalf("the worker kept running: %v", v.l.stopped)
	}
}

func TestAFailedFirstReviewGoesBackToTheWorker(t *testing.T) {
	v := newEnv(t)
	v.firstReview(t)

	mustOK(t, v.run("review-t1-1", "t1", "verdict fail", "Not yet."))

	if tk := v.state(t, "t1"); tk.State != task.Feedback || tk.Pushing {
		t.Fatalf("got %+v", tk)
	}
	if got := v.pushNotes(t); len(got) != 0 {
		t.Fatalf("the worker got a push note after a failed review: %v", got)
	}
}

func TestATaskWithoutARepoIsApprovedOnAPassedReview(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Looks right."))

	if tk := v.state(t, "t1"); tk.State != task.Approved || tk.Pushing {
		t.Fatalf("got %+v", tk)
	}
	if got := v.pushNotes(t); len(got) != 0 {
		t.Fatalf("worker got %v", got)
	}
}

func TestAWorkerOfATaskWithoutARepoIsNotToldNotToPush(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)
	v.feedback(t, fix)

	v.tick(t)

	if p := v.l.started[0].Prompt; strings.Contains(p, "do not push") {
		t.Fatalf("worker told not to push:\n%s", p)
	}
}

func TestAWorkerThatWaitsToPushMayReportBlocked(t *testing.T) {
	v := newEnv(t)
	v.reviewing(t)
	mustFail(t, v.run("worker-t1", "t1", "report blocked", "Stuck."), "needs working or feedback")
	mustOK(t, v.run("review-t1-2", "t1", "verdict pass", "Addressed."))

	mustOK(t, v.run("worker-t1", "t1", "report blocked", "No access."))
}
