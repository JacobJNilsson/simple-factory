package workspace_test

import (
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

func TestLegacyBranchCarriesThePrefixAfterSfy(t *testing.T) {
	if got := workspace.New(t.TempDir()).LegacyBranch("t1"); got != "sfy/t1" {
		t.Errorf("default branch = %q", got)
	}
	if got := workspace.NewPrefixed(t.TempDir(), "sfy-work-").LegacyBranch("t1"); got != "sfy/sfy-work-t1" {
		t.Errorf("prefixed branch = %q", got)
	}
}

func TestCreateChecksOutTheLegacyBranchForATaskWithoutOne(t *testing.T) {
	gitEnv(t)
	repo := newRepo(t)

	dir, err := workspace.NewPrefixed(t.TempDir(), "sfy-work-").Create(task.Task{ID: "t2", Repo: repo})

	if err != nil {
		t.Fatal(err)
	}
	if got := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "sfy/sfy-work-t2" {
		t.Fatalf("branch = %s", got)
	}
}
