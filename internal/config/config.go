// Package config loads config.toml from the sfy home folder.
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
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Models names the model of each kind of agent.
type Models struct {
	Main     string `toml:"main"`
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
	Provider          string        `toml:"provider"`
	Multiplexer       string        `toml:"multiplexer"`
	Models            Models        `toml:"models"`
	Allow             []string      `toml:"allow"`
	// NoMerge stops the main agent from integrating approved branches. The operator merges them by hand.
	// A .sfy.toml file sets it for one repo or for the repos below one folder.
	NoMerge bool `toml:"no_merge"`
	// DraftPR makes the push note tell the worker to open any PR as a draft. sfy opens no PR itself.
	DraftPR bool `toml:"draft_pr"`
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

// SessionSettingsError reports a session_settings table that sfy refuses.
// Key is empty when the kind itself is unknown.
type SessionSettingsError struct {
	Kind string
	Key  string
}

func (e *SessionSettingsError) Error() string {
	if e.Key == "" {
		return fmt.Sprintf("config: unknown session kind %q", e.Kind)
	}
	return fmt.Sprintf("config: session_settings.%s.%s is reserved, because sfy writes %s itself", e.Kind, e.Key, e.Key)
}

// sessionKinds lists the kinds that session_settings accepts.
var sessionKinds = []string{"main", "worker", "reviewer"}

// reservedSessionKeys are the settings keys that sfy writes itself.
var reservedSessionKeys = []string{"permissions", "hooks", "sandbox"}

// validateSessionSettings refuses an unknown kind and a key that sfy owns.
func validateSessionSettings(tables map[string]SessionValues) error {
	for _, kind := range slices.Sorted(maps.Keys(tables)) {
		if !slices.Contains(sessionKinds, kind) {
			return &SessionSettingsError{Kind: kind}
		}
		for _, key := range slices.Sorted(maps.Keys(tables[kind])) {
			if slices.Contains(reservedSessionKeys, key) {
				return &SessionSettingsError{Kind: kind, Key: key}
			}
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
		Provider:          "claude",
		Multiplexer:       "tmux",
		Models:            Models{Main: "opus", Worker: "sonnet", Reviewer: "sonnet"},
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

// LoadFactory reads the operator's config.toml in the default state folder, then the factory's own config.toml on top of it.
// Either file may be missing. A setting that the factory's file leaves out keeps the value of the operator's file.
func LoadFactory(home, userHome string) (Config, error) {
	paths := []string{filepath.Join(DefaultHome(userHome), "config.toml")}
	if own := filepath.Join(home, "config.toml"); own != paths[0] {
		paths = append(paths, own)
	}
	return load(getconfTempDir, paths...)
}

func load(userTempDir func() (string, error), paths ...string) (Config, error) {
	c := defaults(userTempDir)
	for _, path := range paths {
		md, err := toml.DecodeFile(path, &c)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Config{}, fmt.Errorf("config: %w", err)
		}
		if key, ok := firstUnknown(md); ok {
			return Config{}, &SettingError{Setting: key, Unknown: true}
		}
	}
	if c.ReviewRounds < 1 {
		return Config{}, &SettingError{Setting: "review_rounds"}
	}
	if c.MaxWorkers < 1 {
		return Config{}, &SettingError{Setting: "max_workers"}
	}
	if c.ResendMinutes < 1 {
		return Config{}, &SettingError{Setting: "resend_minutes"}
	}
	if c.NoteFallbackAfter <= 0 {
		return Config{}, &SettingError{Setting: "note_fallback_after", Duration: true}
	}
	if err := validateSessionSettings(c.SessionSettings); err != nil {
		return Config{}, err
	}
	for _, kind := range slices.Sorted(maps.Keys(c.SessionRules)) {
		if !slices.Contains(sessionKinds, kind) {
			return Config{}, &SessionSettingsError{Kind: kind}
		}
	}
	return c, nil
}

// firstUnknown returns the first key that no field took.
// It skips the parts inside a session_settings value, because those belong to the provider.
func firstUnknown(md toml.MetaData) (string, bool) {
	for _, key := range md.Undecoded() {
		if len(key) > 3 && key[0] == "session_settings" {
			continue
		}
		return key.String(), true
	}
	return "", false
}

// Home returns the state folder.
// SFY_HOME wins. Otherwise the nearest registered factory folder at or above the current folder wins.
// Without either, the default factory, .sfy in the user's home folder, is the state folder.
func Home(env func(string) string) string {
	if h := env("SFY_HOME"); h != "" {
		return h
	}
	if cwd, err := os.Getwd(); err == nil {
		if state, ok := FindFactory(env("HOME"), cwd); ok {
			return state
		}
	}
	return DefaultHome(env("HOME"))
}

// Prefix returns the text that goes in front of every terminal session name and worker branch of a state folder.
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
