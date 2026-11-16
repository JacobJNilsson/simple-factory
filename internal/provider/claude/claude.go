// Package claude starts Claude Code sessions with sfy's hooks and permissions.
package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/envrun"
	"github.com/JacobJNilsson/simple-factory/internal/prompts"
	"github.com/JacobJNilsson/simple-factory/internal/provider"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

type adapter struct {
	cfg    config.Config
	home   string
	sfyBin string
	goos   string
	uid    int
}

// New returns the adapter for Claude Code. The hooks in each session run sfyBin.
// CONTRACT: the sfy process picks an adapter by the provider setting, so New returns the interface.
func New(cfg config.Config, home, sfyBin string) provider.Provider {
	return adapter{cfg: cfg, home: home, sfyBin: sfyBin, goos: runtime.GOOS, uid: os.Getuid()}
}

type hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	// Timeout is in seconds. A slow sfy process must not stall the agent.
	Timeout int `json:"timeout"`
}

type hookGroup struct {
	Matcher string `json:"matcher,omitempty"`
	Hooks   []hook `json:"hooks"`
}

type filesystem struct {
	DenyRead   []string `json:"denyRead"`
	AllowWrite []string `json:"allowWrite"`
}

// sandbox is Claude Code's sandbox block. Workers and reviewers get it, and the main agent does not.
type sandbox struct {
	Enabled                  bool       `json:"enabled"`
	FailIfUnavailable        bool       `json:"failIfUnavailable"`
	AutoAllowBashIfSandboxed bool       `json:"autoAllowBashIfSandboxed"`
	AllowUnsandboxedCommands bool       `json:"allowUnsandboxedCommands"`
	Filesystem               filesystem `json:"filesystem"`
	ExcludedCommands         []string   `json:"excludedCommands"`
	// WeakerNetworkIsolation lets macOS commands reach the system TLS trust service, which Go tools need.
	WeakerNetworkIsolation bool `json:"enableWeakerNetworkIsolation,omitempty"`
	// Network lists the Unix sockets that sandboxed commands may use. Claude Code reads it on macOS only.
	Network *network `json:"network,omitempty"`
}

type network struct {
	AllowUnixSockets []string `json:"allowUnixSockets"`
}

type permissions struct {
	DefaultMode string   `json:"defaultMode,omitempty"`
	Allow       []string `json:"allow"`
	Deny        []string `json:"deny"`
}

type settings struct {
	Sandbox     *sandbox               `json:"sandbox,omitempty"`
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
	role, err := prompts.Role(spec.Kind, a.home, spec.HasRepo)
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
	clean := spec.Kind != "main"
	if clean {
		env = append(env, a.inherited()...)
	}
	argv := []string{"claude", "--name", spec.Name, "--model", spec.Model}
	// The main agent gets no permission flag, so claude uses the operator's own default mode.
	if spec.Kind != "main" {
		argv = append(argv, "--permission-mode", "acceptEdits")
	}
	prompt := spec.Prompt
	argv = append(argv, "--settings", path, "--append-system-prompt", role)
	if spec.Kind == "main" {
		conversation, resumed, err := a.mainConversation(spec)
		if err != nil {
			return nil, err
		}
		argv = append(argv, conversation...)
		if resumed && spec.ResumePrompt != "" {
			prompt = spec.ResumePrompt
		}
	}
	argv = append(argv, prompt)
	// sfy run keeps the values out of argv, and the process becomes claude itself after the exec.
	return envrun.Wrap(filepath.Dir(path), env, clean, a.sfyBin, argv)
}

// inherited returns the variables that a clean worker or reviewer keeps from sfy's environment.
// CLAUDE_CONFIG_DIR is the only variable that a subscription login needs, and only when it is set.
// SSH_AUTH_SOCK stays because git push runs outside the sandbox and may use SSH.
func (a adapter) inherited() []string {
	names := append([]string{
		"HOME", "USER", "LOGNAME", "SHELL", "TERM", "LANG", "TMPDIR", "CLAUDE_CONFIG_DIR", "SSH_AUTH_SOCK",
	}, a.cfg.PassEnv...)
	var out []string
	seen := map[string]bool{}
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if seen[k] || k == "PATH" || strings.HasPrefix(k, "SFY_") {
			continue
		}
		if slices.Contains(names, k) || strings.HasPrefix(k, "LC_") {
			seen[k] = true
			out = append(out, e)
		}
	}
	return out
}

