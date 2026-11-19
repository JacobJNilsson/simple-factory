package workspace_test

import (
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

func repoTask(repo string) task.Task {
	return task.Task{ID: "t1", Title: "Add locations", Repo: repo}
}

func TestPrepareNamesTheBranchAfterTheTypeAndSlug(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)

	p, err := workspace.New(t.TempDir()).Prepare(repoTask(repo))

	if err != nil || p.Branch != "feat/add-locations" || headOf(t, p.Dir) != p.Branch {
		t.Fatalf("Prepare() = %+v, %v", p, err)
	}
}

func TestPrepareSkipsALocalBranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	git(t, repo, "branch", "feat/add-locations")
	git(t, repo, "branch", "feat/add-locations-2")

	p, err := workspace.New(t.TempDir()).Prepare(repoTask(repo))

	if err != nil || p.Branch != "feat/add-locations-3" {
		t.Fatalf("Prepare() = %+v, %v", p, err)
	}
}

func TestPrepareSkipsARemoteBranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	remote := t.TempDir()
	git(t, remote, "init", "--bare")
	git(t, repo, "remote", "add", "origin", remote)
	git(t, repo, "push", "origin", "HEAD:refs/heads/feat/add-locations")

	p, err := workspace.New(t.TempDir()).Prepare(repoTask(repo))

	if err != nil || p.Branch != "feat/add-locations-2" {
		t.Fatalf("Prepare() = %+v, %v", p, err)
	}
}

func TestPrepareIgnoresARemoteWithoutTheBranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	remote := t.TempDir()
	git(t, remote, "init", "--bare")
	git(t, repo, "remote", "add", "origin", remote)

	p, err := workspace.New(t.TempDir()).Prepare(repoTask(repo))

	if err != nil || p.Branch != "feat/add-locations" {
		t.Fatalf("Prepare() = %+v, %v", p, err)
	}
}
