package workspace_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/task"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

var _ engine.Workspaces = (*workspace.Workspaces)(nil)

// gitEnv isolates the test's git calls from the operator's git config.
func gitEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	if err := os.Unsetenv("GIT_DIR"); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "first")
	return dir
}

func TestCreateGivesATaskWithoutRepoAnEmptyFolder(t *testing.T) {
	gitEnv(t)
	home := t.TempDir()

	dir, err := workspace.New(home).Create(task.Task{ID: "t1"})

	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(home, "work", "t1") {
		t.Fatalf("dir = %s", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("want an empty folder, got %v, %v", entries, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestCreateGivesALocalRepoAWorktreeOnATaskBranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	home := t.TempDir()

	dir, err := workspace.New(home).Create(task.Task{ID: "t2", Repo: repo})

	if err != nil {
		t.Fatal(err)
	}
	if got := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "sfy/t2" {
		t.Fatalf("branch = %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err != nil {
		t.Fatalf("repo file missing: %v", err)
	}
}

func TestRemoveDeletesAWorktreeAndKeepsItsBranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	ws := workspace.New(t.TempDir())
	tk := task.Task{ID: "t3", Repo: repo}
	dir, err := ws.Create(tk)
	if err != nil {
		t.Fatal(err)
	}

	if err := ws.Remove(tk); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("folder still exists: %v", err)
	}
	if got := git(t, repo, "branch", "--list", "sfy/t3"); !strings.Contains(got, "sfy/t3") {
		t.Fatalf("branch is gone: %q", got)
	}
	if got := git(t, repo, "worktree", "list"); strings.Contains(got, "t3") {
		t.Fatalf("worktree still registered: %s", got)
	}
}

func TestRemoveDeletesAPlainFolder(t *testing.T) {
	gitEnv(t)
	ws := workspace.New(t.TempDir())
	dir, err := ws.Create(task.Task{ID: "t4"})
	if err != nil {
		t.Fatal(err)
	}

	if err := ws.Remove(task.Task{ID: "t4"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("folder still exists: %v", err)
	}
}

func TestCreateFailsOnANonEmptyFolderAndNamesIt(t *testing.T) {
	gitEnv(t)
	home := t.TempDir()
	dir := filepath.Join(home, "work", "t5")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := workspace.New(home).Create(task.Task{ID: "t5"})

	var notEmpty *workspace.NotEmptyError
	if !errors.As(err, &notEmpty) || notEmpty.Dir != dir {
		t.Fatalf("want a NotEmptyError naming %s, got %v", dir, err)
	}
}

func TestRemoveOfAMissingFolderIsNotAnError(t *testing.T) {
	gitEnv(t)

	if err := workspace.New(t.TempDir()).Remove(task.Task{ID: "t6"}); err != nil {
		t.Fatal(err)
	}
}

func urlRepo(t *testing.T) (home, url string, ws *workspace.Workspaces) {
	t.Helper()
	repo := newRepo(t)
	home = t.TempDir()
	return home, "file://" + repo, workspace.New(home)
}

func cloneFolders(t *testing.T, home string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "repos"))
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestCreateClonesAURLRepoOnce(t *testing.T) {
	gitEnv(t)
	home, url, ws := urlRepo(t)

	dir, err := ws.Create(task.Task{ID: "t7", Repo: url})

	if err != nil {
		t.Fatal(err)
	}
	if n := len(cloneFolders(t, home)); n != 1 {
		t.Fatalf("want one clone, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err != nil {
		t.Fatalf("repo file missing: %v", err)
	}
	if got := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "sfy/t7" {
		t.Fatalf("branch = %s", got)
	}
}

func TestCreateReusesTheCloneForASecondTask(t *testing.T) {
	gitEnv(t)
	home, url, ws := urlRepo(t)
	if _, err := ws.Create(task.Task{ID: "t7", Repo: url}); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "repos", cloneFolders(t, home)[0].Name(), "marker")
	if err := os.WriteFile(marker, []byte("m"), 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := ws.Create(task.Task{ID: "t8", Repo: url})

	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the clone was replaced: %v", err)
	}
	if n := len(cloneFolders(t, home)); n != 1 {
		t.Fatalf("want one clone, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(second, "hello.txt")); err != nil {
		t.Fatalf("repo file missing: %v", err)
	}
}

func TestCreateAfterRemoveSucceedsAgainOnTheSameBranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	ws := workspace.New(t.TempDir())
	tk := task.Task{ID: "t9", Repo: repo}
	if _, err := ws.Create(tk); err != nil {
		t.Fatal(err)
	}
	if err := ws.Remove(tk); err != nil {
		t.Fatal(err)
	}

	dir, err := ws.Create(tk)

	if err != nil {
		t.Fatal(err)
	}
	if got := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "sfy/t9" {
		t.Fatalf("branch = %s", got)
	}
}

func TestCreateMovesAnOldBranchToTheCurrentHead(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	ws := workspace.New(t.TempDir())
	tk := task.Task{ID: "t10", Repo: repo}
	if _, err := ws.Create(tk); err != nil {
		t.Fatal(err)
	}
	if err := ws.Remove(tk); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "commit", "--allow-empty", "-m", "second")

	dir, err := ws.Create(tk)

	if err != nil {
		t.Fatal(err)
	}
	if got, want := git(t, dir, "rev-parse", "HEAD"), git(t, repo, "rev-parse", "HEAD"); got != want {
		t.Fatalf("branch at %s, want %s", got, want)
	}
}

func TestCreateFailsWhenTheBranchHoldsItsOwnCommits(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	home := t.TempDir()
	ws := workspace.New(home)
	tk := task.Task{ID: "t11", Repo: repo}
	dir, err := ws.Create(tk)
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "commit", "--allow-empty", "-m", "work")
	tip := git(t, dir, "rev-parse", "HEAD")
	if err := ws.Remove(tk); err != nil {
		t.Fatal(err)
	}

	_, err = ws.Create(tk)

	var hasWork *workspace.BranchHasWorkError
	if !errors.As(err, &hasWork) || hasWork.Branch != "sfy/t11" {
		t.Fatalf("want a BranchHasWorkError, got %v", err)
	}
	if got := git(t, repo, "rev-parse", "sfy/t11"); got != tip {
		t.Fatalf("branch moved to %s, want %s", got, tip)
	}
	if _, err := os.Stat(filepath.Join(home, "work", "t11")); !os.IsNotExist(err) {
		t.Fatalf("failed Create left a folder: %v", err)
	}
}

