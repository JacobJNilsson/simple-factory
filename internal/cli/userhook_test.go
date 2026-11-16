package cli

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

// notifyLauncher records the notes that sfy sends to the operator's terminal.
type notifyLauncher struct {
	quietLauncher
	mu    *sync.Mutex
	notes *[]string
}

func (n notifyLauncher) Notify(name, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	*n.notes = append(*n.notes, name+": "+text)
	return nil
}

const permissionInput = `{"session_id":"%s","tool_name":"Bash","tool_input":{"command":"ls"}}`

func userHook(env func(string) string, event, input string) (code int, out, errb string) {
	var o, e bytes.Buffer
	code = Run([]string{"hook", "--user", event}, env, strings.NewReader(input), &o, &e)
	return code, o.String(), e.String()
}

func TestUserHookActsForTheRegisteredSession(t *testing.T) {
	var mu sync.Mutex
	var notes []string
	env := liveServerWith(t, notifyLauncher{mu: &mu, notes: &notes}, nil)
	if err := registration.Write(env("SFY_HOME"), registration.Record{ID: "s-main"}); err != nil {
		t.Fatal(err)
	}

	code, out, errb := userHook(env, "permission", strings.Replace(permissionInput, "%s", "s-main", 1))

	mu.Lock()
	defer mu.Unlock()
	if code != exitOK || out != "" || errb != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errb)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "Tool: Bash. Command: ls") {
		t.Fatalf("notes %q", notes)
	}
}

func TestUserHookIsSilentForAnotherSession(t *testing.T) {
	var mu sync.Mutex
	var notes []string
	env := liveServerWith(t, notifyLauncher{mu: &mu, notes: &notes}, nil)
	if err := registration.Write(env("SFY_HOME"), registration.Record{ID: "s-main"}); err != nil {
		t.Fatal(err)
	}

	for _, event := range []string{"permission", "turn-ended", "idle"} {
		code, out, errb := userHook(env, event, strings.Replace(permissionInput, "%s", "s-worker", 1))
		if code != exitOK || out != "" || errb != "" {
			t.Errorf("%s: code %d, stdout %q, stderr %q", event, code, out, errb)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(notes) != 0 {
		t.Fatalf("a session that did not register got notes %q", notes)
	}
}

func TestUserHookWithoutARegistrationMakesNoSocketCall(t *testing.T) {
	// The home has no socket, so a call would fail with the not-running code.
	env := envOf(map[string]string{"SFY_HOME": t.TempDir()})

	code, out, errb := userHook(env, "turn-ended", `{"session_id":"s-1"}`)

	if code != exitOK || out != "" || errb != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errb)
	}
}

func TestUserHookIsSilentForAnAgentThatSfyStarted(t *testing.T) {
	home := t.TempDir()
	if err := registration.Write(home, registration.Record{ID: "s-main"}); err != nil {
		t.Fatal(err)
	}
	env := envOf(map[string]string{"SFY_HOME": home, "SFY_AGENT": "worker-t1"})

	code, out, errb := userHook(env, "permission", `{"session_id":"s-main"}`)

	if code != exitOK || out != "" || errb != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errb)
	}
}

func TestUserHookRejectsAnUnknownEvent(t *testing.T) {
	code, _, errb := userHook(envOf(nil), "bogus", "")

	if code != exitUsage || !strings.Contains(errb, "usage:") {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
}
