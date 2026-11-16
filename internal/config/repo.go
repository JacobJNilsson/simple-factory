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

// repoSettings lists the settings that a .sfy.toml file may hold. A nil field means the file leaves the setting out.
type repoSettings struct {
	NoMerge *bool `toml:"no_merge"`
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

// ForRepo applies the .sfy.toml files of a repo folder on top of base.
// It reads the file in folder and in each parent folder up to userHome. The nearest file wins, key by key.
// A folder outside userHome counts alone, with no parent folder.
func ForRepo(base Config, userHome, folder string) (Config, error) {
	folders := repoFolders(userHome, folder)
	merged := repoSettings{}
	// The folders run from the nearest to the farthest, so a setting that is already set stays.
	for _, dir := range folders {
		file := filepath.Join(dir, RepoFile)
		var s repoSettings
		md, err := toml.DecodeFile(file, &s)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Config{}, fmt.Errorf("config: %s: %w", file, err)
		}
		if keys := md.Undecoded(); len(keys) > 0 {
			return Config{}, &RepoFileError{File: file, Setting: keys[0].String(), GlobalOnly: slices.Contains(globalOnly, keys[0][0])}
		}
		if merged.NoMerge == nil {
			merged.NoMerge = s.NoMerge
		}
	}
	if merged.NoMerge != nil {
		base.NoMerge = *merged.NoMerge
	}
	return base, nil
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
