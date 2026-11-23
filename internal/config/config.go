// Package config loads config.d/*.toml and config.toml from the machine layer and the factory folder.
package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/JacobJNilsson/simple-factory/internal/process"
)

// Models names the model of each kind of agent.
type Models struct {
	Lead     string `toml:"lead"`
	Worker   string `toml:"worker"`
	Reviewer string `toml:"reviewer"`
}

// Config holds the settings from the spec's configuration table.
type Config struct {
	ReviewRounds  int `toml:"review_rounds"`
	MaxWorkers    int `toml:"max_workers"`
	ResendMinutes int `toml:"resend_minutes"`
	// NoteFallbackAfter is how long a queued note may stay unread before sfy types it into an idle pane.
	NoteFallbackAfter time.Duration `toml:"note_fallback_after"`
	// CheckTimeout is how long a task's check command may run before sfy stops it and counts it as failed.
	CheckTimeout time.Duration `toml:"check_timeout"`
	Provider     string        `toml:"provider"`
	Multiplexer  string        `toml:"multiplexer"`
	Models       Models        `toml:"models"`
	Allow        []string      `toml:"allow"`
	// NoMerge stops the lead from integrating approved branches. The operator merges them by hand.
	// A .sfy.toml file sets it for one repo or for the repos below one folder.
	NoMerge bool `toml:"no_merge"`
	// DraftPR makes the push note tell the worker to open any PR as a draft. sfy opens no PR itself.
	DraftPR bool `toml:"draft_pr"`
	// Headless runs the sfy process detached, with its log in sfy.log and no terminal window.
	Headless bool `toml:"headless"`
	// DenyRead and AllowWrite are the sandbox paths for workers and reviewers. An empty list stays empty.
	DenyRead   []string `toml:"deny_read"`
	AllowWrite []string `toml:"allow_write"`
	// PassEnv names the variables that workers and reviewers keep from sfy's own environment.
	PassEnv []string `toml:"pass_env"`
	// Multiplexers holds the raw template sets by name. A later package validates and fills them.
	Multiplexers map[string]map[string]string `toml:"multiplexers"`
	// SessionSettings holds extra top-level keys for each session kind's provider settings file.
	// The keys are the provider's own, so sfy copies the values without a check.
	SessionSettings map[string]SessionValues `toml:"session_settings"`
	// SessionRules holds extra permission rules for each session kind.
	SessionRules map[string]SessionRule `toml:"session_rules"`
	// agentModels holds the models keys that name a claude agent other than the worker and the reviewer. It is nil when there are none.
	agentModels map[string]string
}

// SessionRule holds the extra rules of one session kind.
type SessionRule struct {
	// Allow lists permission rules that sfy adds to the kind's allow rules, for example Bash(git fetch *).
	Allow []string `toml:"allow"`
	// ExcludedCommands lists command patterns that a sandboxed session runs outside the sandbox.
	ExcludedCommands []string `toml:"excluded_commands"`
}

// SessionValues holds the extra keys of one session kind. Each value is any TOML value.
type SessionValues map[string]any

// SessionSettingsError reports a session_settings or session_rules table of a kind that sfy does not know.
type SessionSettingsError struct {
	Kind string
}

func (e *SessionSettingsError) Error() string {
	return fmt.Sprintf("config: unknown session kind %q", e.Kind)
}

// sessionKinds lists the kinds that session_settings and session_rules accept: the lead and each claude agent of the process.
func sessionKinds(p process.Process) []string {
	kinds := []string{"lead"}
	for _, name := range slices.Sorted(maps.Keys(p.Agents)) {
		if p.Agents[name].Harness == process.Claude {
			kinds = append(kinds, name)
		}
	}
	return kinds
}

// withAgentRules adds the allow and excluded_commands permissions of each claude agent to the session rules of its name.
// The session rules and the agent's permissions then form one list, so a file with either keeps its meaning.
func withAgentRules(rules map[string]SessionRule, p process.Process) map[string]SessionRule {
	for _, name := range slices.Sorted(maps.Keys(p.Agents)) {
		perm := p.Agents[name].Permissions
		if len(perm.Allow) == 0 && len(perm.ExcludedCommands) == 0 {
			continue
		}
		if rules == nil {
			rules = map[string]SessionRule{}
		}
		r := rules[name]
		r.Allow = append(slices.Clone(r.Allow), perm.Allow...)
		r.ExcludedCommands = append(slices.Clone(r.ExcludedCommands), perm.ExcludedCommands...)
		rules[name] = r
	}
	return rules
}

