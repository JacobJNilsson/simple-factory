// Package workspace gives each task a folder: a git worktree when the task names a repo, an empty folder otherwise.
package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

const gitTimeout = 5 * time.Minute

// Workspaces creates and removes task folders under one state folder.
type Workspaces struct {
	home string
}

// New returns Workspaces that keep task folders in home/work and clones in home/repos.
func New(home string) *Workspaces {
	return &Workspaces{home: home}
}

func (w *Workspaces) dir(id string) string {
	return filepath.Join(w.home, "work", id)
}

// Create makes the folder of a task and returns its path. A failed Create leaves no folder behind.
// The engine holds its lock during Create, so a slow clone blocks commands for up to the git timeout.
func (w *Workspaces) Create(t task.Task) (dir string, err error) {
	dir = w.dir(t.ID)
	if err := requireEmpty(dir); err != nil {
		return "", err
	}
	if t.Repo == "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("create folder %s: %w", dir, err)
		}
		return dir, nil
	}
	repo, base, err := w.source(t.Repo)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", fmt.Errorf("create folder %s: %w", filepath.Dir(dir), err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("create folder %s: %w", dir, err)
	}
	if err := addWorktree(repo, dir, "sfy/"+t.ID, base); err != nil {
		if rmErr := os.Remove(dir); rmErr != nil && !os.IsNotExist(rmErr) {
			return "", fmt.Errorf("%w (and the folder stays: %v)", err, rmErr)
		}
		return "", err
	}
	return dir, nil
}

// addWorktree checks out branch in dir. A leftover branch that holds no work of its own moves to base.
func addWorktree(repo, dir, branch, base string) error {
	// A task folder that someone deleted by hand must stop holding its branch.
	if _, err := git(repo, "worktree", "prune"); err != nil {
		return err
	}
	baseCommit, err := git(repo, "rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		return err
	}
	if _, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		_, err := git(repo, "worktree", "add", "--no-track", "-b", branch, dir, baseCommit)
		return err
	}
	holder, err := worktreeHolding(repo, branch)
	if err != nil {
		return err
	}
	if holder != "" {
		return &BranchBusyError{Branch: branch, Worktree: holder}
	}
	ahead, err := git(repo, "rev-list", "--count", baseCommit+".."+branch)
	if err != nil {
		return err
	}
	if ahead != "0" {
		return &BranchHasWorkError{Branch: branch}
	}
	if _, err := git(repo, "branch", "-f", branch, baseCommit); err != nil {
		return err
	}
	_, err = git(repo, "worktree", "add", dir, branch)
	return err
}

// worktreeHolding returns the path of the worktree that has branch checked out, or "".
func worktreeHolding(repo, branch string) (string, error) {
	out, err := git(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	var path string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case line == "branch refs/heads/"+branch:
			return path, nil
		}
	}
	return "", nil
}

// NotEmptyError reports a task folder that already holds files.
type NotEmptyError struct {
	Dir string
}

func (e *NotEmptyError) Error() string {
	return "folder " + e.Dir + " already exists and is not empty"
}

// BranchBusyError reports a task branch that another worktree has checked out.
type BranchBusyError struct {
	Branch, Worktree string
}

func (e *BranchBusyError) Error() string {
	return "branch " + e.Branch + " is checked out in worktree " + e.Worktree
}

// BranchHasWorkError reports a task branch with commits that the base lacks.
type BranchHasWorkError struct {
	Branch string
}

func (e *BranchHasWorkError) Error() string {
	return "branch " + e.Branch + " holds work that sfy will not overwrite"
}

// CloneInUseError reports a broken clone that a task folder still uses, so sfy keeps it.
type CloneInUseError struct {
	Clone string
}

func (e *CloneInUseError) Error() string {
	return "clone " + e.Clone + " is broken but a task folder uses it"
}

// requireEmpty fails when dir exists and holds anything.
func requireEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read folder %s: %w", dir, err)
	}
	if len(entries) > 0 {
		return &NotEmptyError{Dir: dir}
	}
	return nil
}

