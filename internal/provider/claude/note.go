package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// readyPath returns the marker that the SessionStart hook writes when it runs in the session.
func readyPath(noteFile string) string { return filepath.Join(filepath.Dir(noteFile), "hooks-ready") }

// Ready reports whether the running session of name runs the note hooks.
// Only the SessionStart hook writes the marker, and a new start removes it. So a session that an older sfy started never has it.
func Ready(home, name string) bool {
	path, ok := NotePath(home, name)
	if !ok {
		return false
	}
	_, err := os.Stat(readyPath(path))
	return err == nil
}

// Deliver appends text to the note file of the session. The session's FileChanged hook should then take it.
// The text counts as delivered only when the hook takes it, so the caller must check that.
// A non-empty id names the stored message that the text carries, and the drain delivers the text only while that message is unread.
// It returns false when the session's hooks never ran, because no hook watches such a session's note file.
func (a adapter) Deliver(name, text, id string) (bool, error) {
	if !Ready(a.home, name) {
		return false, nil
	}
	path, _ := NotePath(a.home, name)
	if err := AppendNote(path, id, text); err != nil {
		return false, fmt.Errorf("claude: %w", err)
	}
	return true, nil
}

// removeStaleNote deletes the note file and its lock of an earlier session with the same name.
// A new session must not read a note that was meant for its predecessor.
func removeStaleNote(home, name string) error {
	path, ok := NotePath(home, name)
	if !ok {
		return nil
	}
	for _, p := range []string{path, path + ".lock", path + ".taken", path + ".taken.new"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("claude: %w", err)
		}
	}
	return nil
}

// Stuck reports whether the note file of a session holds text that no hook will drain.
func (a adapter) Stuck(name string) bool {
	path, ok := NotePath(a.home, name)
	if !ok || Ready(a.home, name) {
		return false
	}
	for _, p := range []string{path, path + ".taken", path + ".taken.new"} {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			return true
		}
	}
	return false
}

// Drain takes the stuck notes that are no stored message and returns their text.
// A stored message stays unread, so sfy inbox shows it from the store.
func (a adapter) Drain(name string) (string, error) {
	path, ok := NotePath(a.home, name)
	if !ok || Ready(a.home, name) {
		return "", nil
	}
	if _, err := os.Stat(filepath.Dir(path)); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	entries, err := DrainNote(path)
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	var texts []string
	for _, e := range entries {
		if e.ID == "" {
			texts = append(texts, e.Text)
		}
	}
	return strings.Join(texts, "\n\n"), nil
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

const (
	noteHeader    = "<!-- sfy-note -->"
	messageHeader = "<!-- sfy-message "
)

// Entry is one note of a note file. A non-empty ID names the stored message that the text carries.
type Entry struct{ ID, Text string }

// AppendNote adds a note to the note file at path and creates the file when it is missing.
// A header line before the text keeps the entries apart. A file from an older sfy has no header and counts as one plain note.
// A message that the file already holds is not added again, so a resend does not grow the file.
func AppendNote(path, id, text string) error {
	unlock, err := lockNote(path)
	if err != nil {
		return err
	}
	defer unlock()
	header := noteHeader
	if id != "" {
		header = messageHeader + id + " -->"
		if b, err := os.ReadFile(path); err == nil && slices.Contains(strings.Split(string(b), "\n"), header) {
			return nil
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(header + "\n" + strings.TrimRight(text, "\n") + "\n\n")
	return errors.Join(werr, f.Close())
}

// DrainNote returns the entries of the note file at path and leaves no file behind.
// It moves the file aside before it reads it, so a note that arrives meanwhile starts a new file.
// A file that an earlier drain left behind comes first. It returns no entries when no note waits.
func DrainNote(path string) ([]Entry, error) {
	unlock, err := lockNote(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	taken := path + ".taken"
	if err := os.Rename(path, taken+".new"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var out strings.Builder
	for _, p := range []string{taken, taken + ".new"} {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out.Write(b)
	}
	// The leftover file stays until both reads worked, so a failed read loses nothing.
	for _, p := range []string{taken, taken + ".new"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return parseEntries(out.String()), nil
}

// NotePending reports whether the note file at path, or a file that an earlier drain left, holds text.
func NotePending(path string) bool {
	for _, p := range []string{path, path + ".taken", path + ".taken.new"} {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			return true
		}
	}
	return false
}

// LockWaiter takes the lock that lets one note waiter run for the session of the note file at path.
// It returns a nil file when another waiter holds the lock. The caller must keep the file open and reachable while it waits, because closing it releases the lock.
func LockWaiter(path string) (*os.File, error) {
	f, err := os.OpenFile(path+".wait", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

// parseEntries splits the text of a note file at its header lines. Text before the first header is one plain note.
func parseEntries(text string) []Entry {
	var entries []Entry
	var cur *Entry
	var body []string
	flush := func() {
		if cur == nil {
			return
		}
		cur.Text = strings.TrimSpace(strings.Join(body, "\n"))
		if cur.Text != "" {
			entries = append(entries, *cur)
		}
		cur, body = nil, nil
	}
	for _, line := range strings.Split(text, "\n") {
		switch {
		case line == noteHeader:
			flush()
			cur = &Entry{}
		case strings.HasPrefix(line, messageHeader) && strings.HasSuffix(line, " -->"):
			flush()
			cur = &Entry{ID: strings.TrimSuffix(strings.TrimPrefix(line, messageHeader), " -->")}
		default:
			if cur == nil {
				cur = &Entry{}
			}
			body = append(body, line)
		}
	}
	flush()
	return entries
}

// WatchNote makes sure that the note file at path exists and records that the session runs the note hooks.
// It returns the SessionStart hook output that asks Claude Code to watch the file.
func WatchNote(path string) (string, error) {
	if err := os.WriteFile(readyPath(path), nil, 0o600); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
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
