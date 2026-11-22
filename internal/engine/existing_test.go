package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

const existingFile = "# Rebase\n\n## Description\n\nRebase it.\n\n## Goal\n\nRebased.\n\n## Repo\n\nsome/repo\n\n## Branch\n\nchore/pr-branch\n"

// seedExisting stores a queued task that works on an existing remote branch.
func (v env) seedExisting(t *testing.T, hold bool) {
	t.Helper()
	tk := task.Task{ID: "t1", Title: "Rebase", Description: "Do it.", Goal: "Done.", Repo: "some/repo", Existing: "chore/pr-branch", Hold: hold, State: task.Queued, Source: "lead"}
	if err := v.st.Create(tk); err != nil {
		t.Fatal(err)
	}
}

func TestTaskNewRefusesABranchThatTheWorkspaceRefuses(t *testing.T) {
	v := newEnv(t)
	v.ws.remoteErr = errors.New("this is the default branch")

	a := v.run("lead", "", "task new", existingFile)

	mustFail(t, a, "default branch")
}

func TestTaskNewAcceptsAnExistingBranch(t *testing.T) {
	v := newEnv(t)

	mustOK(t, v.run("lead", "", "task new", existingFile))

	if got := v.state(t, "t1").Existing; got != "chore/pr-branch" {
		t.Fatalf("existing = %q", got)
	}
}

func TestTaskNewRefusesABranchWithoutARepo(t *testing.T) {
	v := newEnv(t)

	a := v.run("lead", "", "task new", "# T\n\n## Description\n\nd\n\n## Goal\n\ng\n\n## Branch\n\nfeat/x\n")

	mustFail(t, a, "Branch")
}

func TestTheWorkerOfAnExistingBranchStartsOnThatBranchWithTheRemoteSha(t *testing.T) {
	v := newEnv(t)
	v.ws.remoteSHA = "23da1c9"
	v.seedExisting(t, false)

	v.tick(t)

	s := v.l.started[0]
	if s.Branch != "chore/pr-branch" || s.Lease != "23da1c9" || v.ws.branchesMade != 0 {
		t.Fatalf("started %+v, new branches %d", s, v.ws.branchesMade)
	}
	if tk := v.state(t, "t1"); tk.Branch != "chore/pr-branch" || tk.Lease != "23da1c9" {
		t.Fatalf("got %+v", tk)
	}
}

func TestAWorkerThatCannotCheckOutTheBranchFailsToStart(t *testing.T) {
	v := newEnv(t)
	v.ws.remoteErr = errors.New("the remote has no such branch")
	v.seedExisting(t, false)

	if err := v.e.Tick(); err == nil || len(v.l.started) != 0 {
		t.Fatalf("err = %v, started %d", err, len(v.l.started))
	}
}

func TestAnEscalatedStartKeepsTheBranchAndLeaseThatPrepareReturned(t *testing.T) {
	v := newEnv(t)
	v.ws.remoteSHA = "23da1c9"
	v.ws.createErr = errors.New("disk full")
	v.seedExisting(t, false)

	for range 3 {
		_ = v.e.Tick()
	}

	if tk := v.state(t, "t1"); tk.State != task.Escalated || tk.Branch != "chore/pr-branch" || tk.Lease != "23da1c9" {
		t.Fatalf("got %+v", tk)
	}
}

func TestEditCannotChangeTheBranchOnceTheTaskHasAWorkspace(t *testing.T) {
	v := newEnv(t)
	v.seedExisting(t, false)
	v.tick(t)

	a := v.run("lead", "", "task edit", strings.Replace(existingFile, "chore/pr-branch", "chore/other", 1), "t1")

	mustFail(t, a, "branch is fixed")
}

func TestEditChecksANewBranchBeforeTheTaskHasAWorkspace(t *testing.T) {
	v := newEnv(t)
	v.seedExisting(t, false)
	v.ws.remoteErr = errors.New("the remote has no such branch")

	a := v.run("lead", "", "task edit", strings.Replace(existingFile, "chore/pr-branch", "chore/other", 1), "t1")

	mustFail(t, a, "no such branch")
}

func TestAPassedReviewOfAnExistingBranchTellsTheWorkerTheLeaseCommand(t *testing.T) {
	v := newEnv(t)
	v.ws.remoteSHA = "23da1c9"
	v.seedExisting(t, false)
	v.tick(t)
	mustOK(t, v.run("worker-t1", "t1", "report done", "Rebased."))

	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))

	got := v.inbox(t, "worker-t1")
	want := "git push --force-with-lease=chore/pr-branch:23da1c9 origin HEAD:chore/pr-branch"
	if len(got) != 1 || !strings.Contains(got[0].Body, want) {
		t.Fatalf("worker got %v", got)
	}
}

func TestPushedToAnExistingBranchTellsLeadToCompleteAndRefreshesTheLease(t *testing.T) {
	v := newEnv(t)
	v.seedExisting(t, false)
	v.tick(t)
	mustOK(t, v.run("worker-t1", "t1", "report done", "Rebased."))
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))
	v.ws.remoteSHA = "pushed1"

	mustOK(t, v.run("worker-t1", "t1", "report pushed", "Pushed."))

	got := v.inbox(t, "lead")
	if len(got) != 1 || !strings.Contains(got[0].Body, "existing branch chore/pr-branch") || !strings.Contains(got[0].Body, "sfy task complete t1") || strings.Contains(got[0].Body, "sfy task integrate") {
		t.Fatalf("the lead got %v", got)
	}
	if tk := v.state(t, "t1"); tk.State != task.Approved || tk.Lease != "pushed1" {
		t.Fatalf("got %+v", tk)
	}
}

func TestIntegrateRefusesATaskOnAnExistingBranchAndPointsToComplete(t *testing.T) {
	v := newEnv(t)
	v.seedExisting(t, false)
	v.tick(t)
	mustOK(t, v.run("worker-t1", "t1", "report done", "Rebased."))
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))
	mustOK(t, v.run("worker-t1", "t1", "report pushed", "Pushed."))

	a := v.run("lead", "", "task integrate", "", "t1")

	mustFail(t, a, "sfy task complete t1")
	if len(v.ws.integrated) != 0 || v.state(t, "t1").State != task.Approved {
		t.Errorf("integrated %v, state %s", v.ws.integrated, v.state(t, "t1").State)
	}
	mustOK(t, v.run("lead", "", "task complete", "", "t1"))
}

func TestAHeldTaskOnAnExistingBranchWaitsAndThenPushesWithTheLease(t *testing.T) {
	v := newEnv(t)
	v.ws.remoteSHA = "23da1c9"
	v.seedExisting(t, true)
	v.tick(t)
	if s := v.l.started[0]; !s.NoPush || s.Lease != "23da1c9" {
		t.Fatalf("started %+v", s)
	}
	mustOK(t, v.run("worker-t1", "t1", "report done", "Rebased."))
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))
	if tk := v.state(t, "t1"); tk.State != task.Held {
		t.Fatalf("got %+v", tk)
	}

	mustOK(t, v.run("lead", "", "task push", "", "t1"))

	last := v.l.started[len(v.l.started)-1]
	want := "git push --force-with-lease=chore/pr-branch:23da1c9 origin HEAD:chore/pr-branch"
	if last.NoPush || last.Lease != "23da1c9" || last.Branch != "chore/pr-branch" || !strings.Contains(last.Prompt, want) {
		t.Fatalf("started %+v", last)
	}
}