func TestCreateFailsWhenAnotherWorktreeHoldsTheBranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	git(t, repo, "worktree", "add", "-b", "sfy/t12", other)
	ws := workspace.New(t.TempDir())

	_, err := ws.Create(task.Task{ID: "t12", Repo: repo})

	var busy *workspace.BranchBusyError
	if !errors.As(err, &busy) || busy.Branch != "sfy/t12" || !strings.HasSuffix(busy.Worktree, "other") {
		t.Fatalf("want a BranchBusyError naming the worktree, got %v", err)
	}
}

func TestCreateLeavesNoFolderOrCloneAfterAFailedClone(t *testing.T) {
	gitEnv(t)
	home := t.TempDir()

	_, err := workspace.New(home).Create(task.Task{ID: "t13", Repo: "file://" + filepath.Join(home, "missing")})

	if err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(filepath.Join(home, "work", "t13")); !os.IsNotExist(err) {
		t.Fatalf("failed Create left a folder: %v", err)
	}
	if n := len(cloneFolders(t, home)); n != 0 {
		t.Fatalf("failed clone left %d entries", n)
	}
}

func TestCreateClonesAgainWhenTheClonedFolderIsBroken(t *testing.T) {
	gitEnv(t)
	home, url, ws := urlRepo(t)
	if _, err := ws.Create(task.Task{ID: "t14", Repo: url}); err != nil {
		t.Fatal(err)
	}
	if err := ws.Remove(task.Task{ID: "t14"}); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(home, "repos", cloneFolders(t, home)[0].Name())
	if err := os.RemoveAll(filepath.Join(clone, ".git")); err != nil {
		t.Fatal(err)
	}

	dir, err := ws.Create(task.Task{ID: "t15", Repo: url})

	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err != nil {
		t.Fatalf("repo file missing: %v", err)
	}
}

func TestCreateTreatsARelativeRepoAsAURL(t *testing.T) {
	gitEnv(t)
	home := t.TempDir()

	_, err := workspace.New(home).Create(task.Task{ID: "t16", Repo: "no/such/folder"})

	if err == nil {
		t.Fatal("want a clone error")
	}
}

func TestCreateIgnoresAGitDirInTheEnvironment(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "bogus"))

	dir, err := workspace.New(t.TempDir()).Create(task.Task{ID: "t17", Repo: repo})

	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err != nil {
		t.Fatalf("repo file missing: %v", err)
	}
}

