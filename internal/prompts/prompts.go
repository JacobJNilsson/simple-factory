// Package prompts holds the default agent prompts and finds the one to use.
// A prompt comes from a list of layer folders, lowest first, such as the machine layer ~/.sfy and then the factory folder.
package prompts

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

//go:embed defaults/*.md
var defaults embed.FS

// WorkerRepoPart is the kind of the worker prompt part that only a task with a repo gets.
// A file prompts/worker-repo.md in a layer replaces its default.
const WorkerRepoPart = "worker-repo"

// LeadMemoryPart is the kind of the lead prompt part that says what the lead keeps as its memory and reads at each start.
// A file prompts/lead-memory.md in a layer replaces its default.
const LeadMemoryPart = "lead-memory"

// DefaultBase is the base source that Sources names when no layer has a <kind>.md file.
const DefaultBase = "default"

// Addition is one file of a kind's additions, in the order that sfy appends them.
type Addition struct {
	// Name is the file name, which decides the order and which file replaces which.
	Name string
	// File is the path of the file that wins for Name.
	File string
	// Removed is true when File is empty, so the addition of that name drops out of the prompt.
	Removed bool
}

// Role returns the prompt for a session of kind. The lead gets its prompt followed by the memory part.
// A worker on a task with a repo gets the worker prompt followed by the repo part. Any other session gets the prompt of its kind only.
func Role(kind string, layers []string, hasRepo bool) (string, error) {
	base, err := For(kind, layers)
	if err != nil {
		return "", err
	}
	name := partName(kind, hasRepo)
	if name == "" {
		return base, nil
	}
	part, err := For(name, layers)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(base, "\n") + "\n\n" + part, nil
}

// partName returns the kind of the part that Role appends to the prompt of kind, or an empty string for none.
func partName(kind string, hasRepo bool) string {
	switch {
	case kind == lead:
		return LeadMemoryPart
	case kind == "worker" && hasRepo:
		return WorkerRepoPart
	}
	return ""
}

// For returns the prompt of a name: "lead", or the prompt name of an agent such as "worker", "reviewer" or WorkerRepoPart.
// The last layer with prompts/<kind>.md gives the base, else the embedded default does.
// The .md files of prompts/<kind>.d/ in all layers follow in name order, each after a blank line, as appendAdditions merges them.
func For(kind string, layers []string) (string, error) {
	if err := checkKind(kind); err != nil {
		return "", err
	}
	text, err := base(kind, layers)
	if err != nil {
		return "", err
	}
	return appendAdditions(text, additionDirs(kind, layers))
}

// Sources returns the file of a kind's base prompt, or DefaultBase, and its additions in the order that sfy appends them.
func Sources(kind string, layers []string) (base string, additions []Addition, err error) {
	if err := checkKind(kind); err != nil {
		return "", nil, err
	}
	base = DefaultBase
	if path, ok, err := lastBase(kind, layers); err != nil {
		return "", nil, err
	} else if ok {
		base = path
	}
	merged, err := mergeAdditions(additionDirs(kind, layers))
	if err != nil {
		return "", nil, err
	}
	for _, m := range merged {
		additions = append(additions, m.Addition)
	}
	return base, additions, nil
}

// promptName limits a prompt name to the names of agents, so a name cannot leave the prompts folder.
var promptName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// checkKind refuses a name that is neither the lead nor a valid agent name. The old name of the lead is no prompt name of its own.
func checkKind(kind string) error {
	if kind == lead || (kind != oldLead && promptName.MatchString(kind)) {
		return nil
	}
	return fmt.Errorf("prompts: unknown kind %q", kind)
}

// MissingError reports a prompt name that no layer has a file for and that has no embedded default.
type MissingError struct {
	Name string
}

func (e *MissingError) Error() string {
	return fmt.Sprintf("prompts: no layer has prompts/%s.md, and sfy has no default prompt of that name", e.Name)
}

// Has reports whether a prompt of the name exists in a layer or as an embedded default.
func Has(name string, layers []string) (bool, error) {
	if err := checkKind(name); err != nil {
		return false, err
	}
	if _, ok, err := lastBase(name, layers); ok || err != nil {
		return ok, err
	}
	_, err := fs.Stat(defaults, "defaults/"+name+".md")
	return err == nil, nil
}

// lead is the kind of the lead, and oldLead is the name that its prompt files had before.
// A layer may still use the old name, and sfy reads it as the new one.
const (
	lead    = "lead"
	oldLead = "main"
)

// names returns the names of a kind's files in one layer, the one that wins last.
func names(kind string) []string {
	if kind == lead {
		return []string{oldLead, lead}
	}
	return []string{kind}
}

// additionDirs returns the addition folders of each layer, lowest first. In a layer, lead.d comes after main.d, so its file wins over one of the same name.
func additionDirs(kind string, layers []string) []string {
	dirs := make([]string, 0, len(layers))
	for _, l := range layers {
		for _, name := range names(kind) {
			dirs = append(dirs, filepath.Join(l, "prompts", name+".d"))
		}
	}
	return dirs
}

// lastBase returns the <kind>.md file of the last layer that has one. In a layer, lead.md wins over main.md.
func lastBase(kind string, layers []string) (string, bool, error) {
	for _, l := range slices.Backward(layers) {
		for _, name := range slices.Backward(names(kind)) {
			path := filepath.Join(l, "prompts", name+".md")
			_, err := os.Stat(path)
			if err == nil {
				return path, true, nil
			}
			if !errors.Is(err, fs.ErrNotExist) {
				return "", false, fmt.Errorf("prompts: %w", err)
			}
		}
	}
	return "", false, nil
}

// base returns the text of the last layer's replacing file of a kind, or the embedded default.
func base(kind string, layers []string) (string, error) {
	path, ok, err := lastBase(kind, layers)
	if err != nil {
		return "", err
	}
	if ok {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("prompts: %w", err)
		}
		return string(b), nil
	}
	d, err := defaults.ReadFile("defaults/" + kind + ".md")
	if errors.Is(err, fs.ErrNotExist) {
		return "", &MissingError{Name: kind}
	}
	if err != nil {
		return "", fmt.Errorf("prompts: %w", err)
	}
	return string(d), nil
}

// appendAdditions appends the additions of dirs to text, in name order.
// A file in a later dir replaces the file of the same name in an earlier dir, and an empty file removes it.
func appendAdditions(text string, dirs []string) (string, error) {
	merged, err := mergeAdditions(dirs)
	if err != nil {
		return "", err
	}
	for _, m := range merged {
		if !m.Removed {
			text = strings.TrimRight(text, "\n") + "\n\n" + m.text
		}
	}
	return text, nil
}

// addition is an Addition with the text of its file.
type addition struct {
	Addition
	text string
}

// mergeAdditions collects the .md files of dirs by name, where a later dir wins, and returns them in name order.
// A missing dir adds nothing. A file that holds only white space counts as empty.
func mergeAdditions(dirs []string) ([]addition, error) {
	winners := map[string]string{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("prompts: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
				continue
			}
			winners[e.Name()] = filepath.Join(dir, e.Name())
		}
	}
	var out []addition
	for _, name := range slices.Sorted(maps.Keys(winners)) {
		b, err := os.ReadFile(winners[name])
		if err != nil {
			return nil, fmt.Errorf("prompts: %w", err)
		}
		text := string(b)
		out = append(out, addition{
			Addition: Addition{Name: name, File: winners[name], Removed: strings.TrimSpace(text) == ""},
			text:     text,
		})
	}
	return out, nil
}
