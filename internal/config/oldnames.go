package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// oldKey is a setting that sfy renamed. Parent is the key path of the table that holds it, where "*" matches any name.
type oldKey struct {
	parent   []string
	old, new string
}

// oldKeys lists the settings that named the lead by its old name. sfy reads each one as its new name until a later release removes them.
var oldKeys = []oldKey{
	{parent: []string{"models"}, old: "main", new: "lead"},
	{parent: []string{"session_rules"}, old: "main", new: "lead"},
	{parent: []string{"session_settings"}, old: "main", new: "lead"},
	{parent: []string{"multiplexers", "*"}, old: "show_main", new: "show_lead"},
}

// renamed is one old key that a file sets, with the dotted old and new key paths.
type renamed struct{ old, new string }

// bothKeysError reports a config file that sets a setting under its old and its new name. Old and New are the dotted key paths.
type bothKeysError struct{ Old, New string }

func (e *bothKeysError) Error() string {
	return fmt.Sprintf("sets both %s and %s. Remove %s", e.Old, e.New, e.Old)
}

// renameOldKeys renames each old key of raw to its new name in place, and returns the renamed keys in the order of oldKeys.
// A table that sets both names is a bothKeysError.
func renameOldKeys(raw rawTable) ([]renamed, error) {
	var found []renamed
	for _, k := range oldKeys {
		for _, t := range tablesAt(raw, nil, k.parent) {
			value, ok := t.table[k.old]
			if !ok {
				continue
			}
			oldPath := strings.Join(append(slices.Clone(t.path), k.old), ".")
			newPath := strings.Join(append(slices.Clone(t.path), k.new), ".")
			if _, both := t.table[k.new]; both {
				return nil, &bothKeysError{Old: oldPath, New: newPath}
			}
			t.table[k.new] = value
			delete(t.table, k.old)
			found = append(found, renamed{old: oldPath, new: newPath})
		}
	}
	return found, nil
}

// namedTable is a table inside a raw table, with its key path.
type namedTable struct {
	path  []string
	table rawTable
}

// tablesAt returns the tables of raw that match the key path pattern, in name order. A "*" in pattern matches every key.
func tablesAt(raw rawTable, path, pattern []string) []namedTable {
	if len(pattern) == 0 {
		return []namedTable{{path: path, table: raw}}
	}
	var out []namedTable
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		if pattern[0] != "*" && pattern[0] != key {
			continue
		}
		if sub, ok := raw[key].(map[string]any); ok {
			out = append(out, tablesAt(rawTable(sub), append(slices.Clone(path), key), pattern[1:])...)
		}
	}
	return out
}

// oldPrompts lists the prompt files and folders that named the lead by its old name, with their new names.
var oldPrompts = []struct{ old, new, what string }{
	{old: "main.md", new: "lead.md", what: "file"},
	{old: "main.d", new: "lead.d", what: "folder"},
}

// OldNames returns one line for each old key and prompt name that the factory home still uses, for warnings and for sfy config.
// Each line names the file or folder, the name that sfy reads in its place, and what the operator renames.
func OldNames(home, userHome string) ([]string, error) {
	paths, err := configFiles(home, userHome)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, path := range paths {
		_, found, err := readRenamed(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, r := range found {
			lines = append(lines, fmt.Sprintf("%s sets %s. sfy reads it as %s. Rename the key.", path, r.old, r.new))
		}
	}
	layers, err := Layers(home, userHome)
	if err != nil {
		return nil, err
	}
	for _, layer := range layers {
		for _, p := range oldPrompts {
			path := filepath.Join(layer, "prompts", p.old)
			_, err := os.Stat(path)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("config: %w", err)
			}
			lines = append(lines, fmt.Sprintf("%s has the old name. sfy reads it as %s. Rename the %s.", path, p.new, p.what))
		}
	}
	for _, folder := range walkFolders(WorkFolder(home), userHome) {
		path := filepath.Join(folder, RepoFile)
		_, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		lines = append(lines, fmt.Sprintf("%s has the old name. sfy still reads it for the repos below it. Move it to %s.", path, filepath.Join(folder, OverrideDir, RepoConfigFile)))
	}
	return lines, nil
}