func (a adapter) writeSettings(spec engine.SessionSpec) (string, error) {
	s := settings{
		Permissions: permissions{Allow: a.allow(spec), Deny: deny(spec)},
		Hooks: map[string][]hookGroup{
			"Stop":              {{Hooks: []hook{a.hook("turn-ended")}}},
			"PermissionRequest": {{Hooks: []hook{a.hook("permission")}}},
			"Notification":      {{Matcher: "idle_prompt", Hooks: []hook{a.hook("idle")}}},
		},
	}
	if spec.Kind != "main" {
		if err := a.sandboxFor(&s); err != nil {
			return "", err
		}
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

// sandboxFor turns on the sandbox for s, with no fallback to unsandboxed commands.
// A Linux sandbox ignores a write path that does not exist, so each one is created first.
func (a adapter) sandboxFor(s *settings) error {
	for _, p := range a.cfg.AllowWrite {
		dir, err := expandHome(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("claude: %w", err)
		}
	}
	s.Sandbox = &sandbox{
		Enabled: true, FailIfUnavailable: true, AutoAllowBashIfSandboxed: true,
		Filesystem: filesystem{
			DenyRead:   append([]string{}, a.cfg.DenyRead...),
			AllowWrite: append([]string{}, a.cfg.AllowWrite...),
		},
	}
	// gh and git push fail inside the macOS sandbox, and the sandbox blocks the sfy socket. The allow rules keep the excluded calls narrow.
	s.Sandbox.WeakerNetworkIsolation = a.goos == "darwin"
	if a.goos == "darwin" {
		// Go tests bind sockets in their temp folder. allowUnixSockets takes a folder as a prefix and stays narrower than allowAllUnixSockets.
		s.Sandbox.Network = &network{AllowUnixSockets: []string{
			fmt.Sprintf("/tmp/claude-%d", a.uid), fmt.Sprintf("/private/tmp/claude-%d", a.uid),
		}}
	}
	s.Sandbox.ExcludedCommands = []string{"gh *", "git push *", "sfy *"}
	s.Permissions.DefaultMode = "acceptEdits"
	s.Permissions.Allow = append(s.Permissions.Allow, "WebFetch(domain:*)")
	return nil
}

func expandHome(p string) (string, error) {
	rest, ok := strings.CutPrefix(p, "~/")
	if !ok {
		return p, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	return filepath.Join(h, rest), nil
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
	switch spec.Kind {
	case "main":
		for _, c := range a.cfg.Allow {
			rules = append(rules, "Bash("+c+" *)")
		}
	case "worker":
		// gh posts as the operator, so only read and create commands pass, and edit passes for the body and the title only.
		// Merge, close, comment and review do not.
		rules = append(rules,
			"Bash(gh pr create *)", "Bash(gh pr view *)", "Bash(gh pr list *)", "Bash(gh pr diff *)", "Bash(gh pr checks *)",
			"Bash(gh pr edit * --body-file *)", "Bash(gh pr edit * --title *)",
			"Bash(gh issue create *)", "Bash(gh issue view *)", "Bash(gh issue list *)", "Bash(gh repo view *)")
		// A rule with no wildcard matches that command only, so no second branch, force flag or refspec passes.
		branch := workspace.Branch(spec.Task)
		rules = append(rules, "Bash(git push -u origin "+branch+")", "Bash(git push --force-with-lease origin "+branch+")")
	case "reviewer":
		rules = append(rules, "Bash(gh pr view *)", "Bash(gh pr diff *)", "Bash(gh pr checks *)")
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

// pushWithGitOptions denies a push that starts with a git option, as in git -C . push or git -c a=b push.
// Such a call does not match the excluded git push entry, so it would stay sandboxed and auto-approved.
// A deny rule beats the auto-approval. It cannot match the allowed exact pushes, which have no option before push.
const pushWithGitOptions = "Bash(git -* push*)"

// prEditStateFlags lists the gh pr edit patterns that a worker may not run. A wildcard allow rule such as
// gh pr edit * --body-file * also matches a call that adds a state flag, and a deny rule beats an allow rule.
var prEditStateFlags = []string{
	"Bash(gh pr edit *--add-*)", "Bash(gh pr edit *--remove-*)", "Bash(gh pr edit *--base*)", "Bash(gh pr edit * -B*)",
	"Bash(gh pr edit *--milestone*)", "Bash(gh pr edit * -m*)", "Bash(gh pr edit *--reviewer*)",
	"Bash(gh pr edit *--label*)", "Bash(gh pr edit *--assignee*)", "Bash(gh pr edit *--project*)",
}

// deny returns the deny rules of a session. The main agent talks to the operator, so it denies nothing.
func deny(spec engine.SessionSpec) []string {
	rules := []string{}
	switch spec.Kind {
	case "reviewer":
		return []string{"AskUserQuestion", "Edit", "Write", "NotebookEdit", pushWithGitOptions}
	case "worker":
		rules = append(rules, "AskUserQuestion", pushWithGitOptions)
		rules = append(rules, prEditStateFlags...)
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
