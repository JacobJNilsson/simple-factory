package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// OverrideDir is the name of a folder override: a folder with the shape of a factory definition.
const OverrideDir = ".sfy"

// LocalLayer is the folder in a factory folder that holds the factory's nearest layer.
// It lets a program set the settings of one factory over the machine's settings without a write into the working folder.
const LocalLayer = "local"

// Override is a .sfy folder that the walk of a factory found, or the local layer of the factory folder. Skip is empty for an override that sfy reads, and holds the reason for one that it skips.
type Override struct {
	Dir  string
	Skip string
}

// Overrides returns the folder overrides of the factory with the factory folder home, farthest first.
// The home folder's .sfy, which is ~/.sfy, comes first. Then come the .sfy folders from the home folder down to the working folder that home records.
// A working folder outside the home folder adds its own .sfy and no parent. A factory with no working folder has ~/.sfy only.
// The local layer of home comes last. A missing folder adds nothing.
// An override that the user does not own or that group or others can write comes back with Skip set.
func Overrides(home, userHome string) ([]Override, error) {
	return overrides(home, userHome, os.Geteuid())
}

// overrides is Overrides for a user id, so a test can name an owner that it cannot create.
func overrides(home, userHome string, uid int) ([]Override, error) {
	var dirs []string
	for _, folder := range walkFolders(WorkFolder(home), userHome) {
		dirs = append(dirs, filepath.Join(folder, OverrideDir))
	}
	dirs = append(dirs, filepath.Join(home, LocalLayer))
	var out []Override
	for _, dir := range dirs {
		st, err := os.Stat(dir)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && !st.IsDir()) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		out = append(out, Override{Dir: dir, Skip: skipReason(st, uid)})
	}
	return out, nil
}

// walkFolders returns the folders whose .sfy folder counts as an override, farthest first.
// The user's home folder comes first. A working folder inside it adds each folder down to the working folder.
// A working folder outside it adds itself only, and an empty working folder adds nothing.
func walkFolders(work, userHome string) []string {
	userHome = filepath.Clean(userHome)
	folders := []string{userHome}
	if work == "" {
		return folders
	}
	work = filepath.Clean(work)
	rel, err := filepath.Rel(userHome, work)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return append(folders, work)
	}
	if rel == "." {
		return folders
	}
	dir := userHome
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		dir = filepath.Join(dir, part)
		folders = append(folders, dir)
	}
	return folders
}

// skipReason returns why sfy skips the override folder with the file info st, or an empty string when sfy reads it.
func skipReason(st fs.FileInfo, uid int) string {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != uid {
		return fmt.Sprintf("owned by uid %d, and sfy runs as uid %d", sys.Uid, uid)
	}
	if st.Mode().Perm()&0o022 != 0 {
		return fmt.Sprintf("mode %04o lets group or others write", st.Mode().Perm())
	}
	return ""
}

// Skipped returns the overrides of the factory that sfy skips, with the reason in Skip.
// The factory folder is never an override of its own, so the default factory has none.
func Skipped(home, userHome string) ([]Override, error) {
	found, err := Overrides(home, userHome)
	if err != nil {
		return nil, err
	}
	var out []Override
	for _, o := range found {
		if o.Skip != "" && CanonicalPath(o.Dir) != CanonicalPath(home) {
			out = append(out, o)
		}
	}
	return out, nil
}

// Layers returns the folders that hold a factory's definition, lowest first:
// the factory folder, then the overrides that sfy reads, farthest first, and the local layer last. The built-in defaults sit below the list and are not folders.
// Each folder comes once, so the default factory, whose folder is ~/.sfy, gets one folder.
func Layers(home, userHome string) ([]string, error) {
	found, err := Overrides(home, userHome)
	if err != nil {
		return nil, err
	}
	layers := []string{filepath.Clean(home)}
	seen := map[string]bool{CanonicalPath(home): true}
	for _, o := range found {
		key := CanonicalPath(o.Dir)
		if o.Skip != "" || seen[key] {
			continue
		}
		seen[key] = true
		layers = append(layers, o.Dir)
	}
	return layers, nil
}
