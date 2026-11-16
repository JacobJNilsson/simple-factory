// Package config loads config.toml from the sfy home folder.
package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

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
	ReviewRounds  int      `toml:"review_rounds"`
	MaxWorkers    int      `toml:"max_workers"`
	ResendMinutes int      `toml:"resend_minutes"`
	Provider      string   `toml:"provider"`
	Multiplexer   string   `toml:"multiplexer"`
	Models        Models   `toml:"models"`
	Allow         []string `toml:"allow"`
	// DenyRead and AllowWrite are the sandbox paths for workers and reviewers. An empty list stays empty.
	DenyRead   []string `toml:"deny_read"`
	AllowWrite []string `toml:"allow_write"`
	// PassEnv names the variables that workers and reviewers keep from sfy's own environment.
	PassEnv []string `toml:"pass_env"`
	// Multiplexers holds the raw template sets by name. A later package validates and fills them.
	Multiplexers map[string]map[string]string `toml:"multiplexers"`
}

// SettingError reports a setting with a value that sfy cannot use.
type SettingError struct {
	Setting string
	// Unknown is true when sfy does not know the setting, and false when its value is too low.
	Unknown bool
}

func (e *SettingError) Error() string {
	if e.Unknown {
		return fmt.Sprintf("config: unknown setting %s", e.Setting)
	}
	return fmt.Sprintf("config: %s must be 1 or more", e.Setting)
}

func defaults(userTempDir func() (string, error)) Config {
	return Config{
		ReviewRounds:  3,
		MaxWorkers:    2,
		ResendMinutes: 5,
		Provider:      "claude",
		Multiplexer:   "tmux",
		Models:        Models{Main: "opus", Worker: "sonnet", Reviewer: "sonnet"},
		Allow:         []string{"git", "gh", "make", "go", "gofmt"},
		DenyRead: []string{
			"~/.ssh", "~/.aws", "~/.config/gh", "~/.gnupg",
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
	return load(home, getconfTempDir)
}

func load(home string, userTempDir func() (string, error)) (Config, error) {
	c := defaults(userTempDir)
	md, err := toml.DecodeFile(filepath.Join(home, "config.toml"), &c)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return Config{}, &SettingError{Setting: keys[0].String(), Unknown: true}
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
	return c, nil
}

// Home returns the state folder: SFY_HOME, or .sfy in the user's home folder.
func Home(env func(string) string) string {
	if h := env("SFY_HOME"); h != "" {
		return h
	}
	return filepath.Join(env("HOME"), ".sfy")
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
