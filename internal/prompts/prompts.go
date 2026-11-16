// Package prompts holds the default agent prompts and finds the one to use.
package prompts

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed defaults/*.md
var defaults embed.FS

// WorkerRepoPart is the kind of the worker prompt part that only a task with a repo gets.
// A file <home>/prompts/worker-repo.md replaces its default.
const WorkerRepoPart = "worker-repo"

// Role returns the prompt for a session of kind. A worker on a task with a repo gets the
// worker prompt followed by the repo part. Any other session gets the prompt of its kind only.
func Role(kind, home string, hasRepo bool) (string, error) {
	base, err := For(kind, home)
	if err != nil || kind != "worker" || !hasRepo {
		return base, err
	}
	part, err := For(WorkerRepoPart, home)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(base, "\n") + "\n\n" + part, nil
}

// For returns the prompt for a kind of agent: "main", "worker", "reviewer" or WorkerRepoPart.
// A file <home>/prompts/<kind>.md replaces the default.
// The files ending in .md in <home>/prompts/<kind>.d/ follow in name order, each after a blank line.
func For(kind, home string) (string, error) {
	switch kind {
	case "main", "worker", "reviewer", WorkerRepoPart:
	default:
		return "", fmt.Errorf("prompts: unknown kind %q", kind)
	}
	text, err := base(kind, home)
	if err != nil {
		return "", err
	}
	return appendAdditions(text, filepath.Join(home, "prompts", kind+".d"))
}

// base returns the replacing file of a kind, or the embedded default.
func base(kind, home string) (string, error) {
	b, err := os.ReadFile(filepath.Join(home, "prompts", kind+".md"))
	if err == nil {
		return string(b), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("prompts: %w", err)
	}
	d, err := defaults.ReadFile("defaults/" + kind + ".md")
	if err != nil {
		return "", fmt.Errorf("prompts: %w", err)
	}
	return string(d), nil
}

// appendAdditions appends each .md file of dir to text, in name order. A missing dir adds nothing.
func appendAdditions(text, dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return text, nil
	}
	if err != nil {
		return "", fmt.Errorf("prompts: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return "", fmt.Errorf("prompts: %w", err)
		}
		text = strings.TrimRight(text, "\n") + "\n\n" + string(b)
	}
	return text, nil
}
