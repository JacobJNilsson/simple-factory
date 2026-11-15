// Package claude starts Claude Code sessions with sfy's hooks and permissions.
package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/envrun"
	"github.com/JacobJNilsson/simple-factory/internal/prompts"
	"github.com/JacobJNilsson/simple-factory/internal/provider"
)

type adapter struct {
	cfg    config.Config
	home   string
	sfyBin string
}

// New returns the adapter for Claude Code. The hooks in each session run sfyBin.
// CONTRACT: the sfy process picks an adapter by the provider setting, so New returns the interface.
func New(cfg config.Config, home, sfyBin string) provider.Provider {
	return adapter{cfg: cfg, home: home, sfyBin: sfyBin}
}

type hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	// Timeout is in seconds. A slow sfy process must not stall the agent.
	Timeout int `json:"timeout"`
}

type hookGroup struct {
	Hooks []hook `json:"hooks"`
}

type permissions struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

type settings struct {
	Permissions permissions            `json:"permissions"`
	Hooks       map[string][]hookGroup `json:"hooks"`
}

var sessionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Command writes the session's settings file and returns the argv that starts claude.
func (a adapter) Command(spec engine.SessionSpec) ([]string, error) {
	switch spec.Kind {
	case "main", "worker", "reviewer":
	default:
		return nil, fmt.Errorf("claude: unknown session kind %q", spec.Kind)
	}
	if !sessionName.MatchString(spec.Name) {
		return nil, fmt.Errorf("claude: bad session name %q", spec.Name)
	}
	role, err := prompts.For(spec.Kind, a.home)
	if err != nil {
		return nil, err
	}
	path, err := a.writeSettings(spec)
	if err != nil {
		return nil, err
	}
	env := []string{"SFY_AGENT=" + spec.Name}
	if spec.Task != "" {
		env = append(env, "SFY_TASK="+spec.Task)
	}
	env = append(env,
		"SFY_HOME="+a.home,
		"PATH="+filepath.Dir(a.sfyBin)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	// sfy run keeps the values out of argv, and the process becomes claude itself after the exec.
	return envrun.Wrap(filepath.Dir(path), env, false, a.sfyBin, []string{
		"claude",
		"--name", spec.Name,
		"--model", spec.Model,
		"--permission-mode", "acceptEdits",
		"--settings", path,
		"--append-system-prompt", role,
		spec.Prompt,
	})
}

func (a adapter) writeSettings(spec engine.SessionSpec) (string, error) {
	s := settings{
		Permissions: permissions{Allow: a.allow(spec), Deny: deny(spec)},
		Hooks: map[string][]hookGroup{
			"Stop":              {{Hooks: []hook{a.hook("turn-ended")}}},
			"PermissionRequest": {{Hooks: []hook{a.hook("permission")}}},
		},
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	dir := filepath.Join(a.home, "sessions", spec.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	return path, nil
}

// hook builds a command hook. Claude Code runs the command through a shell, so a binary path with spaces is quoted.
func (a adapter) hook(event string) hook {
	return hook{Type: "command", Command: shellWord(a.sfyBin) + " hook " + event, Timeout: 10}
}

var safeWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellWord(s string) string {
	if safeWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (a adapter) allow(spec engine.SessionSpec) []string {
	rules := []string{}
	for _, c := range a.cfg.Allow {
		rules = append(rules, "Bash("+c+" *)")
	}
	// Agents run sfy by its name on the PATH, and the hooks run it by its full path.
	rules = append(rules, "Bash("+a.sfyBin+" *)", "Bash(sfy *)")
	if spec.Kind == "main" {
		rules = append(rules, a.mainReads()...)
	}
	return rules
}

// mainReads lets the main agent read the sfy home without a prompt and list folders. It gets no cat rule, because a rule matches the command text and cannot limit a path.
func (a adapter) mainReads() []string {
	return []string{"Read(" + rulePath(a.home) + "/**)", "Bash(ls *)"}
}

// deny returns the deny rules of a session. The main agent talks to the operator, so it denies nothing.
func deny(spec engine.SessionSpec) []string {
	rules := []string{}
	switch spec.Kind {
	case "reviewer":
		return []string{"AskUserQuestion", "Edit", "Write", "NotebookEdit"}
	case "worker":
		rules = append(rules, "AskUserQuestion")
		for _, p := range spec.Protected {
			p = rulePath(p)
			// Claude Code applies an Edit rule to Write and NotebookEdit too. The second rule covers a folder's files.
			rules = append(rules, "Edit("+p+")", "Edit("+strings.TrimSuffix(p, "/")+"/**)")
		}
	}
	return rules
}

// rulePath writes a path in the rule syntax. An absolute path starts with two slashes, and a relative path stays relative to the session's folder.
func rulePath(p string) string {
	if strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return strings.TrimPrefix(p, "./")
}
