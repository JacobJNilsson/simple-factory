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
	"slices"
	"strings"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

const gitTimeout = 5 * time.Minute

// Workspaces creates and removes task folders under one state folder.
type Workspaces struct {
	home   string
	prefix string
}

// New returns Workspaces that keep task folders in home/work and clones in home/repos.
func New(home string) *Workspaces {
	return NewPrefixed(home, "")
}

// NewPrefixed returns Workspaces whose legacy task branches carry prefix after sfy/, as in sfy/<prefix>t1.
func NewPrefixed(home, prefix string) *Workspaces {
	return &Workspaces{home: home, prefix: prefix}
}

func (w *Workspaces) dir(id string) string {
	return filepath.Join(w.home, "work", id)
}

// Prepare fixes the branch and lease of a task and makes its folder. A task with a set Dir keeps that folder.
// A task with a repo gets its branch once: the existing branch, the recorded one, the legacy name of a started task, or a new free name.
// A failed Prepare leaves no folder behind. It runs git and may fetch or clone, and the engine holds its lock meanwhile, so it can block commands for up to the git timeout.
// A reopened task keeps a folder that still holds its work, and otherwise gets its branch with its earlier commits.
// Any other task is refused a branch that holds work.
// It returns the branch and lease it fixed even on an error, so a caller can record them.
func (w *Workspaces) Prepare(t task.Task) (task.Prepared, error) {
	p := task.Prepared{Dir: t.Dir, Branch: t.Branch, Lease: t.Lease}
	if t.Repo != "" {
		if err := w.chooseBranch(t, &p); err != nil {
			return p, fmt.Errorf("choose branch: %w", err)
		}
	}
	if p.Dir != "" {
		return p, nil
	}
	t.Branch, t.Lease = p.Branch, p.Lease
	dir, err := w.create(t, t.Reopened)
	if err != nil {
		return p, fmt.Errorf("create workspace: %w", err)
	}
	p.Dir = dir
	return p, nil
}

// chooseBranch fixes the branch of a task with a repo in p.
func (w *Workspaces) chooseBranch(t task.Task, p *task.Prepared) error {
	if t.Existing != "" {
		p.Branch = t.Existing
		if p.Lease != "" {
			return nil
		}
		// The checkout records the remote sha, which later pushes must still find on the remote.
		sha, err := w.RemoteBranch(t)
		p.Lease = sha
		return err
	}
	if p.Branch = w.BranchOf(t); p.Branch != "" {
		return nil
	}
	branch, err := w.newBranch(t)
	p.Branch = branch
	return err
}

// BranchOf returns the task's branch. A task with a repo that started before tasks carried a branch keeps its old name.
// It returns "" for a task with no repo and for a task that has not started and has no branch yet.
func (w *Workspaces) BranchOf(t task.Task) string {
	if t.Branch == "" && t.Repo != "" && (t.Dir != "" || t.Reopened) {
		return w.legacyBranch(t.ID)
	}
	return t.Branch
}

// create makes the folder of a task on t.Branch. With restore, a branch that holds work is checked out as it is.
func (w *Workspaces) create(t task.Task, restore bool) (dir string, err error) {
	dir = w.dir(t.ID)
	if restore && w.holdsTask(t) {
		return dir, nil
	}
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
	add := func() error { return addWorktree(repo, dir, t.Branch, base, restore) }
	if t.Existing != "" {
		add = func() error { return addExistingWorktree(repo, dir, t.Existing, restore) }
	}
	if err := add(); err != nil {
		if rmErr := os.Remove(dir); rmErr != nil && !os.IsNotExist(rmErr) {
			return "", fmt.Errorf("%w (and the folder stays: %v)", err, rmErr)
		}
		return "", err
	}
	return dir, nil
}

// legacyBranch returns the branch name from before tasks carried their own: sfy/<prefix><id>.
func (w *Workspaces) legacyBranch(id string) string { return "sfy/" + w.prefix + id }

// newBranch returns the first free name of the form <type>/<slug>, <type>/<slug>-2, <type>/<slug>-3 and so on.
// A name is taken when the repo has a local branch of that name or any remote has one.
// It fetches no branch and creates none.
func (w *Workspaces) newBranch(t task.Task) (string, error) {
	repo, _, err := w.source(t.Repo)
	if err != nil {
		return "", err
	}
	remotes, err := git(repo, "remote")
	if err != nil {
		return "", err
	}
	base := t.BranchBase()
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		taken, err := branchTaken(repo, strings.Fields(remotes), name)
		if err != nil || !taken {
			return name, err
		}
	}
}

// branchTaken reports whether the repo or one of its remotes has a branch with this name.
func branchTaken(repo string, remotes []string, name string) (bool, error) {
	if _, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
		return true, nil
	}
	for _, remote := range remotes {
		out, err := git(repo, "ls-remote", "--heads", remote, "refs/heads/"+name)
		if err != nil {
			return false, err
		}
		if out != "" {
			return true, nil
		}
	}
	return false, nil
}