// validateSessionSettings refuses an unknown kind.
// A key that sfy writes itself, such as permissions, is fine, because the provider merges it with sfy's own value.
func validateSessionSettings(tables map[string]SessionValues, kinds []string) error {
	for _, kind := range slices.Sorted(maps.Keys(tables)) {
		if !slices.Contains(kinds, kind) {
			return &SessionSettingsError{Kind: kind}
		}
	}
	return nil
}

// SettingError reports a setting with a value that sfy cannot use.
type SettingError struct {
	Setting string
	// Unknown is true when sfy does not know the setting, and false when its value is too low.
	Unknown bool
	// Duration is true when the value must be a positive duration.
	Duration bool
}

// DefaultNoteFallbackAfter is the default of note_fallback_after.
// It covers a long tool call, because the hook delivers a note only between tool calls or at the end of the turn.
const DefaultNoteFallbackAfter = 5 * time.Minute

// DefaultCheckTimeout is the default of check_timeout.
const DefaultCheckTimeout = 20 * time.Minute

func (e *SettingError) Error() string {
	if e.Unknown {
		return fmt.Sprintf("config: unknown setting %s", e.Setting)
	}
	if e.Duration {
		return fmt.Sprintf("config: %s must be a positive duration, such as 5m", e.Setting)
	}
	return fmt.Sprintf("config: %s must be 1 or more", e.Setting)
}

func defaults(userTempDir func() (string, error)) Config {
	return Config{
		ReviewRounds:      3,
		MaxWorkers:        2,
		ResendMinutes:     5,
		NoteFallbackAfter: DefaultNoteFallbackAfter,
		CheckTimeout:      DefaultCheckTimeout,
		Provider:          "claude",
		Multiplexer:       "tmux",
		Models:            Models{Lead: "opus", Worker: "sonnet", Reviewer: "sonnet"},
		Allow:             []string{"git", "make", "go", "gofmt"},
		DenyRead: []string{
			"~/.ssh", "~/.aws", "~/.gnupg",
			"~/.netrc", "~/.docker", "~/.kube", "~/.claude.json",
		},
		AllowWrite: defaultAllowWrite(runtime.GOOS, userTempDir),
	}
}

// getconfTempDir looks up the macOS per-user temp folder.
func getconfTempDir() (string, error) {
	out, err := exec.CommandContext(context.Background(), "getconf", "DARWIN_USER_TEMP_DIR").Output()
	return string(out), err
}

// defaultAllowWrite lists the default sandbox write paths for an operating system.
// On macOS, mktemp and other tools write to the per-user temp folder, not to TMPDIR.
func defaultAllowWrite(goos string, userTempDir func() (string, error)) []string {
	paths := []string{cacheDir(goos), "~/go/pkg/mod"}
	if goos != "darwin" {
		return paths
	}
	dir, err := userTempDir()
	dir = strings.TrimRight(strings.TrimSpace(dir), "/")
	if err != nil || dir == "" {
		return paths
	}
	return append(paths, dir)
}

// cacheDir returns the user cache folder for an operating system, with ~ for the home folder.
func cacheDir(goos string) string {
	if goos == "darwin" {
		return "~/Library/Caches"
	}
	return "~/.cache"
}

// Load reads <home>/config.toml. A missing file gives the defaults, and a setting left out keeps its default.
func Load(home string) (Config, error) {
	return load(getconfTempDir, filepath.Join(home, "config.toml"))
}

// configFiles returns the config files of each layer, lowest first: the .toml files of config.d in name order, then config.toml.
func configFiles(home, userHome string) ([]string, error) {
	layers, err := Layers(home, userHome)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, dir := range layers {
		parts, err := configParts(filepath.Join(dir, "config.d"))
		if err != nil {
			return nil, err
		}
		paths = append(append(paths, parts...), filepath.Join(dir, "config.toml"))
	}
	return paths, nil
}

