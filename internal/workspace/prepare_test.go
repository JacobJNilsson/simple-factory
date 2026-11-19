package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

func headOf(t *testing.T, dir string) string {
	t.Helper()
	return git(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
}

func TestPrepareGivesATaskWithoutABranchTheFirstFreeName(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	git(t, repo, "branch", "feat/add-locations")

	p, err := workspace.New(t.TempDir()).Prepare(repoTask(repo))

	if err != nil {
		t.Fatal(err)
	}
	if p.Branch != "feat/add-locations-2" || headOf(t, p.Dir) != p.Branch {
		t.Fatalf("branch = %q, head = %q", p.Branch, headOf(t, p.Dir))
	}
}

func TestPrepareKeepsARecordedBranchWithoutASearch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	tk := repoTask(repo)
	tk.Branch = "fix/recorded-name"

	p, err := workspace.New(t.TempDir()).Prepare(tk)

	if err != nil || p.Branch != "fix/recorded-name" || headOf(t, p.Dir) != "fix/recorded-name" {
		t.Fatalf("Prepare() = %+v, %v", p, err)
	}
}

func TestPrepareGivesAReopenedTaskWithoutABranchItsLegacyBranchAndWork(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	ws := workspace.NewPrefixed(t.TempDir(), "sfy-work-")
	tk := task.Task{ID: "t2", Repo: repo, Branch: "sfy/sfy-work-t2"}
	first, err := ws.Prepare(tk)
	if err != nil {
		t.Fatal(err)
	}
	git(t, first.Dir, "commit", "--allow-empty", "-m", "work")
	tip := git(t, first.Dir, "rev-parse", "HEAD")
	tk.Dir = first.Dir
	if err := ws.Remove(tk); err != nil {
		t.Fatal(err)
	}
	tk.Branch, tk.Dir, tk.Reopened = "", "", true

	p, err := ws.Prepare(tk)

	if err != nil {
		t.Fatal(err)
	}
	if p.Branch != "sfy/sfy-work-t2" || headOf(t, p.Dir) != p.Branch || git(t, p.Dir, "rev-parse", "HEAD") != tip {
		t.Fatalf("Prepare() = %+v, head %s", p, headOf(t, p.Dir))
	}
}

func TestPrepareKeepsTheFolderOfAStartedTaskWithoutABranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	home := t.TempDir()
	tk := task.Task{ID: "t3", Repo: repo, Dir: "/leftover/t3"}

	p, err := workspace.New(home).Prepare(tk)

	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != "/leftover/t3" || p.Branch != "sfy/t3" {
		t.Fatalf("Prepare() = %+v", p)
	}
	if _, err := os.Stat(filepath.Join(home, "work", "t3")); !os.IsNotExist(err) {
		t.Fatalf("Prepare made a folder: %v", err)
	}
}

func TestPrepareTakesTheLeaseOfAnExistingBranchFromTheRemote(t *testing.T) {
	repo, remote := remoteRepo(t)
	tk := existingTask(repo, "chore/pr")
	tk.Branch = ""

	p, err := workspace.New(t.TempDir()).Prepare(tk)

	if err != nil {
		t.Fatal(err)
	}
	if want := git(t, remote, "rev-parse", "chore/pr"); p.Branch != "chore/pr" || p.Lease != want {
		t.Fatalf("Prepare() = %+v, want lease %s", p, want)
	}
}

func TestPrepareKeepsARecordedLease(t *testing.T) {
	repo, _ := remoteRepo(t)
	tk := existingTask(repo, "chore/pr")
	tk.Lease = "recorded"

	p, err := workspace.New(t.TempDir()).Prepare(tk)

	if err != nil || p.Lease != "recorded" {
		t.Fatalf("Prepare() = %+v, %v", p, err)
	}
}

func TestPrepareGivesATaskWithoutARepoAnEmptyFolderAndNoBranch(t *testing.T) {
	p, err := workspace.New(t.TempDir()).Prepare(task.Task{ID: "t4"})

	if err != nil || p.Branch != "" || p.Lease != "" {
		t.Fatalf("Prepare() = %+v, %v", p, err)
	}
	if entries, err := os.ReadDir(p.Dir); err != nil || len(entries) != 0 {
		t.Fatalf("folder = %v, %v", entries, err)
	}
}

func TestPrepareWrapsAFailedNameSearchAndMakesNoFolder(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	git(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing"))
	home := t.TempDir()

	p, err := workspace.New(home).Prepare(repoTask(repo))

	if err == nil {
		t.Fatal("Prepare() found a branch name with a remote that does not answer")
	}
	if p.Dir != "" {
		t.Fatalf("Dir = %q", p.Dir)
	}
	if _, err := os.Stat(filepath.Join(home, "work", "t1")); !os.IsNotExist(err) {
		t.Fatalf("folder exists: %v", err)
	}
}

func TestHoldsWorkSeesAStartedTaskWithoutABranch(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	ws := workspace.NewPrefixed(t.TempDir(), "sfy-work-")
	tk := task.Task{ID: "t5", Repo: repo, Branch: "sfy/sfy-work-t5"}
	p, err := ws.Prepare(tk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "wip.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tk.Branch, tk.Dir = "", p.Dir

	got, err := ws.HoldsWork(tk)

	if err != nil || !got {
		t.Fatalf("HoldsWork() = %v, %v", got, err)
	}
}

func TestPrepareKeepsTheFolderThatHoldsALegacyTask(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)
	ws := workspace.NewPrefixed(t.TempDir(), "sfy-work-")
	tk := task.Task{ID: "t6", Repo: repo, Branch: "sfy/sfy-work-t6"}
	p, err := ws.Prepare(tk)
	if err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(p.Dir, "unsaved.txt")
	if err := os.WriteFile(note, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tk.Branch, tk.Reopened = "", true

	again, err := ws.Prepare(tk)

	if err != nil || again.Dir != p.Dir {
		t.Fatalf("Prepare() = %+v, %v", again, err)
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("the unsaved file is gone: %v", err)
	}
}

func TestBranchOf(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		task   task.Task
		want   string
	}{
		{"recorded", "", task.Task{ID: "t1", Repo: "r", Branch: "fix/x"}, "fix/x"},
		{"recorded without a repo", "", task.Task{ID: "t1", Branch: "fix/x"}, "fix/x"},
		{"no repo", "", task.Task{ID: "t1", Dir: "/d"}, ""},
		{"not started", "", task.Task{ID: "t1", Repo: "r"}, ""},
		{"started by folder", "", task.Task{ID: "t1", Repo: "r", Dir: "/d"}, "sfy/t1"},
		{"started by reopen", "", task.Task{ID: "t1", Repo: "r", Reopened: true}, "sfy/t1"},
		{"prefixed", "sfy-work-", task.Task{ID: "t1", Repo: "r", Dir: "/d"}, "sfy/sfy-work-t1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workspace.NewPrefixed(t.TempDir(), tt.prefix).BranchOf(tt.task); got != tt.want {
				t.Errorf("BranchOf() = %q, want %q", got, tt.want)
			}
		})
	}
}
