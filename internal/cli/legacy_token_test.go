package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/store"
)

const mainConversation = "11111111-2222-4333-8444-555555555555"

// preTokenHome returns a state folder with the session folders that an sfy without tokens left behind.
func preTokenHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, name := range []string{"main", "worker-t1", "worker-t2"} {
		if err := os.MkdirAll(filepath.Join(home, "sessions", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	id := filepath.Join(home, "sessions", "main", "conversation-id")
	if err := os.WriteFile(id, []byte(mainConversation+"\n"), 0o600); err != nil {
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
	eng := engine.New(store.Files(t.TempDir()), quietLauncher{}, repoWorkspaces{}, config.Config{}, fixedClock{})
	eng.SetAuth(identity.Folder{Home: home})
	ans := eng.Handle(req)
	return ans.OK, ans.Text + ans.Error
}

func TestSessionsThatStartedBeforeTokensWorkAfterARestart(t *testing.T) {
	home := preTokenHome(t)
	sessions := map[string]map[string]string{
		"main":      {"SFY_AGENT": "main", "CLAUDE_CODE_SESSION_ID": mainConversation},
		"worker-t1": {"SFY_AGENT": "worker-t1", "SFY_TASK": "t1"},
	}

	// Before the restart, the session has no token.
	for name, env := range sessions {
		if ok, text := answer(t, home, env, "status"); ok || !strings.Contains(text, "Restart the session, or run sfy restart") {
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
