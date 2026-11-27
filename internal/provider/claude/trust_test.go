package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
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

func TestLegacyConfigFileWinsWhenItHoldsData(t *testing.T) {
	home := useHome(t)
	legacy := filepath.Join(home, ".claude", ".config.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	for content, want := range map[string]string{"": filepath.Join(home, ".claude.json"), "{}": legacy} {
		if err := os.WriteFile(legacy, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := claude.ConfigPath()
		if err != nil || got != want {
			t.Errorf("legacy file %q: got %q, %v, want %q", content, got, err, want)
		}
	}
}

// trustConfig writes content as the config file in a fresh HOME and returns the file's path.
func trustConfig(t *testing.T, content string) string {
	t.Helper()
	home := useHome(t)
	writeConfig(t, home, content)
	return filepath.Join(home, ".claude.json")
}

func trust(t *testing.T, dir string) error {
	t.Helper()
	p, _ := setup(t, bin)
	return p.Trust(dir)
}

// defaultEntry is the project entry that Claude Code writes for a new folder on accept.
const defaultEntry = `{"allowedTools":[],"mcpContextUris":[],"mcpServers":{},"enabledMcpjsonServers":[],"disabledMcpjsonServers":[],` +
	`"hasTrustDialogAccepted":true,"hasClaudeMdExternalIncludesApproved":false,"hasClaudeMdExternalIncludesWarningShown":false}`

// parse returns JSON in a canonical form with sorted keys and no spacing, so that two values compare as strings.
func parse(t *testing.T, b []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func decode(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return parse(t, b)
}

// project returns the canonical entry of key in the config at path, or "" when it has none.
func project(t *testing.T, path, key string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Projects map[string]json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	raw, ok := file.Projects[key]
	if !ok {
		return ""
	}
	return parse(t, raw)
}

func accepted(t *testing.T, path, key string) bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Projects map[string]struct {
			Accepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	return file.Projects[key].Accepted
}

// onlyConfigIn fails when the config's folder holds anything other than the config file, such as a lock or a temporary file.
func onlyConfigIn(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(path) {
			t.Errorf("left %s behind", e.Name())
		}
	}
}

func TestTrustOnAWorktreeMarksTheMainCheckout(t *testing.T) {
	repo := newRepo(t)
	tree := filepath.Join(realDir(t), "tree")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "task", tree)
	path := trustConfig(t, `{}`)
	if err := trust(t, tree); err != nil {
		t.Fatal(err)
	}
	if !accepted(t, path, repo) {
		t.Fatalf("no trust for the main checkout: %v", decode(t, path))
	}
	if project(t, path, tree) != "" {
		t.Error("sfy wrote a key for the worktree")
	}
	if w := warning(t, tree); w != "" {
		t.Errorf("warning after trust: %q", w)
	}
}

func TestTrustOnAPlainFolderMarksTheFolder(t *testing.T) {
	dir := realDir(t)
	path := trustConfig(t, `{}`)
	if err := trust(t, dir); err != nil {
		t.Fatal(err)
	}
	if !accepted(t, path, dir) {
		t.Fatalf("no trust for the folder: %v", decode(t, path))
	}
}

func TestTrustKeepsEveryOtherValue(t *testing.T) {
	dir := realDir(t)
	others := `"userID":"u<1>","oauthAccount":{"emailAddress":"a@b","n":[1,2.5,null]},"numStartups":7`
	other := `"/other":{"hasTrustDialogAccepted":false,"allowedTools":["x"]}`
	path := trustConfig(t, `{`+others+`,"projects":{`+other+`}}`)
	if err := trust(t, dir); err != nil {
		t.Fatal(err)
	}
	want := parse(t, []byte(`{`+others+`,"projects":{`+other+`,"`+dir+`":`+defaultEntry+`}}`))
	if got := decode(t, path); got != want {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestTrustOnAnEntrySetsOnlyTheFlag(t *testing.T) {
	dir := realDir(t)
	path := trustConfig(t, `{"projects":{"`+dir+`":{"hasTrustDialogAccepted":false,"allowedTools":["Bash(ls)"],"lastCost":1.5}}}`)
	if err := trust(t, dir); err != nil {
		t.Fatal(err)
	}
	want := parse(t, []byte(`{"hasTrustDialogAccepted":true,"allowedTools":["Bash(ls)"],"lastCost":1.5}`))
	if got := project(t, path, dir); got != want {
		t.Fatalf("entry %v, want %v", got, want)
	}
}

func TestTrustOnANewFolderWritesTheDefaultEntry(t *testing.T) {
	dir := realDir(t)
	path := trustConfig(t, `{"projects":{}}`)
	if err := trust(t, dir); err != nil {
		t.Fatal(err)
	}
	want := parse(t, []byte(defaultEntry))
	if got := project(t, path, dir); got != want {
		t.Fatalf("entry %v, want %v", got, want)
	}
}

func TestTrustOnATrustedFolderWritesNothing(t *testing.T) {
	dir := realDir(t)
	content := `{"projects": {"` + dir + `": {"hasTrustDialogAccepted": true}}}`
	path := trustConfig(t, content)
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := trust(t, dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != content || !info.ModTime().Equal(old) {
		t.Fatalf("file changed: %s at %v", b, info.ModTime())
	}
}

func TestTrustGivesUpOnALockThatStaysTaken(t *testing.T) {
	dir := realDir(t)
	path := trustConfig(t, `{}`)
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	claude.ShortenLockWait(t, 100*time.Millisecond)
	if err := trust(t, dir); !errors.Is(err, claude.ErrConfigLocked) {
		t.Fatalf("err %v, want %v", err, claude.ErrConfigLocked)
	}
	if b, _ := os.ReadFile(path); string(b) != `{}` {
		t.Errorf("file changed: %s", b)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("sfy removed the lock of another process: %v", err)
	}
}

func TestTrustWithoutAConfigFileFailsAndCreatesNone(t *testing.T) {
	home := useHome(t)
	if err := trust(t, realDir(t)); err == nil {
		t.Fatal("no error")
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("home holds %v", entries)
	}
}

func TestTrustRefusesAConfigFileThatIsALink(t *testing.T) {
	home := useHome(t)
	target := filepath.Join(realDir(t), "real.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".claude.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := trust(t, realDir(t)); err == nil {
		t.Fatal("no error")
	}
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Errorf("link now %q, %v", got, err)
	}
	if b, _ := os.ReadFile(target); string(b) != `{}` {
		t.Errorf("target changed: %s", b)
	}
	onlyConfigIn(t, link)
}

func TestTrustOnInvalidJSONFailsAndChangesNothing(t *testing.T) {
	path := trustConfig(t, `{"projects":`)
	if err := trust(t, realDir(t)); err == nil {
		t.Fatal("no error")
	}
	if b, _ := os.ReadFile(path); string(b) != `{"projects":` {
		t.Errorf("file changed: %s", b)
	}
	onlyConfigIn(t, path)
}

func TestTrustKeepsTheFileModeAndLeavesNoLockOrTemporaryFile(t *testing.T) {
	path := trustConfig(t, `{}`)
	if err := trust(t, realDir(t)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	onlyConfigIn(t, path)
}
