package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"
)

// rawTable is a TOML table as the decoder gives it. Each value is any TOML value, and a nested table is a map[string]any.
type rawTable map[string]any

// FileError reports a config file that sfy cannot parse or decode.
type FileError struct {
	Path string
	Err  error
}

func (e *FileError) Error() string { return fmt.Sprintf("config: %s: %v", e.Path, e.Err) }

func (e *FileError) Unwrap() error { return e.Err }

// readTable parses one config file into a raw table, with each old key renamed to its new name.
// It also decodes the file onto a spare Config, so a value of the wrong type names its own file.
func readTable(path string) (rawTable, error) {
	raw, _, err := readRenamed(path)
	return raw, err
}

// readRenamed is readTable that also returns each old key that the file sets, as renameOldKeys reports them.
func readRenamed(path string) (rawTable, []renamed, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	raw := rawTable{}
	if _, err := toml.Decode(string(b), (*map[string]any)(&raw)); err != nil {
		return nil, nil, &FileError{Path: path, Err: err}
	}
	var spare Config
	if _, err := toml.Decode(string(b), &spare); err != nil {
		return nil, nil, &FileError{Path: path, Err: err}
	}
	found, err := renameOldKeys(raw)
	if err != nil {
		return nil, nil, &FileError{Path: path, Err: err}
	}
	return raw, found, nil
}

// mergeTables merges upper onto lower at every depth and returns a new table.
// Where both hold a table, the tables merge key by key. Any other value of upper, a list too, replaces the value of lower.
func mergeTables(lower, upper rawTable) rawTable {
	out := make(rawTable, len(lower)+len(upper))
	for k, v := range lower {
		out[k] = v
	}
	for k, v := range upper {
		low, lowTable := out[k].(map[string]any)
		up, upTable := v.(map[string]any)
		if lowTable && upTable {
			out[k] = map[string]any(mergeTables(low, up))
			continue
		}
		out[k] = v
	}
	return out
}

// Sources maps the key path of each setting that a file sets to the last file that sets it, for sfy config.
// A key path names a value that is not a table, such as models.worker or session_settings.worker.env.TOKEN.
// A setting that no file sets keeps its default and has no entry.
func Sources(home, userHome string) (map[string]string, error) {
	paths, err := configFiles(home, userHome)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, path := range paths {
		raw, err := readTable(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		walkLeaves(raw, nil, func(key toml.Key) { out[key.String()] = path })
	}
	return out, nil
}

// walkLeaves calls visit with the key path of each value below table that is not a table itself.
func walkLeaves(table rawTable, prefix toml.Key, visit func(toml.Key)) {
	for k, v := range table {
		key := append(append(toml.Key{}, prefix...), k)
		if sub, ok := v.(map[string]any); ok {
			walkLeaves(sub, key, visit)
			continue
		}
		visit(key)
	}
}
