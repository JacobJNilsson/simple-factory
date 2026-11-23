package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// RepoFile is the name of the per-repo config file.
const RepoFile = ".sfy.toml"

// RepoConfigFile names the config file in a per-repo override. The override folder is OverrideDir.
const RepoConfigFile = "config.toml"

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
	"review_rounds", "max_workers", "resend_minutes", "note_fallback_after", "check_timeout", "provider", "multiplexer", "models", "allow",
	"deny_read", "allow_write", "pass_env", "multiplexers", "session_settings", "session_rules",
}

// RepoFileError reports a .sfy.toml setting that sfy refuses. File names the file.
type RepoFileError struct {
	File    string
	Setting string
	// GlobalOnly is true when the setting is valid, but only in the config.toml of ~/.sfy or of the factory folder.
	GlobalOnly bool
}

func (e *RepoFileError) Error() string {
	if e.GlobalOnly {
		return fmt.Sprintf("config: %s: %s is a global setting, so set it in config.toml in ~/.sfy or in the factory folder", e.File, e.Setting)
	}
	return fmt.Sprintf("config: %s: unknown setting %s", e.File, e.Setting)
}

// OverrideError reports a per-repo override that sfy does not trust, because the user does not own it or group or others can write it.
// A skipped per-repo override could drop a no_merge setting without notice, so ForRepo fails instead.
type OverrideError struct{ Dir, Reason string }

func (e *OverrideError) Error() string {
	return fmt.Sprintf("config: the per-repo override %s is not safe to read: %s", e.Dir, e.Reason)
}

// ForRepo applies the per-repo files of a repo folder on top of base.
// base holds the settings of the factory's layers, so the overrides on the walk of workFolder already reach it.
// The files .sfy/config.toml in folder and in each parent folder up to userHome come first, nearest first.
// The parent folders stop before the first folder on the walk of workFolder. An override that sfy does not trust fails with an OverrideError.
// Then come the .sfy.toml files in folder and in each parent folder up to userHome, nearest first.
// A folder outside userHome counts alone, with no parent folder. Each setting comes from the first file that sets it.
func ForRepo(base Config, userHome, workFolder, folder string) (Config, error) {
	return forRepo(base, userHome, workFolder, folder, os.Geteuid())
}

func forRepo(base Config, userHome, workFolder, folder string, uid int) (Config, error) {
	folders := repoFolders(userHome, folder)
	files, err := repoOverrideFiles(folders, walkFolders(workFolder, userHome), uid)
	if err != nil {
		return Config{}, err
	}
	for _, dir := range folders {
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

// repoOverrideFiles returns the config.toml of the .sfy folder in each of folders, nearest first, up to the first folder of the walk.
// A folder without a .sfy folder adds nothing, and a .sfy folder that sfy does not trust is an OverrideError.
func repoOverrideFiles(folders, walk []string, uid int) ([]string, error) {
	onWalk := map[string]bool{}
	for _, dir := range walk {
		onWalk[CanonicalPath(dir)] = true
	}
	var files []string
	for _, dir := range folders {
		if onWalk[CanonicalPath(dir)] {
			break
		}
		override := filepath.Join(dir, OverrideDir)
		st, err := os.Stat(override)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && !st.IsDir()) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		if reason := skipReason(st, uid); reason != "" {
			return nil, &OverrideError{Dir: override, Reason: reason}
		}
		files = append(files, filepath.Join(override, RepoConfigFile))
	}
	return files, nil
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
