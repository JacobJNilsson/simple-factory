package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// IntegrateError reports an integration that stopped. Step names the git step, and Reason holds git's message.
// The repo and the branch are unchanged after a stopped rebase, because Integrate aborts it.
type IntegrateError struct {
	Step   string
	Reason error
}

func (e *IntegrateError) Error() string {
	return "integrate: " + e.Step + " failed: " + e.Reason.Error()
}

func (e *IntegrateError) Unwrap() error { return e.Reason }

// Integrate brings the task branch into the default branch of the repo folder. It forces nothing.
// With a remote it fetches first, catches the default branch up to its remote copy, and pushes the default branch at the end.
// It rebases the task branch onto the default branch, then fast-forwards the default branch with git merge --ff-only.
// The repo folder must have the default branch checked out.
func (w *Workspaces) Integrate(t task.Task) error {
	repo := w.RepoFolder(t.Repo)
	branch := w.BranchOf(t)
	remote, err := git(repo, "remote")
	if err != nil {
		return &IntegrateError{Step: "git remote", Reason: err}
	}
	hasRemote := remote != ""
	if hasRemote {
		if _, err := git(repo, "fetch"); err != nil {
			return &IntegrateError{Step: "git fetch", Reason: err}
		}
	}
	base, err := defaultBranch(repo, hasRemote)
	if err != nil {
		return &IntegrateError{Step: "find the default branch", Reason: err}
	}
	if hasRemote {
		if _, err := git(repo, "merge", "--ff-only", "origin/"+base); err != nil {
			return &IntegrateError{Step: "git merge --ff-only origin/" + base, Reason: err}
		}
	}
	// The task branch is checked out in the task's worktree, so the rebase runs there when the folder exists.
	rebaseIn := repo
	if dir := w.dir(t.ID); isWorktree(dir) {
		rebaseIn = dir
	}
	if _, err := git(rebaseIn, "rebase", base, branch); err != nil {
		_, _ = git(rebaseIn, "rebase", "--abort")
		return &IntegrateError{Step: "git rebase " + base, Reason: err}
	}
	if _, err := git(repo, "merge", "--ff-only", branch); err != nil {
		return &IntegrateError{Step: "git merge --ff-only " + branch, Reason: err}
	}
	if hasRemote {
		if _, err := git(repo, "push", "origin", base); err != nil {
			return &IntegrateError{Step: "git push origin " + base, Reason: err}
		}
	}
	return nil
}

// isWorktree reports whether dir holds a git checkout.
func isWorktree(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// defaultBranch returns the default branch of a repo folder. With a remote it is the branch that origin/HEAD names.
// Without one, or when origin/HEAD is unset, it is the branch that the repo folder has checked out.
// The repo folder must have that branch checked out.
func defaultBranch(repo string, hasRemote bool) (string, error) {
	current, err := git(repo, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	if hasRemote {
		if head, err := git(repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
			name := strings.TrimPrefix(head, "origin/")
			if name != current {
				return "", fmt.Errorf("the repo folder has %s checked out, but the default branch is %s", current, name)
			}
		}
	}
	return current, nil
}
