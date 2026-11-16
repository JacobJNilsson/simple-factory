package workspace_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

// commitFile writes a file in dir and commits it.
func commitFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "change "+name)
}

func TestIntegrateRebasesAndFastForwardsTheDefaultBranch(t *testing.T) {
	ws, tk, dir := newWorktree(t, "t1")
	repo := tk.Repo
	commitFile(t, dir, "task.txt", "from the task")
	commitFile(t, repo, "other.txt", "from main")

	if err := ws.Integrate(tk); err != nil {
		t.Fatal(err)
	}

	if got, want := git(t, repo, "rev-parse", "HEAD"), git(t, dir, "rev-parse", "HEAD"); got != want {
		t.Errorf("default branch %s, task branch %s", got, want)
	}
	if got := git(t, repo, "rev-list", "--merges", "--count", "HEAD"); got != "0" {
		t.Errorf("history has %s merge commits", got)
	}
	if got := git(t, repo, "rev-list", "--count", "HEAD"); got != "3" {
		t.Errorf("history has %s commits, want 3", got)
	}
}

func TestIntegrateStopsOnAConflictAndLeavesBothBranchesAsTheyWere(t *testing.T) {
	ws, tk, dir := newWorktree(t, "t1")
	repo := tk.Repo
	commitFile(t, dir, "hello.txt", "task version")
	commitFile(t, repo, "hello.txt", "main version")
	mainHead := git(t, repo, "rev-parse", "HEAD")
	taskHead := git(t, dir, "rev-parse", "HEAD")

	err := ws.Integrate(tk)

	var stopped *workspace.IntegrateError
	if !errors.As(err, &stopped) {
		t.Fatalf("err = %v", err)
	}
	if got := git(t, repo, "rev-parse", "HEAD"); got != mainHead {
		t.Errorf("default branch moved to %s", got)
	}
	if got := git(t, dir, "rev-parse", "HEAD"); got != taskHead {
		t.Errorf("task branch moved to %s", got)
	}
	if got := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "sfy/t1" {
		t.Errorf("worktree is on %s, so a rebase still runs", got)
	}
}

func TestIntegratePushesTheDefaultBranchWhenTheRepoHasARemote(t *testing.T) {
	ws, tk, dir := newWorktree(t, "t1")
	repo := tk.Repo
	remote := t.TempDir()
	git(t, remote, "init", "--bare")
	git(t, repo, "remote", "add", "origin", remote)
	branch := git(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	git(t, repo, "push", "origin", branch)
	commitFile(t, dir, "task.txt", "from the task")

	if err := ws.Integrate(tk); err != nil {
		t.Fatal(err)
	}

	if got, want := git(t, remote, "rev-parse", branch), git(t, repo, "rev-parse", "HEAD"); got != want {
		t.Errorf("remote has %s, want %s", got, want)
	}
}

func TestIntegrateStopsWhenTheRepoFolderHasAnotherBranchCheckedOut(t *testing.T) {
	ws, tk, dir := newWorktree(t, "t1")
	repo := tk.Repo
	remote := t.TempDir()
	git(t, remote, "init", "--bare")
	git(t, repo, "remote", "add", "origin", remote)
	base := git(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	git(t, repo, "push", "origin", base)
	git(t, repo, "remote", "set-head", "origin", base)
	git(t, repo, "checkout", "-b", "side")
	commitFile(t, dir, "task.txt", "from the task")

	err := ws.Integrate(task.Task{ID: "t1", Repo: repo})

	var stopped *workspace.IntegrateError
	if !errors.As(err, &stopped) {
		t.Fatalf("err = %v", err)
	}
}
