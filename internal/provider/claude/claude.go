// Package claude starts Claude Code sessions with sfy's hooks and permissions.
package claude

import (
	"encoding/json"
	"errors"
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
	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/prompts"
	"github.com/JacobJNilsson/simple-factory/internal/provider"
)

type adapter struct {
	cfg    config.Config
	home   string
	sfyBin string
	// layers are the folders that the role prompts come from, lowest first, as config.Layers returns them.
	layers []string
	goos   string
	uid    int
	sys    system
}

// New returns the adapter for Claude Code. The hooks in each session run sfyBin, and the role prompts come from layers.
// CONTRACT: the sfy process picks an adapter by the provider setting, so New returns the interface.
func New(cfg config.Config, home, sfyBin string, layers []string) provider.Provider {
	return adapter{cfg: cfg, home: home, sfyBin: sfyBin, layers: layers, goos: runtime.GOOS, uid: os.Getuid(), sys: realSystem()}
}

type hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	// Timeout is in seconds. A slow sfy process must not stall the agent.
	Timeout int `json:"timeout"`
	// AsyncRewake runs the hook in the background. Exit code 2 gives its stderr to the agent and starts a turn, also when the session is idle.
	AsyncRewake bool `json:"asyncRewake,omitempty"`
}

type hookGroup struct {
	Matcher string `json:"matcher,omitempty"`
	Hooks   []hook `json:"hooks"`
}

type filesystem struct {
	DenyRead   []string `json:"denyRead"`
	AllowWrite []string `json:"allowWrite"`
}

