package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestAStartedTaskGetsItsBranchRecordedOnce(t *testing.T) {
	v := newEnv(t)
	v.seedRepo(t, task.Queued, 0)

	v.tick(t)

	if got := v.l.started[0].Branch; got != "feat/t1-title" {
		t.Fatalf("session branch = %q", got)
	}
	if got := v.state(t, "t1").Branch; got != "feat/t1-title" {
		t.Fatalf("recorded branch = %q", got)
	}
}

func TestAReopenedTaskKeepsItsRecordedBranch(t *testing.T) {
	v := newEnv(t)
	v.firstReview(t)
	v.l.started = nil
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))
	mustOK(t, v.run("worker-t1", "t1", "report pushed", "Pushed."))
	v.feedback(t, fix)

	v.tick(t)

	if got := v.l.started[0].Branch; got != "feat/t1-title" {
		t.Fatalf("session branch = %q", got)
	}
	if v.ws.branchesMade != 1 {
		t.Fatalf("NewBranch ran %d times, want 1", v.ws.branchesMade)
	}
}

func TestATaskThatStartedBeforeBranchesKeepsItsSfyBranch(t *testing.T) {
	v := newEnv(t)
	v.seedRepo(t, task.Approved, 1)
	v.feedback(t, fix)

	v.tick(t)

	if got := v.l.started[0].Branch; got != "sfy/t1" {
		t.Fatalf("session branch = %q", got)
	}
	if v.ws.branchesMade != 0 {
		t.Fatalf("NewBranch ran %d times, want 0", v.ws.branchesMade)
	}
}

func TestATaskWithNoRepoGetsNoBranch(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)

	v.tick(t)

	if got := v.l.started[0].Branch; got != "" {
		t.Fatalf("session branch = %q", got)
	}
}

func TestAFailedBranchChoiceQueuesTheTaskAgain(t *testing.T) {
	v := newEnv(t)
	v.ws.branchErr = errors.New("remote unreachable")
	v.seedRepo(t, task.Queued, 0)

	if err := v.e.Tick(); err == nil {
		t.Fatal("Tick succeeded although no branch name was free to check")
	}

	if len(v.l.started) != 0 || v.state(t, "t1").State != task.Queued {
		t.Fatalf("started %d sessions, state %s", len(v.l.started), v.state(t, "t1").State)
	}
}

func TestTheApprovalNoteAndShowNameTheBranch(t *testing.T) {
	v := newEnv(t)
	v.firstReview(t)
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))
	mustOK(t, v.run("worker-t1", "t1", "report pushed", "Pushed."))

	notes := v.inbox(t, "lead")

	if len(notes) != 1 || !strings.Contains(notes[0].Body, "Integrate branch feat/t1-title of") {
		t.Errorf("no approval note names the branch:\n%v", notes)
	}
	if out := v.run("lead", "", "task show", "", "t1").Text; !strings.Contains(out, "Branch: feat/t1-title\n") {
		t.Errorf("task show lacks the branch:\n%s", out)
	}
}

func TestTaskShowNamesTheLegacyBranchOfAStartedTask(t *testing.T) {
	v := newEnv(t)
	v.seedRepo(t, task.Working, 1)

	if out := v.run("lead", "", "task show", "", "t1").Text; !strings.Contains(out, "Branch: sfy/t1\n") {
		t.Errorf("task show lacks the branch:\n%s", out)
	}
}
