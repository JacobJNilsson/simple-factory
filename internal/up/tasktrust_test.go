package up

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
// The fake Claude config trusts no folder.
func taskNewReply(t *testing.T, repo string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sfyHome := t.TempDir()
	cfg := config.Config{}
	m := &fakeMux{alive: map[string]bool{}}
	agent := claude.New(cfg, sfyHome, "/bin/sfy", []string{sfyHome})
	e := engine.New(store.Files(sfyHome), launcher.New(m, agent, nil), agent, workspace.New(sfyHome), cfg, fixedClock{})
	body := "# Title\n\n## Description\n\nDo it.\n\n## Goal\n\nDone.\n\n## Repo\n\n" + repo + "\n"
	ans := e.Handle(protocol.Request{Command: "task new", Body: body})
	if !ans.OK {
		t.Fatal(ans.Error)
	}
	return ans.Text
}

// The worker start marks the repo as trusted, so task new no longer warns about an untrusted repo.
func TestTaskNewGivesOnlyTheIdForAnUntrustedRepo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(context.Background(), "git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, r := range []string{repo, "https://example.com/org/thing.git"} {
		if reply := taskNewReply(t, r); reply != "t1" {
			t.Errorf("%s: reply %q", r, reply)
		}
	}
}
