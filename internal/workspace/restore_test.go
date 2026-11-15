package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

func TestRestoreChecksOutTheBranchWithItsWork(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	ws := workspace.New(t.TempDir())
	tk := task.Task{ID: "t12", Repo: repo}
	dir, err := ws.Create(tk)
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "commit", "--allow-empty", "-m", "work")
	tip := git(t, dir, "rev-parse", "HEAD")
	if err := ws.Remove(tk); err != nil {
		t.Fatal(err)
	}

	dir, err = ws.Restore(tk)
	if err != nil {
		t.Fatal(err)
	}

	if got := git(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("HEAD is %s, want %s", got, tip)
	}
	if got := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "sfy/t12" {
		t.Fatalf("branch %s", got)
	}
}

func TestRestoreKeepsAFolderThatStillHoldsTheTask(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	ws := workspace.New(t.TempDir())
	tk := task.Task{ID: "t13", Repo: repo}
	dir, err := ws.Create(tk)
	if err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(dir, "unsaved.txt")
	if err := os.WriteFile(note, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	again, err := ws.Restore(tk)
	if err != nil {
		t.Fatal(err)
	}

	if again != dir {
		t.Fatalf("got %s, want %s", again, dir)
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("the kept work is gone: %v", err)
	}
}

func TestRestoreGivesATaskWithoutRepoAnEmptyFolder(t *testing.T) {
	ws := workspace.New(t.TempDir())

	dir, err := ws.Restore(task.Task{ID: "t14"})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries %v, err %v", entries, err)
	}
}

func TestRestoreKeepsAPlainFolderWithFiles(t *testing.T) {
	ws := workspace.New(t.TempDir())
	tk := task.Task{ID: "t15"}
	dir, err := ws.Create(tk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "answer.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	again, err := ws.Restore(tk)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(again, "answer.md")); err != nil {
		t.Fatal(err)
	}
}
