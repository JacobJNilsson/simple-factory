package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

// registeredEnv registers session s-main in a new factory folder and returns the environment of session id.
func registeredEnv(t *testing.T, id string) func(string) string {
	t.Helper()
	home := t.TempDir()
	if err := registration.Write(home, registration.Record{ID: "s-main"}); err != nil {
		t.Fatal(err)
	}
	return envOf(map[string]string{"SFY_HOME": home, "CLAUDE_CODE_SESSION_ID": id})
}

func TestAgentMapsTheRegisteredSessionToMain(t *testing.T) {
	if got := Agent(registeredEnv(t, "s-main")); got != "main" {
		t.Fatalf("got %q", got)
	}
}

func TestRegisteredSessionRunsCommandsAsMain(t *testing.T) {
	live := liveServer(t)
	env := func(k string) string {
		if k == "CLAUDE_CODE_SESSION_ID" {
			return "s-main"
		}
		return live(k)
	}
	if err := registration.Write(live("SFY_HOME"), registration.Record{ID: "s-main"}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer

	// Only the agents may run inbox. The operator may not.
	code := Run([]string{"inbox"}, env, strings.NewReader(""), &out, &errb)

	if code != exitOK {
		t.Fatalf("inbox: code %d, stderr %q", code, errb.String())
	}
	errb.Reset()

	code = Run([]string{"down"}, env, strings.NewReader(""), &out, &errb)

	if code != exitFailed || !strings.Contains(errb.String(), "not for a main") {
		t.Fatalf("down: code %d, stderr %q", code, errb.String())
	}
}

func TestWithIdentitySetsTheAgentVariable(t *testing.T) {
	env := WithIdentity(registeredEnv(t, "s-main"))

	if got := env("SFY_AGENT"); got != "main" {
		t.Errorf("SFY_AGENT %q", got)
	}
	if got := env("CLAUDE_CODE_SESSION_ID"); got != "s-main" {
		t.Errorf("other variables must pass, got %q", got)
	}
}