// source returns the repository to add a worktree to and the base to start from.
// A local repo's base is its current HEAD. A clone's base is the default branch after a fetch.
func (w *Workspaces) source(repo string) (dir, base string, err error) {
	if local, ok := localRepo(repo); ok {
		return local, "HEAD", nil
	}
	clone := filepath.Join(w.home, "repos", cloneName(repo))
	if isClone(clone) {
		if _, err := git(clone, "fetch", "origin"); err != nil {
			return "", "", err
		}
		return clone, "origin/HEAD", nil
	}
	if _, err := os.Lstat(clone); err == nil {
		if w.usesClone(clone) {
			return "", "", &CloneInUseError{Clone: clone}
		}
	}
	if err := os.RemoveAll(clone); err != nil {
		return "", "", fmt.Errorf("remove broken clone %s: %w", clone, err)
	}
	return clone, "origin/HEAD", cloneInto(repo, clone)
}

// isClone reports whether dir is the top folder of a git repository of its own.
func isClone(dir string) bool {
	top, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	want, err1 := filepath.EvalSymlinks(dir)
	got, err2 := filepath.EvalSymlinks(top)
	return err1 == nil && err2 == nil && want == got
}

// usesClone reports whether a task folder under home/work has its git data inside clone.
func (w *Workspaces) usesClone(clone string) bool {
	if resolved, err := filepath.EvalSymlinks(clone); err == nil {
		clone = resolved
	}
	entries, _ := os.ReadDir(filepath.Join(w.home, "work"))
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(w.home, "work", e.Name(), ".git"))
		if err != nil {
			continue
		}
		gitdir := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir:"))
		if strings.HasPrefix(gitdir, clone+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// cloneInto clones under a temporary name and renames it, so a killed clone never looks like a clone.
func cloneInto(repo, clone string) error {
	parent := filepath.Dir(clone)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create folder for clones: %w", err)
	}
	tmp, err := os.MkdirTemp(parent, ".clone-")
	if err != nil {
		return fmt.Errorf("create folder for clone: %w", err)
	}
	if _, err := git("", "clone", repo, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, clone); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("move clone into place: %w", err)
	}
	return nil
}

// localRepo reports whether repo is an absolute path, after ~/ expansion, to an existing folder.
func localRepo(repo string) (string, bool) {
	if rest, ok := strings.CutPrefix(repo, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		repo = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(repo) {
		return "", false
	}
	info, err := os.Stat(repo)
	return repo, err == nil && info.IsDir()
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// cloneName derives a folder name from a repo URL. The hash keeps two URLs with the same last part apart.
func cloneName(repo string) string {
	last := repo[strings.LastIndexAny(repo, "/:")+1:]
	last = strings.TrimSuffix(last, ".git")
	last = strings.Trim(unsafeName.ReplaceAllString(last, "-"), "-.")
	if last == "" {
		last = "repo"
	}
	sum := sha256.Sum256([]byte(repo))
	return last + "-" + hex.EncodeToString(sum[:])[:8]
}

// Remove deletes the folder of a task. A worktree's branch stays.
func (w *Workspaces) Remove(t task.Task) error {
	dir := w.dir(t.ID)
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove folder %s: %w", dir, err)
		}
		return nil
	}
	common, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	if _, err := git("", "--git-dir="+common, "worktree", "remove", "--force", dir); err != nil {
		return err
	}
	_, err = git("", "--git-dir="+common, "worktree", "prune")
	return err
}

// git runs git in dir and returns its trimmed output. A failure carries git's stderr.
func git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// WaitDelay stops a git child that ignores the kill from holding the call open.
	cmd.WaitDelay = 10 * time.Second
	cmd.Env = cleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// cleanEnv drops the variables that point git at another repository.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_PREFIX":
		default:
			env = append(env, kv)
		}
	}
	return append(env, "GIT_TERMINAL_PROMPT=0")
}
