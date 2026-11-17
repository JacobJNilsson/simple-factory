package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// folderFile is the file in a factory's state folder that records the absolute path of its folder.
const folderFile = "folder"

// maxNameLength keeps the socket path of a factory within the system limit.
const maxNameLength = 32

// FactoriesDir returns the folder that holds the state folders of the factories that sfy created.
func FactoriesDir(userHome string) string {
	return filepath.Join(userHome, ".sfy", "factories")
}

// DefaultHome returns the state folder of the default factory.
func DefaultHome(userHome string) string { return filepath.Join(userHome, ".sfy") }

// FactoryName returns the name that a folder gets: its base name in lower case, with each character outside a-z0-9- replaced by -.
func FactoryName(folder string) string {
	name := strings.ToLower(strings.Trim(filepath.Base(filepath.Clean(folder)), "."))
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, name)
	if len(name) > maxNameLength {
		name = name[:maxNameLength]
	}
	if strings.Trim(name, "-") == "" {
		return "factory"
	}
	return name
}

// CanonicalPath resolves symbolic links, so a folder has one spelling. It returns the cleaned path when the folder cannot be resolved.
func CanonicalPath(dir string) string {
	dir = filepath.Clean(dir)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// FactoryFolder returns the folder that a state folder records, or an empty string when it records none.
func FactoryFolder(home string) string {
	b, err := os.ReadFile(filepath.Join(home, folderFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// registered maps each recorded folder to its state folder.
func registered(userHome string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(FactoriesDir(userHome))
	if err != nil {
		return out
	}
	for _, e := range entries {
		state := filepath.Join(FactoriesDir(userHome), e.Name())
		if folder := FactoryFolder(state); folder != "" {
			out[CanonicalPath(folder)] = state
		}
	}
	return out
}

// FindFactory returns the state folder of the nearest factory folder at or above dir.
// The user's home folder is never a factory folder.
func FindFactory(userHome, dir string) (string, bool) {
	known := registered(userHome)
	if len(known) == 0 {
		return "", false
	}
	userHome = CanonicalPath(userHome)
	for d := CanonicalPath(dir); ; d = filepath.Dir(d) {
		if state, ok := known[d]; ok && d != userHome {
			return state, true
		}
		if d == filepath.Dir(d) {
			return "", false
		}
	}
}

// CreateFactory makes the state folder of a new factory for dir and records dir in it.
// Two folders with one base name get the names web and web-2.
func CreateFactory(userHome, dir string) (string, error) {
	dir = CanonicalPath(dir)
	base := FactoryName(dir)
	if err := os.MkdirAll(FactoriesDir(userHome), 0o700); err != nil {
		return "", err
	}
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = base + "-" + strconv.Itoa(n)
		}
		state := filepath.Join(FactoriesDir(userHome), name)
		err := os.Mkdir(state, 0o700)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(state, folderFile), []byte(dir+"\n"), 0o600); err != nil {
			return "", err
		}
		return state, nil
	}
}

// Describe returns the name of the factory with the state folder home, and the folder it works in.
// The default factory has the name default and no folder, so its folder is the state folder.
func Describe(home string, env func(string) string) (name, folder string) {
	if folder = FactoryFolder(home); folder != "" {
		return filepath.Base(filepath.Clean(home)), folder
	}
	if filepath.Clean(home) == DefaultHome(env("HOME")) {
		return "default", home
	}
	return filepath.Base(filepath.Clean(home)), home
}

// Header returns the line that names the factory and its folder.
func Header(home string, env func(string) string) string {
	name, folder := Describe(home, env)
	return fmt.Sprintf("Factory: %s (%s)", name, folder)
}