// holdsTask reports whether the task's folder still holds its work: its worktree on the task branch, or a plain folder with files.
func (w *Workspaces) holdsTask(t task.Task) bool {
	dir := w.dir(t.ID)
	if t.Repo == "" {
		entries, err := os.ReadDir(dir)
		return err == nil && len(entries) > 0
	}
	head, err := git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	return err == nil && head == w.BranchOf(t)
}

// addWorktree checks out branch in dir. A leftover branch that holds no work of its own moves to base.
// With keep, the branch stays as it is, whatever work it holds.
func addWorktree(repo, dir, branch, base string, keep bool) error {
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
	if !keep {
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
	}
	_, err = git(repo, "worktree", "add", dir, branch)
	return err
}

// addExistingWorktree checks out branch of origin in dir, as a local branch that tracks origin/<branch>.
// A leftover local branch that holds no work beyond origin/<branch> moves to it. With keep, a leftover branch stays as it is.
func addExistingWorktree(repo, dir, branch string, keep bool) error {
	if _, err := git(repo, "worktree", "prune"); err != nil {
		return err
	}
	remoteRef := "refs/remotes/origin/" + branch
	if _, err := git(repo, "fetch", "origin", "+refs/heads/"+branch+":"+remoteRef); err != nil {
		return err
	}
	if _, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		_, err := git(repo, "worktree", "add", "--track", "-b", branch, dir, "origin/"+branch)
		return err
	}
	holder, err := worktreeHolding(repo, branch)
	if err != nil {
		return err
	}
	if holder != "" {
		return &BranchBusyError{Branch: branch, Worktree: holder}
	}
	if !keep {
		ahead, err := git(repo, "rev-list", "--count", "origin/"+branch+".."+branch)
		if err != nil {
			return err
		}
		if ahead != "0" {
			return &BranchHasWorkError{Branch: branch}
		}
		if _, err := git(repo, "branch", "-f", branch, "origin/"+branch); err != nil {
			return err
		}
	}
	if _, err := git(repo, "branch", "--set-upstream-to=origin/"+branch, branch); err != nil {
		return err
	}
	_, err = git(repo, "worktree", "add", dir, branch)
	return err
}

// BranchError reports a Branch section that sfy refuses.
type BranchError struct {
	Branch, Reason string
}

func (e *BranchError) Error() string {
	return "## Branch " + e.Branch + ": " + e.Reason
}

// RemoteBranch checks the task's Branch section and returns the sha that the branch has on origin.
// It refuses a repo with no origin, a branch that origin lacks, and the default branch.
// The default branch is the one that origin's HEAD names, or the branch that a local repo folder has checked out.
func (w *Workspaces) RemoteBranch(t task.Task) (string, error) {
	repo, _, err := w.source(t.Repo)
	if err != nil {
		return "", err
	}
	branch := t.Existing
	if remotes, err := git(repo, "remote"); err != nil {
		return "", err
	} else if !slices.Contains(strings.Fields(remotes), "origin") {
		return "", &BranchError{Branch: branch, Reason: "the repo has no remote named origin"}
	}
	out, err := git(repo, "ls-remote", "--symref", "origin", "HEAD", "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	sha, def := parseLsRemote(out, branch)
	if sha == "" {
		return "", &BranchError{Branch: branch, Reason: "the remote has no such branch"}
	}
	if local, ok := localRepo(t.Repo); ok {
		if current, err := git(local, "symbolic-ref", "--short", "HEAD"); err == nil && current == branch {
			def = branch
		}
	}
	if def == branch {
		return "", &BranchError{Branch: branch, Reason: "this is the default branch, which a task never pushes to"}
	}
	return sha, nil
}

// parseLsRemote reads the output of git ls-remote --symref for HEAD and one branch.
// It returns the sha of the branch and the name of the branch that HEAD names, each empty when absent.
func parseLsRemote(out, branch string) (sha, def string) {
	for _, line := range strings.Split(out, "\n") {
		if ref, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			def, _, _ = strings.Cut(ref, "\t")
			continue
		}
		if id, name, ok := strings.Cut(line, "\t"); ok && name == "refs/heads/"+branch {
			sha = id
		}
	}
	return sha, def
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

// HoldsWork reports whether a task's workspace holds work that Remove would delete.
// A worktree holds work when git status shows changes or untracked files. A plain folder holds work when it is not empty.
func (w *Workspaces) HoldsWork(t task.Task) (bool, error) {
	dir := w.dir(t.ID)
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
		out, err := git(dir, "status", "--porcelain")
		return out != "", err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	return len(entries) > 0, err
}

// HasRemote reports whether the task's repo has a remote. A repo that exists only locally has none.
func (w *Workspaces) HasRemote(t task.Task) (bool, error) {
	dir := w.dir(t.ID)
	if _, err := os.Stat(dir); err != nil {
		dir = w.RepoFolder(t.Repo)
	}
	out, err := git(dir, "remote")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// RepoFolder returns the folder that a task's worktree belongs to: a local repo itself, or the clone of a URL.
// The clone folder is known before the clone exists.
func (w *Workspaces) RepoFolder(repo string) string {
	if local, ok := localRepo(repo); ok {
		return local
	}
	return filepath.Join(w.home, "repos", cloneName(repo))
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
