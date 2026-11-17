package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// RepoFile is the name of the per-repo config file.
const RepoFile = ".sfy.toml"

// RepoDir and RepoConfigFile name the settings folder and file at the root of a repo folder.
const (
	RepoDir        = ".sfy"
	RepoConfigFile = "config.toml"
)

// repoSettings lists the settings that a per-repo file may hold. A nil field means the file leaves the setting out.
// A new setting needs a field here, a line in fill and a line in apply.
type repoSettings struct {
	NoMerge *bool `toml:"no_merge"`
	DraftPR *bool `toml:"draft_pr"`
}

// fill takes each setting that s leaves out from lower. Call it with the files in order of precedence.
func (s *repoSettings) fill(lower repoSettings) {
	if s.NoMerge == nil {
		s.NoMerge = lower.NoMerge
	}
	if s.DraftPR == nil {
		s.DraftPR = lower.DraftPR
	}
}

// apply sets each setting that s holds on c.
func (s repoSettings) apply(c *Config) {
	if s.NoMerge != nil {
		c.NoMerge = *s.NoMerge
	}
	if s.DraftPR != nil {
		c.DraftPR = *s.DraftPR
	}
}

// globalOnly lists the top-level settings that only ~/.sfy/config.toml accepts.
var globalOnly = []string{
	"review_rounds", "max_workers", "resend_minutes", "provider", "multiplexer", "models", "allow",
	"deny_read", "allow_write", "pass_env", "multiplexers", "session_settings", "session_rules",
}

// RepoFileError reports a .sfy.toml setting that sfy refuses. File names the file.
type RepoFileError struct {
	File    string
	Setting string
	// GlobalOnly is true when the setting is valid, but only in ~/.sfy/config.toml.
	GlobalOnly bool
}

func (e *RepoFileError) Error() string {
	if e.GlobalOnly {
		return fmt.Sprintf("config: %s: %s is a global setting, so set it in config.toml in the sfy home folder", e.File, e.Setting)
	}
	return fmt.Sprintf("config: %s: unknown setting %s", e.File, e.Setting)
}

// ForRepo applies the per-repo files of a repo folder on top of base.
// The file .sfy/config.toml in folder wins. Then come the .sfy.toml files in folder and in each parent folder up to userHome, nearest first.
// A folder outside userHome counts alone, with no parent folder. Each setting comes from the first file that sets it.
func ForRepo(base Config, userHome, folder string) (Config, error) {
	files := []string{filepath.Join(folder, RepoDir, RepoConfigFile)}
	for _, dir := range repoFolders(userHome, folder) {
		files = append(files, filepath.Join(dir, RepoFile))
	}
	merged := repoSettings{}
	for _, file := range files {
		s, err := readRepoFile(file)
		if err != nil {
			return Config{}, err
		}
		merged.fill(s)
	}
	merged.apply(&base)
	return base, nil
}

// readRepoFile decodes one per-repo file. A missing file gives empty settings.
func readRepoFile(file string) (repoSettings, error) {
	var s repoSettings
	md, err := toml.DecodeFile(file, &s)
	if errors.Is(err, fs.ErrNotExist) {
		return repoSettings{}, nil
	}
	if err != nil {
		return repoSettings{}, fmt.Errorf("config: %s: %w", file, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return repoSettings{}, &RepoFileError{File: file, Setting: keys[0].String(), GlobalOnly: slices.Contains(globalOnly, keys[0][0])}
	}
	return s, nil
}

// repoFolders lists folder and its parents up to userHome, nearest first.
func repoFolders(userHome, folder string) []string {
	folder, userHome = filepath.Clean(folder), filepath.Clean(userHome)
	if rel, err := filepath.Rel(userHome, folder); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []string{folder}
	}
	var out []string
	for dir := folder; ; dir = filepath.Dir(dir) {
		out = append(out, dir)
		if dir == userHome || dir == filepath.Dir(dir) {
			return out
		}
	}
}