func TestCreateSucceedsAfterSomeoneDeletedTheTaskFolder(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	ws := workspace.New(t.TempDir())
	tk := task.Task{ID: "t18", Repo: repo}
	dir, err := ws.Create(tk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	if _, err := ws.Create(tk); err != nil {
		t.Fatal(err)
	}
}

func TestCreateClonesAgainWhenTheClonedFolderLiesInsideAnotherRepo(t *testing.T) {
	gitEnv(t)
	home := filepath.Join(newRepo(t), "state")
	ws := workspace.New(home)
	url := "file://" + newRepo(t)
	tk := task.Task{ID: "t19", Repo: url}
	if _, err := ws.Create(tk); err != nil {
		t.Fatal(err)
	}
	if err := ws.Remove(tk); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(home, "repos", cloneFolders(t, home)[0].Name())
	if err := os.RemoveAll(filepath.Join(clone, ".git")); err != nil {
		t.Fatal(err)
	}

	dir, err := ws.Create(task.Task{ID: "t20", Repo: url})

	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err != nil {
		t.Fatalf("repo file missing: %v", err)
	}
}

func TestCreateKeepsABrokenCloneThatATaskFolderUses(t *testing.T) {
	gitEnv(t)
	home, url, ws := urlRepo(t)
	if _, err := ws.Create(task.Task{ID: "t21", Repo: url}); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(home, "repos", cloneFolders(t, home)[0].Name())
	if err := os.RemoveAll(filepath.Join(clone, ".git", "HEAD")); err != nil {
		t.Fatal(err)
	}

	_, err := ws.Create(task.Task{ID: "t22", Repo: url})

	var inUse *workspace.CloneInUseError
	if !errors.As(err, &inUse) || inUse.Clone != clone {
		t.Fatalf("want a CloneInUseError naming %s, got %v", clone, err)
	}
	if _, err := os.Stat(clone); err != nil {
		t.Fatalf("the clone was removed: %v", err)
	}
}

// newWorktree creates a worktree for a task and returns its workspaces, task and dir.
func newWorktree(t *testing.T, id string) (*workspace.Workspaces, task.Task, string) {
	t.Helper()
	gitEnv(t)
	ws := workspace.New(t.TempDir())
	tk := task.Task{ID: id, Repo: newRepo(t)}
	dir, err := ws.Create(tk)
	if err != nil {
		t.Fatal(err)
	}
	return ws, tk, dir
}

func TestHasRemoteIsFalseForALocalRepoWithNoRemote(t *testing.T) {
	ws, tk, _ := newWorktree(t, "t1")

	got, err := ws.HasRemote(tk)

	if err != nil || got {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestHasRemoteIsTrueForALocalRepoWithARemote(t *testing.T) {
	ws, tk, dir := newWorktree(t, "t1")
	git(t, dir, "remote", "add", "origin", "file:///elsewhere")

	got, err := ws.HasRemote(tk)

	if err != nil || !got {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestHasRemoteIsTrueForAURLRepo(t *testing.T) {
	gitEnv(t)
	_, url, ws := urlRepo(t)
	tk := task.Task{ID: "t1", Repo: url}
	if _, err := ws.Create(tk); err != nil {
		t.Fatal(err)
	}

	got, err := ws.HasRemote(tk)

	if err != nil || !got {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestHoldsWorkIsFalseForACleanWorktree(t *testing.T) {
	ws, tk, _ := newWorktree(t, "t7")
	if held, err := ws.HoldsWork(tk); err != nil || held {
		t.Fatalf("held %v, err %v", held, err)
	}
}

func TestHoldsWorkSeesAModifiedFile(t *testing.T) {
	ws, tk, dir := newWorktree(t, "t7")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if held, err := ws.HoldsWork(tk); err != nil || !held {
		t.Fatalf("held %v, err %v", held, err)
	}
}

func TestHoldsWorkSeesAnUntrackedFile(t *testing.T) {
	ws, tk, dir := newWorktree(t, "t7")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if held, err := ws.HoldsWork(tk); err != nil || !held {
		t.Fatalf("held %v, err %v", held, err)
	}
}

func TestHoldsWorkIsFalseForAnEmptyPlainFolder(t *testing.T) {
	gitEnv(t)
	ws := workspace.New(t.TempDir())
	tk := task.Task{ID: "t8"}
	if _, err := ws.Create(tk); err != nil {
		t.Fatal(err)
	}
	if held, err := ws.HoldsWork(tk); err != nil || held {
		t.Fatalf("held %v, err %v", held, err)
	}
}

func TestHoldsWorkIsTrueForAPlainFolderWithAFile(t *testing.T) {
	gitEnv(t)
	ws := workspace.New(t.TempDir())
	tk := task.Task{ID: "t8"}
	dir, err := ws.Create(tk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "answer.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if held, err := ws.HoldsWork(tk); err != nil || !held {
		t.Fatalf("held %v, err %v", held, err)
	}
}
