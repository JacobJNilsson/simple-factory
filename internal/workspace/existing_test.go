package workspace_test

import (
	"errors"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

// remoteRepo returns a local repo whose origin holds the default branch and the branch chore/pr.
func remoteRepo(t *testing.T) (repo, remote string) {
	t.Helper()
	gitEnv(t)
	repo = newRepo(t)
	remote = t.TempDir()
	git(t, remote, "init", "--bare")
	git(t, repo, "remote", "add", "origin", remote)
	base := git(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	git(t, repo, "push", "origin", base)
	git(t, remote, "symbolic-ref", "HEAD", "refs/heads/"+base)
	git(t, repo, "checkout", "-b", "chore/pr")
	commitFile(t, repo, "pr.txt", "from the pr")
	git(t, repo, "push", "origin", "chore/pr")
	git(t, repo, "checkout", base)
	git(t, repo, "branch", "-D", "chore/pr")
	return repo, remote
}

func existingTask(repo, branch string) task.Task {
	return task.Task{ID: "t1", Title: "Rebase", Repo: repo, Existing: branch, Branch: branch}
}

func TestRemoteBranchReturnsTheShaOfTheRemoteBranch(t *testing.T) {
	repo, remote := remoteRepo(t)

	got, err := workspace.New(t.TempDir()).RemoteBranch(existingTask(repo, "chore/pr"))

	if want := git(t, remote, "rev-parse", "chore/pr"); err != nil || got != want {
		t.Fatalf("RemoteBranch() = %q, %v, want %q", got, err, want)
	}
}

func TestRemoteBranchRefusesTheDefaultBranch(t *testing.T) {
	repo, _ := remoteRepo(t)
	base := git(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	_, err := workspace.New(t.TempDir()).RemoteBranch(existingTask(repo, base))

	var refused *workspace.BranchError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoteBranchRefusesABranchTheRemoteLacks(t *testing.T) {
	repo, _ := remoteRepo(t)

	_, err := workspace.New(t.TempDir()).RemoteBranch(existingTask(repo, "chore/missing"))

	var refused *workspace.BranchError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoteBranchRefusesARepoWithoutOrigin(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)

	_, err := workspace.New(t.TempDir()).RemoteBranch(existingTask(repo, "chore/pr"))

	var refused *workspace.BranchError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateChecksOutTheExistingBranchTrackingOrigin(t *testing.T) {
	repo, remote := remoteRepo(t)

	dir, err := workspace.New(t.TempDir()).Create(existingTask(repo, "chore/pr"))

	if err != nil {
		t.Fatal(err)
	}
	if got := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "chore/pr" {
		t.Errorf("branch = %s", got)
	}
	if got := git(t, dir, "rev-parse", "--abbrev-ref", "chore/pr@{upstream}"); got != "origin/chore/pr" {
		t.Errorf("upstream = %s", got)
	}
	if got, want := git(t, dir, "rev-parse", "HEAD"), git(t, remote, "rev-parse", "chore/pr"); got != want {
		t.Errorf("HEAD = %s, want %s", got, want)
	}
}

func TestCreateMovesALeftoverLocalBranchToTheRemoteOne(t *testing.T) {
	repo, remote := remoteRepo(t)
	git(t, repo, "branch", "chore/pr", "HEAD")

	dir, err := workspace.New(t.TempDir()).Create(existingTask(repo, "chore/pr"))

	if err != nil {
		t.Fatal(err)
	}
	if got, want := git(t, dir, "rev-parse", "HEAD"), git(t, remote, "rev-parse", "chore/pr"); got != want {
		t.Errorf("HEAD = %s, want %s", got, want)
	}
}
