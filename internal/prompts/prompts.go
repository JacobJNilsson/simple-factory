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
func For(kind, home string) (string, error) {
	switch kind {
	case "main", "worker", "reviewer", WorkerRepoPart:
	default:
		return "", fmt.Errorf("prompts: unknown kind %q", kind)
	}
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
