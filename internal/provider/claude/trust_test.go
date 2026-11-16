package claude_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// useHome points HOME at a temporary folder with no Claude Code config, and returns it.
func useHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

// trustHome writes a fake .claude.json that trusts the given folders.
func trustHome(t *testing.T, trusted ...string) {
	t.Helper()
	projects := map[string]map[string]any{}
	for _, dir := range trusted {
		projects[dir] = map[string]any{"hasTrustDialogAccepted": true}
	}
	b, err := json.Marshal(map[string]any{"projects": projects})
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, useHome(t), string(b))
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
}

func realDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func newRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(realDir(t), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "init", "-q")
	run(t, repo, "git", "commit", "-q", "--allow-empty", "-m", "init")
	return repo
}

func warning(t *testing.T, dir string) string {
	t.Helper()
	p, _ := setup(t, bin)
	return p.TrustWarning(dir)
}

func TestTrustedFolderGivesNoWarning(t *testing.T) {
	dir := realDir(t)
	trustHome(t, dir)
	if w := warning(t, dir); w != "" {
		t.Fatalf("warning: %q", w)
	}
}

func TestUntrustedFolderWarnsAndNamesIt(t *testing.T) {
	dir := realDir(t)
	trustHome(t)
	w := warning(t, dir)
	if !strings.Contains(w, dir) {
		t.Fatalf("warning: %q", w)
	}
	if !strings.Contains(w, "open that agent's terminal") {
		t.Fatalf("warning does not tell the operator to open the agent's terminal: %q", w)
	}
}

func TestFolderUnderTrustedParentGivesNoWarning(t *testing.T) {
	parent := realDir(t)
	child := filepath.Join(parent, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	trustHome(t, parent)
	if w := warning(t, child); w != "" {
		t.Fatalf("warning: %q", w)
	}
}

func TestTrustedParentDoesNotCoverRepo(t *testing.T) {
	repo := newRepo(t)
	trustHome(t, filepath.Dir(repo))
	if w := warning(t, repo); !strings.Contains(w, repo) {
		t.Fatalf("warning: %q", w)
	}
}

func TestMissingFolderNeedsItsOwnKey(t *testing.T) {
	parent := realDir(t)
	clone := filepath.Join(parent, "repos", "thing-1234")
	trustHome(t, parent)
	if w := warning(t, clone); !strings.Contains(w, clone) {
		t.Fatalf("a trusted parent must not cover a clone that does not exist yet: %q", w)
	}
	trustHome(t, clone)
	if w := warning(t, clone); w != "" {
		t.Fatalf("warning: %q", w)
	}
}

func TestSymlinkedFolderUsesItsResolvedKey(t *testing.T) {
	dir := realDir(t)
	link := filepath.Join(realDir(t), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	trustHome(t, dir)
	if w := warning(t, link); w != "" {
		t.Fatalf("warning: %q", w)
	}
}

func TestWorktreeIsCoveredByTheMainCheckout(t *testing.T) {
	repo := newRepo(t)
	tree := filepath.Join(realDir(t), "tree")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "task", tree)
	sub := filepath.Join(tree, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	trustHome(t, repo)
	for _, dir := range []string{tree, sub, repo} {
		if w := warning(t, dir); w != "" {
			t.Errorf("%s: warning %q", dir, w)
		}
	}
}

func TestTrustOfTheWorktreeFolderDoesNotCount(t *testing.T) {
	repo := newRepo(t)
	tree := filepath.Join(realDir(t), "tree")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "task", tree)
	trustHome(t, tree)
	if w := warning(t, tree); !strings.Contains(w, tree) {
		t.Errorf("warning: %q", w)
	}
}

func TestMissingFileGivesAWarningThatNamesTheFolder(t *testing.T) {
	useHome(t)
	dir := realDir(t)
	if w := warning(t, dir); !strings.Contains(w, dir) {
		t.Fatalf("warning: %q", w)
	}
}

func TestFileWithoutProjectsMeansUntrusted(t *testing.T) {
	writeConfig(t, useHome(t), `{"userID":"x"}`)
	dir := realDir(t)
	if w := warning(t, dir); !strings.Contains(w, dir) {
		t.Fatalf("warning: %q", w)
	}
}

func TestBrokenFileGivesAWarningThatLeaksNothing(t *testing.T) {
	writeConfig(t, useHome(t), `{"userID":"zz-secret", "projects":{"/other":{"hasTrustDialogAccepted":false,"note":"zz-note"}`)
	if w := warning(t, realDir(t)); w == "" || strings.Contains(w, "zz-") {
		t.Fatalf("warning: %q", w)
	}
}

func TestOtherEntriesNeverReachTheWarning(t *testing.T) {
	writeConfig(t, useHome(t), `{"userID":"zz-secret","projects":{"/other":{"hasTrustDialogAccepted":false,"note":"zz-note"}}}`)
	if w := warning(t, realDir(t)); w == "" || strings.Contains(w, "zz-") || strings.Contains(w, "/other") {
		t.Fatalf("warning: %q", w)
	}
}

func TestConfigDirMovesTheFile(t *testing.T) {
	dir := realDir(t)
	useHome(t)
	cfgDir := t.TempDir()
	writeConfig(t, cfgDir, `{"projects":{"`+dir+`":{"hasTrustDialogAccepted":true}}}`)
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	if w := warning(t, dir); w != "" {
		t.Fatalf("warning: %q", w)
	}
}