// configParts returns the .toml files of the folder dir in name order, with links followed.
// A missing folder gives none, and a file without .toml or a folder is skipped.
func configParts(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	var paths []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".toml" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := os.Stat(path)
		if err != nil {
			return nil, &FileError{Path: path, Err: err}
		}
		if !info.IsDir() {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// LoadFactory merges the config files of each folder from Layers onto the defaults, as configFiles orders them.
// Any file may be missing. A setting that a later file leaves out keeps the value of an earlier file.
func LoadFactory(home, userHome string) (Config, error) {
	paths, err := configFiles(home, userHome)
	if err != nil {
		return Config{}, err
	}
	return load(getconfTempDir, paths...)
}

// LoadProcess returns the settings of LoadFactory and the factory's task process, which the agents, lists and start keys change.
func LoadProcess(home, userHome string) (Config, process.Process, error) {
	paths, err := configFiles(home, userHome)
	if err != nil {
		return Config{}, process.Process{}, err
	}
	return loadWithProcess(getconfTempDir, paths...)
}

// load is loadWithProcess for a caller that needs only the settings.
func load(userTempDir func() (string, error), paths ...string) (Config, error) {
	c, _, err := loadWithProcess(userTempDir, paths...)
	return c, err
}

// loadWithProcess merges the files with mergeTables, decodes the result onto the defaults once, and then checks the values once.
func loadWithProcess(userTempDir func() (string, error), paths ...string) (Config, process.Process, error) {
	var none process.Process
	merged := rawTable{}
	for _, path := range paths {
		raw, err := readTable(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Config{}, none, err
		}
		merged = mergeTables(merged, raw)
	}
	merged, agentModels := splitAgentModels(merged)
	c := defaults(userTempDir)
	var text strings.Builder
	if err := toml.NewEncoder(&text).Encode(merged); err != nil {
		return Config{}, none, fmt.Errorf("config: %w", err)
	}
	md, err := toml.Decode(text.String(), &c)
	if err != nil {
		return Config{}, none, fmt.Errorf("config: %w", err)
	}
	if key, ok := firstUnknown(md); ok {
		return Config{}, none, &SettingError{Setting: key, Unknown: true}
	}
	if err := checkValues(c); err != nil {
		return Config{}, none, err
	}
	p, err := process.Parse(c.Provider, text.String())
	if err != nil {
		return Config{}, none, err
	}
	if err := p.Validate(nil); err != nil {
		return Config{}, none, err
	}
	kinds := sessionKinds(p)
	if err := validateSessionSettings(c.SessionSettings, kinds); err != nil {
		return Config{}, none, err
	}
	for _, kind := range slices.Sorted(maps.Keys(c.SessionRules)) {
		if !slices.Contains(kinds, kind) {
			return Config{}, none, &SessionSettingsError{Kind: kind}
		}
	}
	c.SessionRules = withAgentRules(c.SessionRules, p)
	for _, name := range slices.Sorted(maps.Keys(agentModels)) {
		if !slices.Contains(kinds, name) {
			return Config{}, none, &SettingError{Setting: "models." + name, Unknown: true}
		}
	}
	c.agentModels = agentModels
	return c, p, nil
}

// splitAgentModels takes each models key that names another agent than the lead, the worker and the reviewer out of the merged table.
// Such a key holds the model of the claude agent of its name. The models table keeps the three fixed keys.
func splitAgentModels(merged rawTable) (rawTable, map[string]string) {
	models, ok := merged["models"].(map[string]any)
	if !ok {
		return merged, nil
	}
	var agents map[string]string
	kept := map[string]any{}
	for k, v := range models {
		model, isText := v.(string)
		if k == "lead" || k == "worker" || k == "reviewer" || !isText {
			kept[k] = v
			continue
		}
		if agents == nil {
			agents = map[string]string{}
		}
		agents[k] = model
	}
	out := maps.Clone(merged)
	out["models"] = kept
	return out, agents
}

// ModelOf returns the model of the named claude agent: its models key, then the worker's or the reviewer's model.
// A verdict agent falls back to the reviewer's model, and every other agent to the worker's model.
func (c Config) ModelOf(agent string, verdict bool) string {
	switch {
	case c.agentModels[agent] != "":
		return c.agentModels[agent]
	case agent == "reviewer" || (verdict && agent != "worker"):
		return c.Models.Reviewer
	}
	return c.Models.Worker
}

// checkValues refuses a number or a duration that is too low.
func checkValues(c Config) error {
	switch {
	case c.ReviewRounds < 1:
		return &SettingError{Setting: "review_rounds"}
	case c.MaxWorkers < 1:
		return &SettingError{Setting: "max_workers"}
	case c.ResendMinutes < 1:
		return &SettingError{Setting: "resend_minutes"}
	case c.NoteFallbackAfter <= 0:
		return &SettingError{Setting: "note_fallback_after", Duration: true}
	case c.CheckTimeout <= 0:
		return &SettingError{Setting: "check_timeout", Duration: true}
	}
	return nil
}

// firstUnknown returns the first key that no field took.
// It skips the parts inside a session_settings value, because those belong to the provider.
func firstUnknown(md toml.MetaData) (string, bool) {
	for _, key := range md.Undecoded() {
		if len(key) > 3 && key[0] == "session_settings" {
			continue
		}
		// The process package decodes and checks these keys itself.
		if key[0] == "agents" || key[0] == "lists" || key[0] == "start" {
			continue
		}
		return key.String(), true
	}
	return "", false
}

// NamePrefixEnv is the variable that scopes the factory lookup by a name prefix.
const NamePrefixEnv = "SFY_NAME_PREFIX"

// maxNamePrefix is the longest name prefix, so a factory name keeps room for the working folder's name.
const maxNamePrefix = 16

// namePrefixForm matches a non-empty name prefix: lowercase letters, digits and -, ending in -.
var namePrefixForm = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*-$`)

// NamePrefix returns the name prefix of the environment, or an empty string when SFY_NAME_PREFIX is unset or empty.
// A value that is too long or has other characters than lowercase letters, digits and -, or does not end in -, fails and names the variable.
func NamePrefix(env func(string) string) (string, error) {
	prefix := env(NamePrefixEnv)
	if prefix == "" {
		return "", nil
	}
	if len(prefix) > maxNamePrefix || !namePrefixForm.MatchString(prefix) {
		return "", &NamePrefixError{Value: prefix}
	}
	return prefix, nil
}

// NamePrefixError reports a SFY_NAME_PREFIX value of the wrong form.
type NamePrefixError struct{ Value string }

func (e *NamePrefixError) Error() string {
	return fmt.Sprintf("%s must hold lowercase letters, digits and -, end in -, and have at most %d characters, but it is %q", NamePrefixEnv, maxNamePrefix, e.Value)
}

// Lookup returns the factory folder of the environment.
// SFY_HOME wins. Otherwise the factory of the nearest registered working folder at or above the current folder wins,
// among the factory folders with the name prefix of SFY_NAME_PREFIX.
// Without any, the default factory folder, .sfy in the user's home folder, wins. A name prefix has no default factory, so it fails there.
func Lookup(env func(string) string) (string, error) {
	if h := env("SFY_HOME"); h != "" {
		return h, nil
	}
	prefix, err := NamePrefix(env)
	if err != nil {
		return "", err
	}
	if cwd, err := os.Getwd(); err == nil {
		if state, ok := FindFactory(env("HOME"), cwd, prefix); ok {
			return state, nil
		}
	}
	if prefix != "" {
		return "", &NoFactoryError{Prefix: prefix}
	}
	return DefaultHome(env("HOME")), nil
}

// NoFactoryError reports a lookup with a name prefix that finds no factory folder with that name prefix.
type NoFactoryError struct{ Prefix string }

func (e *NoFactoryError) Error() string {
	return fmt.Sprintf("no factory with the name prefix %s here", e.Prefix)
}

// Home returns the factory folder that Lookup finds. A caller that must report a failed lookup runs Lookup first.
// When Lookup fails, Home returns the default factory folder.
func Home(env func(string) string) string {
	home, err := Lookup(env)
	if err != nil {
		return DefaultHome(env("HOME"))
	}
	return home
}

// Prefix returns the text that goes in front of every terminal session name and worker branch of a factory folder.
// The default folder, .sfy in the user's home folder, has an empty prefix.
// Any other folder gets its base name without a leading dot, in lower case, with each character outside a-z0-9- replaced by -, and a final -.
func Prefix(home string, env func(string) string) string {
	home = filepath.Clean(home)
	if home == filepath.Join(env("HOME"), ".sfy") {
		return ""
	}
	name := strings.ToLower(strings.TrimPrefix(filepath.Base(home), "."))
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, name) + "-"
}
