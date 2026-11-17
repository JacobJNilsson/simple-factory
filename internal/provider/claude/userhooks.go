package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UserSettingsPath returns the user's Claude Code settings file.
// CLAUDE_CONFIG_DIR moves the folder that holds it, as it does for Claude Code.
func UserSettingsPath(env func(string) string) (string, error) {
	if dir := env("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home := env("HOME")
	if home == "" {
		return "", errors.New("claude: HOME is not set, so sfy cannot find the user settings")
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// userEvents lists the hooks that sfy adds to the user settings, the same events as each session's own settings.
var userEvents = []struct{ name, arg, matcher string }{
	{"Stop", "turn-ended", ""},
	{"PermissionRequest", "permission", ""},
	{"Notification", "idle", "idle_prompt"},
	{"UserPromptSubmit", "busy", ""},
}

// userHook builds the hook command that marks the call as coming from the user settings.
func userHook(sfyBin, arg string) hook {
	return hook{Type: "command", Command: shellWord(sfyBin) + " hook --user " + arg, Timeout: 10}
}

// InstallUserHooks merges sfy's hooks into the settings file at path and returns one line for each change.
// It keeps every other key and hook. A second call changes nothing and returns no lines.
// Go sorts the keys of the file it writes, so a file that sfy changed loses its key order.
func InstallUserHooks(path, sfyBin string) ([]string, error) {
	doc := map[string]json.RawMessage{}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("claude: %w", err)
	case len(bytes.TrimSpace(b)) > 0:
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("claude: %s is not a JSON object, so sfy left it alone: %w", path, err)
		}
	}
	events := map[string][]json.RawMessage{}
	if raw, ok := doc["hooks"]; ok {
		if err := json.Unmarshal(raw, &events); err != nil {
			return nil, fmt.Errorf("claude: the hooks key of %s is not an object of lists, so sfy left it alone: %w", path, err)
		}
	}
	var changes []string
	for _, e := range userEvents {
		want := hookGroup{Matcher: e.matcher, Hooks: []hook{userHook(sfyBin, e.arg)}}
		groups, verb, err := mergeGroup(events[e.name], want, " hook --user "+e.arg)
		if err != nil {
			return nil, err
		}
		if verb != "" {
			events[e.name] = groups
			changes = append(changes, fmt.Sprintf("%s the %s hook: %s", verb, e.name, want.Hooks[0].Command))
		}
	}
	if len(changes) == 0 {
		return nil, nil
	}
	if doc["hooks"], err = json.Marshal(events); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	return changes, nil
}

// mergeGroup adds want to groups. It returns the verb "Added" or "Updated", or no verb when want is in groups already.
// A group that holds only an older sfy hook for the same event, with another binary path, gets replaced.
// suffix is the end of every sfy command for the event.
func mergeGroup(groups []json.RawMessage, want hookGroup, suffix string) ([]json.RawMessage, string, error) {
	var typed []hookGroup
	for _, raw := range groups {
		var g hookGroup
		// A group that sfy cannot read stays as it is.
		_ = json.Unmarshal(raw, &g)
		typed = append(typed, g)
		for _, h := range g.Hooks {
			if h.Command == want.Hooks[0].Command {
				return groups, "", nil
			}
		}
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		return nil, "", fmt.Errorf("claude: %w", err)
	}
	for i, g := range typed {
		if len(g.Hooks) == 1 && strings.HasSuffix(g.Hooks[0].Command, suffix) {
			groups[i] = encoded
			return groups, "Updated", nil
		}
	}
	return append(groups, encoded), "Added", nil
}
