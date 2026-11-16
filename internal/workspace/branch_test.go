package workspace_test

import (
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

func repoTask(repo string) task.Task {
	return task.Task{ID: "t1", Title: "Add locations", Repo: repo}
}

func TestNewBranchUsesTheTypeAndSlug(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)

	got, err := workspace.New(t.TempDir()).NewBranch(repoTask(repo))

	if err != nil || got != "feat/add-locations" {
		t.Fatalf("NewBranch() = %q, %v", got, err)
	}
}

func TestNewBranchSkipsALocalBranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	git(t, repo, "branch", "feat/add-locations")
	git(t, repo, "branch", "feat/add-locations-2")

	got, err := workspace.New(t.TempDir()).NewBranch(repoTask(repo))

	if err != nil || got != "feat/add-locations-3" {
		t.Fatalf("NewBranch() = %q, %v", got, err)
	}
}

func TestNewBranchSkipsARemoteBranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	remote := t.TempDir()
	git(t, remote, "init", "--bare")
	git(t, repo, "remote", "add", "origin", remote)
	git(t, repo, "push", "origin", "HEAD:refs/heads/feat/add-locations")

	got, err := workspace.New(t.TempDir()).NewBranch(repoTask(repo))

	if err != nil || got != "feat/add-locations-2" {
		t.Fatalf("NewBranch() = %q, %v", got, err)
	}
}

func TestNewBranchIgnoresARemoteWithoutTheBranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	remote := t.TempDir()
	git(t, remote, "init", "--bare")
	git(t, repo, "remote", "add", "origin", remote)

	got, err := workspace.New(t.TempDir()).NewBranch(repoTask(repo))

	if err != nil || got != "feat/add-locations" {
		t.Fatalf("NewBranch() = %q, %v", got, err)
	}
}

func TestCreateChecksOutTheRecordedBranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	tk := repoTask(repo)
	tk.Branch = "fix/recorded-name"

	dir, err := workspace.New(t.TempDir()).Create(tk)

	if err != nil {
		t.Fatal(err)
	}
	if got := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "fix/recorded-name" {
		t.Fatalf("branch = %s", got)
	}
}
