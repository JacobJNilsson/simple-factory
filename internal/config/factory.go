package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// folderFile is the file in a factory folder that records the absolute path of its working folder.
const folderFile = "folder"

// namePrefixFile is the file in a factory folder that records its name prefix. A factory folder without it has an empty name prefix.
const namePrefixFile = "name-prefix"

// StateNames are the names that sfy owns in a factory folder. A factory repo's install must not write them.
var StateNames = []string{
	namePrefixFile, "tasks", "work", "repos", "sessions", "inbox", "next-id", "status.md", "lead-prompt.md", "main-prompt.md", "definition.toml",
	"sfy.log", "sfy.sock", "sfy.lock", "sfy.pid", "drop",
}

// definitionNames are the names in a factory folder that say how sfy behaves.
var definitionNames = []string{"config.toml", "config.d", "prompts", skillsName}

// skillsName is the folder in a layer that holds the skills of the factory's sessions.
const skillsName = "skills"

// Skill is a skill folder that a layer holds. Name is the folder's name, and Dir is its path in the layer.
type Skill struct{ Name, Dir string }

// Skills lists the skills of the layers, one for each name, in name order.
// A skill is a folder below skills/ of a layer that holds a SKILL.md. The nearest layer wins for a name, so layers go lowest first.
// It follows links, and a layer without a skills folder adds none.
func Skills(layers []string) ([]Skill, error) {
	byName := map[string]Skill{}
	for _, layer := range layers {
		dir := filepath.Join(layer, skillsName)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			st, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md"))
			if err == nil && st.Mode().IsRegular() {
				byName[e.Name()] = Skill{Name: e.Name(), Dir: filepath.Join(dir, e.Name())}
			}
		}
	}
	return slices.SortedFunc(maps.Values(byName), func(a, b Skill) int { return strings.Compare(a.Name, b.Name) }), nil
}

// FactoryFiles lists the names in the factory folder home that are neither state nor definition, such as a checks folder that a factory repo installed.
// It leaves out the folder file, the lead's notes.md, and the factories folder of the default factory.
func FactoryFiles(home, userHome string) ([]string, error) {
	entries, err := os.ReadDir(home)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	isDefault := filepath.Clean(home) == DefaultHome(userHome)
	var out []string
	for _, e := range entries {
		name := e.Name()
		if slices.Contains(StateNames, name) || slices.Contains(definitionNames, name) || name == folderFile || name == "notes.md" {
			continue
		}
		if isDefault && filepath.Join(home, name) == FactoriesDir(userHome) {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

// maxNameLength keeps the socket path of a factory within the system limit.
const maxNameLength = 32

// FactoriesDir returns the folder that holds the factory folders of the factories that sfy created.
func FactoriesDir(userHome string) string {
	return filepath.Join(userHome, ".sfy", "factories")
}

// DefaultHome returns the factory folder of the default factory, which is also the machine layer.
func DefaultHome(userHome string) string { return filepath.Join(userHome, ".sfy") }

// defaultName is the name of the default factory, whose factory folder is DefaultHome.
const defaultName = "default"

// factoryAddress matches a factory name in an address. It holds no dot and no slash, so it never leaves the factories folder.
var factoryAddress = regexp.MustCompile(`^[a-z0-9-]+$`)

// FactoryHome returns the factory folder of the factory with the given name, as Describe names it.
// The name default gives DefaultHome. It reports false for a name that no factory folder on this machine has.
func FactoryHome(userHome, name string) (string, bool) {
	if name == defaultName {
		return DefaultHome(userHome), true
	}
	if !factoryAddress.MatchString(name) {
		return "", false
	}
	home := filepath.Join(FactoriesDir(userHome), name)
	st, err := os.Stat(home)
	if err != nil || !st.IsDir() {
		return "", false
	}
	return home, true
}

// SessionFolders returns the sessions folder of the default factory and of each factory in the factories folder.
// Each holds the token files of its sessions, so a sandbox hides them all.
func SessionFolders(userHome string) []string {
	out := []string{filepath.Join(DefaultHome(userHome), "sessions")}
	entries, err := os.ReadDir(FactoriesDir(userHome))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(FactoriesDir(userHome), e.Name(), "sessions"))
		}
	}
	return out
}

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

