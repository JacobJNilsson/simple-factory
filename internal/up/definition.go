package up

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/BurntSushi/toml"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/prompts"
)

// definitionName is the file in the factory folder that holds the flat result that the running sfy process started with.
const definitionName = "definition.toml"

// unsetValue stands in for the value of a setting that one side of a change report lacks.
const unsetValue = "unset"

// noChange is the report line when the flat result did not change.
const noChange = "The definition did not change since the last start."

// definition is the flat result of a factory, as sfy restart compares it.
type definition struct {
	// Settings maps each dotted key to its inline TOML value, as sfy config prints it.
	Settings map[string]string `toml:"settings"`
	// Prompts maps each kind to its prompt files, keyed by the path below prompts/, with the SHA-256 sum of each file.
	// A kind's base file counts only when a layer has one, and a removed addition does not count.
	Prompts map[string]map[string]string `toml:"prompts"`
}

// buildDefinition returns the flat result of cfg and of the prompt files of layers.
func buildDefinition(cfg config.Config, layers []string) (definition, error) {
	settings, err := flatSettings(cfg)
	if err != nil {
		return definition{}, err
	}
	d := definition{Settings: settings, Prompts: map[string]map[string]string{}}
	for _, kind := range promptKinds {
		files, err := promptFiles(kind, layers)
		if err != nil {
			return definition{}, err
		}
		if len(files) > 0 {
			d.Prompts[kind] = files
		}
	}
	return d, nil
}

// promptFiles maps the path below prompts/ of each prompt file of kind to the SHA-256 sum of its text.
func promptFiles(kind string, layers []string) (map[string]string, error) {
	base, additions, err := prompts.Sources(kind, layers)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	if base != prompts.DefaultBase {
		paths[kind+".md"] = base
	}
	for _, a := range additions {
		if !a.Removed {
			paths[kind+".d/"+a.Name] = a.File
		}
	}
	files := map[string]string{}
	for name, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		files[name] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return files, nil
}

// writeDefinition replaces <home>/definition.toml with d in one rename, with mode 0600.
func writeDefinition(home string, d definition) error {
	var text bytes.Buffer
	text.WriteString("# sfy writes this file at start. sfy restart compares the new definition with it.\n")
	if err := toml.NewEncoder(&text).Encode(d); err != nil {
		return err
	}
	f, err := os.CreateTemp(home, "."+definitionName+"-*")
	if err != nil {
		return err
	}
	_, err = f.Write(text.Bytes())
	err = errors.Join(err, f.Chmod(0o600), f.Close())
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(home, definitionName))
	}
	if err != nil {
		return errors.Join(err, os.Remove(f.Name()))
	}
	return nil
}

// readDefinition reads <home>/definition.toml. It reports false when the file does not exist.
func readDefinition(home string) (definition, bool, error) {
	var d definition
	_, err := toml.DecodeFile(filepath.Join(home, definitionName), &d)
	if errors.Is(err, fs.ErrNotExist) {
		return definition{}, false, nil
	}
	if err != nil {
		return definition{}, false, err
	}
	return d, true, nil
}

// definitionReport compares the flat result of cfg and layers with the one that the last start wrote.
// It returns one sentence per change, the noChange line when nothing changed, and nothing on a first start.
func definitionReport(home string, cfg config.Config, layers []string) ([]string, error) {
	old, found, err := readDefinition(home)
	if err != nil || !found {
		return nil, err
	}
	current, err := buildDefinition(cfg, layers)
	if err != nil {
		return nil, err
	}
	lines := definitionChanges(old, current)
	if len(lines) == 0 {
		return []string{noChange}, nil
	}
	return lines, nil
}

// definitionChanges returns a sentence for each setting and each prompt file that differs between old and current.
func definitionChanges(old, current definition) []string {
	var lines []string
	for _, key := range sortedUnion(old.Settings, current.Settings) {
		before, wasSet := old.Settings[key]
		after, isSet := current.Settings[key]
		if wasSet && isSet && before == after {
			continue
		}
		if !wasSet {
			before = unsetValue
		}
		if !isSet {
			after = unsetValue
		}
		lines = append(lines, fmt.Sprintf("Setting %s changed from %s to %s.", key, before, after))
	}
	for _, kind := range promptKinds {
		was, is := old.Prompts[kind], current.Prompts[kind]
		for _, name := range sortedUnion(was, is) {
			before, existed := was[name]
			after, exists := is[name]
			switch {
			case !existed:
				lines = append(lines, fmt.Sprintf("The %s prompt file %s was added.", kind, name))
			case !exists:
				lines = append(lines, fmt.Sprintf("The %s prompt file %s was removed.", kind, name))
			case before != after:
				lines = append(lines, fmt.Sprintf("The %s prompt file %s was changed.", kind, name))
			}
		}
	}
	return lines
}

// sortedUnion returns the keys of a and b in order, each once.
func sortedUnion(a, b map[string]string) []string {
	keys := slices.Collect(maps.Keys(a))
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}
