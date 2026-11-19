package up

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/launcher"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) }

// taskNewReply creates a task for repo through an engine with the real Claude adapter and workspaces.
// The fake Claude config trusts the given folders.
func taskNewReply(t *testing.T, repo string, trusted ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	var entries []string
	for _, dir := range trusted {
		entries = append(entries, `"`+dir+`":{"hasTrustDialogAccepted":true}`)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"projects":{`+strings.Join(entries, ",")+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sfyHome := t.TempDir()
	cfg := config.Config{}
	m := &fakeMux{alive: map[string]bool{}}
	agent := claude.New(cfg, sfyHome, "/bin/sfy")
	e := engine.New(store.Files(sfyHome), launcher.New(m, agent, nil), agent, workspace.New(sfyHome), cfg, fixedClock{})
	body := "# Title\n\n## Description\n\nDo it.\n\n## Goal\n\nDone.\n\n## Repo\n\n" + repo + "\n"
	ans := e.Handle(protocol.Request{Command: "task new", Body: body})
	if !ans.OK {
		t.Fatal(ans.Error)
	}
	return ans.Text
}

func TestTaskNewWarnsForARepoInsideATrustedParent(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(parent, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(context.Background(), "git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if reply := taskNewReply(t, repo, parent); !strings.Contains(reply, repo) {
		t.Errorf("a nested repo needs its own trust: %q", reply)
	}
	if reply := taskNewReply(t, repo, repo); strings.ContainsAny(reply, " \n") {
		t.Errorf("a trusted repo gets only the task id: %q", reply)
	}
}

func TestTaskNewWarnsForAUrlRepoWhoseCloneDoesNotExist(t *testing.T) {
	reply := taskNewReply(t, "https://example.com/org/thing.git")
	if !strings.Contains(reply, filepath.Join("repos", "thing-")) {
		t.Errorf("reply %q", reply)
	}
}
