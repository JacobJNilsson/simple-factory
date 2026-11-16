package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// NotePath returns the note file of a session and reports whether name is a valid session name.
func NotePath(home, name string) (string, bool) {
	if !sessionName.MatchString(name) {
		return "", false
	}
	return filepath.Join(home, "sessions", name, "note.md"), true
}

// lockNote takes an exclusive lock for the note file at path and returns the function that releases it.
// The lock keeps an append out of a drain, so a note never lands in a file that a drain already read.
func lockNote(path string) (func(), error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

// AppendNote adds text to the note file at path and creates the file when it is missing.
func AppendNote(path, text string) error {
	unlock, err := lockNote(path)
	if err != nil {
		return err
	}
	defer unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(strings.TrimRight(text, "\n") + "\n\n")
	return errors.Join(werr, f.Close())
}

// DrainNote returns the content of the note file at path and leaves no file behind.
// It moves the file aside before it reads it, so a note that arrives meanwhile starts a new file.
// A file that an earlier drain left behind comes first. It returns an empty string when no note waits.
func DrainNote(path string) (string, error) {
	unlock, err := lockNote(path)
	if err != nil {
		return "", err
	}
	defer unlock()
	taken := path + ".taken"
	if err := os.Rename(path, taken+".new"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	var out strings.Builder
	for _, p := range []string{taken, taken + ".new"} {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		out.Write(b)
	}
	// The leftover file stays until both reads worked, so a failed read loses nothing.
	for _, p := range []string{taken, taken + ".new"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// WatchNote makes sure that the note file at path exists and returns the SessionStart hook output that asks Claude Code to watch it.
func WatchNote(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	b, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "SessionStart", "watchPaths": []string{path},
	}})
	return string(b), err
}
