package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/agentproc"
	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/store"
)

const leadConversation = "11111111-2222-4333-8444-555555555555"

// preTokenHome returns a factory folder with the session folders that an sfy without tokens left behind, with the folder of the lead moved to its new name.
func preTokenHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, name := range []string{"lead", "worker-t1", "worker-t2"} {
		if err := os.MkdirAll(filepath.Join(home, "sessions", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	id := filepath.Join(home, "sessions", "lead", "conversation-id")
	if err := os.WriteFile(id, []byte(leadConversation+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// answer runs one command line as the session that env describes, through a token-checking engine.
func answer(t *testing.T, home string, env map[string]string, args ...string) (ok bool, text string) {
	t.Helper()
	env["SFY_HOME"] = home
	req, err := parse(args, envOf(env), strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(store.Files(t.TempDir()), quietLauncher{}, nil, repoWorkspaces{}, config.Config{}, fixedClock{})
	eng.SetAuth(identity.Folder{Home: home})
	ans := eng.Handle(req)
	return ans.OK, ans.Text + ans.Error
}

func TestASessionReadsItsTokenOnlyFromInsideItsProcess(t *testing.T) {
	home := t.TempDir()
	folder := identity.Folder{Home: home}
	for _, name := range []string{"worker-t1", "worker-t2"} {
		if _, err := folder.Issue(name); err != nil {
			t.Fatal(err)
		}
	}
	// The test process stands in for the agent process that sfy run recorded for worker-t1.
	self, err := agentproc.Self()
	if err != nil {
		t.Fatal(err)
	}
	if err := agentproc.Write(filepath.Join(home, "sessions", "worker-t1", agentproc.ProcessFile), self); err != nil {
		t.Fatal(err)
	}

	if ok, text := answer(t, home, map[string]string{"SFY_AGENT": "worker-t1", "SFY_TASK": "t1"}, "status"); !ok {
		t.Errorf("worker-t1 in its own process: %q", text)
	}
	if ok, text := answer(t, home, map[string]string{"SFY_AGENT": "worker-t2", "SFY_TASK": "t1"}, "status"); ok || !strings.Contains(text, "Run sfy from the session that sfy started for this name") {
		t.Errorf("worker-t1 that claims worker-t2: ok %v, %q", ok, text)
	}
}

func TestSessionsThatStartedBeforeTokensWorkAfterARestart(t *testing.T) {
	home := preTokenHome(t)
	sessions := map[string]map[string]string{
		"lead under its old name": {"SFY_AGENT": "main", "CLAUDE_CODE_SESSION_ID": leadConversation},
		"worker-t1":               {"SFY_AGENT": "worker-t1", "SFY_TASK": "t1"},
	}

	// Before the restart, the session has no token.
	for name, env := range sessions {
		if ok, text := answer(t, home, env, "status"); ok || !strings.Contains(text, "needs a restart, or run sfy restart") {
			t.Fatalf("%s before the restart: ok %v, %q", name, ok, text)
		}
	}
	// sfy serve runs at the start of every restart.
	if err := (identity.Folder{Home: home}).IssueLegacy(); err != nil {
		t.Fatal(err)
	}

	for name, env := range sessions {
		if ok, text := answer(t, home, env, "status"); !ok {
			t.Errorf("%s after the restart: %q", name, text)
		}
	}
}
