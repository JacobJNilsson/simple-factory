// Package prompts holds the default agent prompts and finds the one to use.
package prompts

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed defaults/*.md
var defaults embed.FS

// For returns the prompt for a kind of agent: "main", "worker" or "reviewer".
// A file <home>/prompts/<kind>.md replaces the default.
func For(kind, home string) (string, error) {
	switch kind {
	case "main", "worker", "reviewer":
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