// sandbox is Claude Code's sandbox block. Workers and reviewers get it, and the lead does not.
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
	case "lead", "worker", "reviewer":
	default:
		return nil, fmt.Errorf("claude: unknown session kind %q", spec.Kind)
	}
	if spec.Kind != "lead" && spec.TaskFile == "" {
		return nil, fmt.Errorf("claude: %s has no task file", spec.Name)
	}
	if !sessionName.MatchString(spec.Name) {
		return nil, fmt.Errorf("claude: bad session name %q", spec.Name)
	}
	role, err := a.role(spec)
	if err != nil {
		return nil, err
	}
	// The worker prompt names the branch as <branch>, and the workspace decides its name.
	if spec.Branch != "" {
		role = strings.ReplaceAll(role, "<branch>", spec.Branch)
	}
	path, err := a.writeSettings(spec)
	if err != nil {
		return nil, err
	}
	if err := removeStaleNote(a.home, spec.Name); err != nil {
		return nil, err
	}
	token, err := identity.Folder{Home: a.home}.Issue(spec.Name)
	if err != nil {
		return nil, err
	}
	env := []string{"SFY_AGENT=" + spec.Name, "SFY_TOKEN=" + token}
	if spec.Task != "" {
		env = append(env, "SFY_TASK="+spec.Task)
	}
	env = append(env,
		"SFY_HOME="+a.home,
		"PATH="+filepath.Dir(a.sfyBin)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	clean := spec.Kind != "lead"
	if clean {
		env = append(env, a.inherited(own(spec.PassEnv, a.cfg.PassEnv))...)
	}
	argv := []string{a.claudeCommand(), "--name", spec.Name, "--model", spec.Model}
	// The lead gets no permission flag, so claude uses the operator's own default mode.
	if spec.Kind != "lead" {
		argv = append(argv, "--permission-mode", "acceptEdits")
	}
	prompt := spec.Prompt
	argv = append(argv, "--settings", path, "--append-system-prompt", role)
	// The lead gets the skills too, because it runs in the working folder and sees no skill in the factory folder.
	plugin, err := a.pluginArgs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	argv = append(argv, plugin...)
	if spec.Kind == "lead" {
		conversation, resumed, err := a.leadConversation(spec)
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

// role returns the prompt of a session. A spec with a Role takes that prompt, and its RepoRole part for a task with a repo.
func (a adapter) role(spec engine.SessionSpec) (string, error) {
	if spec.Role == "" {
		return prompts.Role(spec.Kind, a.layers, spec.HasRepo)
	}
	base, err := prompts.For(spec.Role, a.layers)
	if err != nil || !spec.HasRepo || spec.RepoRole == "" {
		return base, err
	}
	part, err := prompts.For(spec.RepoRole, a.layers)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(base, "\n") + "\n\n" + part, nil
}

// rulesKey returns the name that the session's session_rules and session_settings tables have: its agent, or its kind.
func rulesKey(spec engine.SessionSpec) string {
	if spec.Agent != "" {
		return spec.Agent
	}
	return spec.Kind
}

// own returns the agent's own list when it has one, and the factory's list otherwise.
func own(list *[]string, factory []string) []string {
	if list != nil {
		return *list
	}
	return factory
}

// inherited returns the variables that a clean worker or reviewer keeps from sfy's environment.
// CLAUDE_CONFIG_DIR is the only variable that a subscription login needs, and only when it is set.
// SSH_AUTH_SOCK stays because git push runs outside the sandbox and may use SSH.
func (a adapter) inherited(passEnv []string) []string {
	names := append([]string{
		"HOME", "USER", "LOGNAME", "SHELL", "TERM", "LANG", "TMPDIR", "CLAUDE_CONFIG_DIR", "SSH_AUTH_SOCK",
	}, passEnv...)
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
			"UserPromptSubmit":  {{Hooks: []hook{a.hook("busy")}}},
			// SessionStart asks Claude Code to watch the note file. FileChanged then wakes the agent with the file's text.
			"SessionStart": {{Hooks: []hook{a.hook("note-watch")}}},
			// A folder change replaces the watch set with the one that CwdChanged hooks return, so this hook returns it again.
			"CwdChanged":  {{Hooks: []hook{a.hook("note-watch")}}},
			"FileChanged": {{Hooks: []hook{a.rewakeHook("note")}}},
		},
	}
	switch {
	case spec.Kind == "lead":
		// The hook blocks a hand push, merge or rebase in a no-merge repo. sfy task integrate refuses there too.
		s.Hooks["PreToolUse"] = []hookGroup{{Matcher: "Bash", Hooks: []hook{a.hook("merge-guard")}}}
	case spec.Unsandboxed:
		// An agent outside the sandbox runs only the commands that its allow rules name.
		s.Permissions.DefaultMode = "acceptEdits"
	default:
		if err := a.sandboxFor(&s, spec, a.cfg.SessionRules[rulesKey(spec)].ExcludedCommands); err != nil {
			return "", err
		}
	}
	b, err := marshalSettings(s, a.extraSettings(spec.Kind, rulesKey(spec)))
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	dir := filepath.Join(a.home, "sessions", spec.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	// The new session has to prove its hooks, so the marker of an earlier session goes.
	if err := os.Remove(filepath.Join(dir, "hooks-ready")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("claude: %w", err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	return path, nil
}

// extraSettings returns the extra top-level keys for a session kind, from the session_settings table of key.
// Workers and reviewers turn off prompt suggestions by default, and a config table can override that.
func (a adapter) extraSettings(kind, key string) config.SessionValues {
	configured := a.cfg.SessionSettings[key]
	if kind == "lead" {
		return configured
	}
	extra := config.SessionValues{"promptSuggestionEnabled": false}
	for k, v := range configured {
		extra[k] = v
	}
	return extra
}

// marshalSettings encodes s. Without extra keys the output is the struct's own encoding.
// With extra keys, the output holds sfy's keys merged with the extra keys through mergeExtra, sorted by name.
func marshalSettings(s settings, extra config.SessionValues) ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil || len(extra) == 0 {
		return b, err
	}
	var merged config.SessionValues
	if err := json.Unmarshal(b, &merged); err != nil {
		return nil, err
	}
	// The round trip gives the TOML values the shapes that JSON decoding gives, such as []any for every list.
	eb, err := json.Marshal(extra)
	if err != nil {
		return nil, err
	}
	var plain config.SessionValues
	if err := json.Unmarshal(eb, &plain); err != nil {
		return nil, err
	}
	mergeExtra(merged, plain, "")
	return json.MarshalIndent(merged, "", "  ")
}

// mergeExtra adds the extra keys to sfy's own settings, and sfy's value wins where both set a key.
// Tables merge at every depth. The lists permissions.allow, permissions.deny and each hooks event get the extra entries after sfy's.
func mergeExtra(own, extra config.SessionValues, path string) {
	for k, v := range extra {
		key := k
		if path != "" {
			key = path + "." + k
		}
		cur, taken := own[k]
		if !taken {
			own[k] = v
			continue
		}
		ownList, ownIsList := cur.([]any)
		extraList, extraIsList := v.([]any)
		if ownIsList && extraIsList && appendsExtra(key) {
			own[k] = append(ownList, extraList...)
			continue
		}
		ownTable, ownIsTable := cur.(map[string]any)
		extraTable, extraIsTable := v.(map[string]any)
		if ownIsTable && extraIsTable {
			mergeExtra(ownTable, extraTable, key)
		}
	}
}

// appendsExtra reports whether the list at a settings key path takes extra entries after sfy's own.
func appendsExtra(key string) bool {
	if key == "permissions.allow" || key == "permissions.deny" {
		return true
	}
	event, ok := strings.CutPrefix(key, "hooks.")
	return ok && !strings.Contains(event, ".")
}

// sandboxFor turns on the sandbox for s, with no fallback to unsandboxed commands. The excluded patterns run outside it.
// A Linux sandbox ignores a write path that does not exist, so each one is created first.
func (a adapter) sandboxFor(s *settings, spec engine.SessionSpec, excluded []string) error {
	allowWrite, denyRead := own(spec.AllowWrite, a.cfg.AllowWrite), own(spec.DenyRead, a.cfg.DenyRead)
	for _, p := range allowWrite {
		dir, err := expandHome(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("claude: %w", err)
		}
	}
	deny, err := a.sessionFolders()
	if err != nil {
		return err
	}
	s.Sandbox = &sandbox{
		Enabled: true, FailIfUnavailable: true, AutoAllowBashIfSandboxed: true,
		Filesystem: filesystem{
			DenyRead:   append(deny, denyRead...),
			AllowWrite: append([]string{}, allowWrite...),
		},
	}
	// git push fails inside the macOS sandbox, and the sandbox blocks the sfy socket. The allow rules keep the excluded calls narrow.
	s.Sandbox.WeakerNetworkIsolation = a.goos == "darwin"
	if a.goos == "darwin" {
		// Go tests bind sockets in their temp folder. allowUnixSockets takes a folder as a prefix and stays narrower than allowAllUnixSockets.
		s.Sandbox.Network = &network{AllowUnixSockets: []string{
			fmt.Sprintf("/tmp/claude-%d", a.uid), fmt.Sprintf("/private/tmp/claude-%d", a.uid),
		}}
	}
	s.Sandbox.ExcludedCommands = append(append([]string{"git push *"}, sfyRules(spec.Kind)...), excluded...)
	s.Permissions.DefaultMode = "acceptEdits"
	s.Permissions.Allow = append(s.Permissions.Allow, "WebFetch(domain:*)")
	return nil
}

// sessionFolders lists the sessions folder of the session's own factory first, then that of every other factory on the machine.
// Each holds session tokens, and a token of another factory's lead would let a session send as that lead.
func (a adapter) sessionFolders() ([]string, error) {
	own := filepath.Join(a.home, "sessions")
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	out := []string{own}
	for _, f := range config.SessionFolders(userHome) {
		if filepath.Clean(f) != filepath.Clean(own) {
			out = append(out, f)
		}
	}
	return out, nil
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

// rewakeHook builds a command hook that runs in the background and wakes the agent when it exits with code 2.
func (a adapter) rewakeHook(event string) hook {
	h := a.hook(event)
	h.AsyncRewake = true
	return h
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
	case "lead":
		for _, c := range a.cfg.Allow {
			rules = append(rules, "Bash("+c+" *)")
		}
	case "worker":
		// A rule with no wildcard matches that command only, so no second branch, force flag or refspec passes.
		if !spec.NoPush && spec.Lease != "" {
			// An existing branch gets a lease on the sha seen at checkout. A bare lease would trust a fetched ref, so it stays denied.
			rules = append(rules, "Bash(git push -u origin "+spec.Branch+")",
				"Bash(git push --force-with-lease="+spec.Branch+":"+spec.Lease+" origin HEAD:"+spec.Branch+")")
		} else if !spec.NoPush {
			rules = append(rules, "Bash(git push -u origin "+spec.Branch+")", "Bash(git push --force-with-lease origin "+spec.Branch+")")
		}
	}
	rules = append(rules, a.cfg.SessionRules[rulesKey(spec)].Allow...)
	// Agents run sfy by its name on the PATH, and the hooks run it by its full path.
	// Hooks run from the settings and need no rule. Agents run sfy by its name on the PATH.
	if spec.Kind == "lead" {
		rules = append(rules, "Bash("+a.sfyBin+" *)", "Bash(sfy *)")
		rules = append(rules, a.leadReads()...)
	} else {
		for _, c := range sfyRules(spec.Kind) {
			rules = append(rules, "Bash("+c+")")
		}
		// The rule names one file, so a session reads its own task and no other. Bash can read it already, so no sandbox list changes.
		rules = append(rules, "Read("+rulePath(spec.TaskFile)+")")
	}
	return rules
}

// sfyRules lists the sfy commands that a worker or a reviewer may run, as command patterns.
// The same patterns run outside the sandbox, because the sandbox blocks the sfy socket.
// A wider pattern such as "sfy *" would let sfy run start any command outside the sandbox.
func sfyRules(kind string) []string {
	switch kind {
	case "worker":
		return []string{"sfy report *", "sfy inbox"}
	case "reviewer":
		return []string{"sfy verdict *", "sfy report blocked *", "sfy inbox"}
	}
	return nil
}

// leadReads lets the lead read the factory folder without a prompt and list folders. It gets no cat rule, because a rule matches the command text and cannot limit a path.
func (a adapter) leadReads() []string {
	return []string{"Read(" + rulePath(a.home) + "/**)", "Bash(ls *)"}
}

// pushWithGitOptions denies a push that starts with a git option, as in git -C . push or git -c a=b push.
// Such a call does not match the excluded git push entry, so it would stay sandboxed and auto-approved.
// A deny rule beats the auto-approval. It cannot match the allowed exact pushes, which have no option before push.
const pushWithGitOptions = "Bash(git -* push*)"

// pushAny denies every git push. A worker of a held task gets it and has no push allow rule.
const pushAny = "Bash(git push*)"

// deny returns the deny rules of a session. The lead gets none, and its merge guard hook limits no-merge repos.
func deny(spec engine.SessionSpec) []string {
	rules := []string{}
	switch spec.Kind {
	case "reviewer":
		// A reviewer agent of the process whose permissions keep edit may edit, and any other reviewer may not.
		if spec.Agent != "" && !spec.ReadOnly {
			return []string{"AskUserQuestion", pushWithGitOptions}
		}
		return []string{"AskUserQuestion", "Edit", "Write", "NotebookEdit", pushWithGitOptions}
	case "worker":
		rules = append(rules, "AskUserQuestion", pushWithGitOptions)
		if spec.NoPush {
			rules = append(rules, pushAny)
		}
		if spec.ReadOnly {
			rules = append(rules, "Edit", "Write", "NotebookEdit")
		}
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

// claudeCommand returns the path of claude when it lies off PATH, and the plain name otherwise.
func (a adapter) claudeCommand() string {
	if _, err := a.sys.lookPath("claude"); err == nil {
		return "claude"
	}
	if p, ok := a.claudePath(); ok {
		return p
	}
	return "claude"
}