// WorkFolder returns the working folder that a factory folder records, or an empty string when it records none.
func WorkFolder(home string) string {
	b, err := os.ReadFile(filepath.Join(home, folderFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// registered maps each recorded working folder to the factory folders that record it, in name order.
func registered(userHome string) map[string][]string {
	out := map[string][]string{}
	entries, err := os.ReadDir(FactoriesDir(userHome))
	if err != nil {
		return out
	}
	for _, e := range entries {
		state := filepath.Join(FactoriesDir(userHome), e.Name())
		if folder := WorkFolder(state); folder != "" {
			key := CanonicalPath(folder)
			out[key] = append(out[key], state)
		}
	}
	return out
}

// FactoriesAt returns every factory folder of the nearest working folder at or above dir.
// The user's home folder is never a working folder.
func FactoriesAt(userHome, dir string) []string {
	known := registered(userHome)
	if len(known) == 0 {
		return nil
	}
	userHome = CanonicalPath(userHome)
	for d := CanonicalPath(dir); ; d = filepath.Dir(d) {
		if states, ok := known[d]; ok && d != userHome {
			return states
		}
		if d == filepath.Dir(d) {
			return nil
		}
	}
}

// namePrefixOf returns the name prefix that the factory folder home records, or an empty string when it records none.
func namePrefixOf(home string) string {
	b, err := os.ReadFile(filepath.Join(home, namePrefixFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// FindFactory returns the first factory folder with the name prefix of the nearest working folder at or above dir that has one.
// The user's home folder is never a working folder.
func FindFactory(userHome, dir, prefix string) (string, bool) {
	known := registered(userHome)
	userHome = CanonicalPath(userHome)
	for d := CanonicalPath(dir); ; d = filepath.Dir(d) {
		if d != userHome {
			for _, state := range known[d] {
				if namePrefixOf(state) == prefix {
					return state, true
				}
			}
		}
		if d == filepath.Dir(d) {
			return "", false
		}
	}
}

// CreateFactory makes the factory folder of a new factory for the working folder dir and records dir in it.
// The name starts with the name prefix, and the whole name is cut to maxNameLength characters.
// Two folders with one base name get the names web and web-2. A non-empty name prefix is recorded in the file name-prefix.
func CreateFactory(userHome, dir, prefix string) (string, error) {
	dir = CanonicalPath(dir)
	base := prefix + FactoryName(dir)
	if len(base) > maxNameLength {
		base = base[:maxNameLength]
	}
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
		// The name prefix comes first, because the folder file makes the factory findable.
		if prefix != "" {
			if err := os.WriteFile(filepath.Join(state, namePrefixFile), []byte(prefix+"\n"), 0o600); err != nil {
				return "", err
			}
		}
		if err := os.WriteFile(filepath.Join(state, folderFile), []byte(dir+"\n"), 0o600); err != nil {
			return "", err
		}
		return state, nil
	}
}

// Describe returns the name of the factory with the factory folder home, and its working folder.
// The default factory has the name default and records no working folder, so its working folder is its factory folder.
func Describe(home string, env func(string) string) (name, folder string) {
	if folder = WorkFolder(home); folder != "" {
		return filepath.Base(filepath.Clean(home)), folder
	}
	if filepath.Clean(home) == DefaultHome(env("HOME")) {
		return defaultName, home
	}
	return filepath.Base(filepath.Clean(home)), home
}

// Header returns the line that names the factory and its folder.
func Header(home string, env func(string) string) string {
	name, folder := Describe(home, env)
	return fmt.Sprintf("Factory: %s (%s)", name, folder)
}
