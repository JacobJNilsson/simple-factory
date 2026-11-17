package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func settingsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	if content != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// settingsDoc holds the keys of a settings file that the tests read.
type settingsDoc struct {
	Model       string `json:"model"`
	Permissions struct {
		Allow []string `json:"allow"`
	} `json:"permissions"`
	Hooks map[string][]hookGroup `json:"hooks"`
}

func readSettings(t *testing.T, path string) settingsDoc {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s settingsDoc
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// commands returns every hook command of one event.
func commands(s settingsDoc, event string) []string {
	var out []string
	for _, g := range s.Hooks[event] {
		for _, h := range g.Hooks {
			out = append(out, h.Command)
		}
	}
	return out
}

func TestInstallUserHooksCreatesTheFile(t *testing.T) {
	path := settingsFile(t, "")

	changes, err := InstallUserHooks(path, "/opt/sfy")
	if err != nil {
		t.Fatal(err)
	}

	if len(changes) != 4 {
		t.Errorf("changes %q", changes)
	}
	s := readSettings(t, path)
	for event, arg := range map[string]string{"Stop": "turn-ended", "PermissionRequest": "permission", "Notification": "idle", "UserPromptSubmit": "busy"} {
		want := "/opt/sfy hook --user " + arg
		if got := commands(s, event); len(got) != 1 || got[0] != want {
			t.Errorf("%s hooks %q, want %q", event, got, want)
		}
	}
	if got := s.Hooks["Notification"][0].Matcher; got != "idle_prompt" {
		t.Errorf("notification matcher %q", got)
	}
}

func TestInstallUserHooksKeepsOtherSettingsAndHooks(t *testing.T) {
	path := settingsFile(t, `{
  "model": "opus",
  "permissions": {"allow": ["Bash(ls *)"]},
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "notify-me", "timeout": 5}]}],
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "audit"}]}]
  }
}`)

	if _, err := InstallUserHooks(path, "sfy"); err != nil {
		t.Fatal(err)
	}

	s := readSettings(t, path)
	if s.Model != "opus" {
		t.Errorf("model %q", s.Model)
	}
	if allow := s.Permissions.Allow; len(allow) != 1 || allow[0] != "Bash(ls *)" {
		t.Errorf("allow %q", allow)
	}
	if got := commands(s, "Stop"); strings.Join(got, "|") != "notify-me|sfy hook --user turn-ended" {
		t.Errorf("Stop hooks %q", got)
	}
	if got := commands(s, "PreToolUse"); len(got) != 1 || got[0] != "audit" {
		t.Errorf("PreToolUse hooks %q", got)
	}
}

func TestInstallUserHooksTwiceChangesNothing(t *testing.T) {
	path := settingsFile(t, `{"model": "opus"}`)
	if _, err := InstallUserHooks(path, "sfy"); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	changes, err := InstallUserHooks(path, "sfy")
	if err != nil {
		t.Fatal(err)
	}

	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 || string(first) != string(second) {
		t.Fatalf("second run changed %q\nbefore %s\nafter %s", changes, first, second)
	}
}

func TestInstallUserHooksUpdatesAMovedBinary(t *testing.T) {
	path := settingsFile(t, "")
	if _, err := InstallUserHooks(path, "/old/sfy"); err != nil {
		t.Fatal(err)
	}

	changes, err := InstallUserHooks(path, "/new/sfy")
	if err != nil {
		t.Fatal(err)
	}

	if len(changes) != 4 || !strings.HasPrefix(changes[0], "Updated") {
		t.Errorf("changes %q", changes)
	}
	if got := commands(readSettings(t, path), "Stop"); len(got) != 1 || got[0] != "/new/sfy hook --user turn-ended" {
		t.Errorf("Stop hooks %q", got)
	}
}

func TestInstallUserHooksLeavesAFileThatIsNotJSONAlone(t *testing.T) {
	path := settingsFile(t, "{not json")

	if _, err := InstallUserHooks(path, "sfy"); err == nil {
		t.Fatal("want an error")
	}

	if b, _ := os.ReadFile(path); string(b) != "{not json" {
		t.Errorf("file changed to %q", b)
	}
}

func TestUserSettingsPathFollowsTheConfigFolder(t *testing.T) {
	env := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }

	if got, _ := UserSettingsPath(env(map[string]string{"HOME": "/h"})); got != "/h/.claude/settings.json" {
		t.Errorf("home path %q", got)
	}
	if got, _ := UserSettingsPath(env(map[string]string{"HOME": "/h", "CLAUDE_CONFIG_DIR": "/c"})); got != "/c/settings.json" {
		t.Errorf("config path %q", got)
	}
	if _, err := UserSettingsPath(env(nil)); err == nil {
		t.Error("want an error without HOME")
	}
}
