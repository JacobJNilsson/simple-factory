package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
)

const testPoll = 5 * time.Millisecond

func waitRun(env func(string) string, limit time.Duration) (code int, errb string) {
	var e bytes.Buffer
	code = noteWait(env, &e, testPoll, limit)
	return code, e.String()
}

func TestNoteWaitWakesWithANoteThatArrivesWhileItWaits(t *testing.T) {
	env, path := noteEnv(t, "worker-t1")
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = claude.AppendNote(path, "", "From main:\nPlease rebase.")
	}()

	code, errb := waitRun(env, 10*time.Second)

	if code != 2 || errb != "From main:\nPlease rebase.\n" {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the note file stayed: %v", err)
	}
}

func TestNoteWaitTakesANoteThatWaitsAlready(t *testing.T) {
	env, path := noteEnv(t, "worker-t1")
	if err := claude.AppendNote(path, "", "Waiting note."); err != nil {
		t.Fatal(err)
	}

	code, errb := waitRun(env, 10*time.Second)

	if code != 2 || errb != "Waiting note.\n" {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
}

func TestNoteWaitEndsQuietlyAtItsLimit(t *testing.T) {
	env, _ := noteEnv(t, "worker-t1")

	code, errb := waitRun(env, 30*time.Millisecond)

	if code != 0 || errb != "" {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
}

func TestNoteWaitYieldsToTheWaiterThatRuns(t *testing.T) {
	env, path := noteEnv(t, "worker-t1")
	held, err := claude.LockWaiter(path)
	if err != nil || held == nil {
		t.Fatalf("lock: %v, %v", held, err)
	}
	defer func() { _ = held.Close() }()
	if err := claude.AppendNote(path, "", "Stays for the first waiter."); err != nil {
		t.Fatal(err)
	}

	code, errb := waitRun(env, 10*time.Second)

	if code != 0 || errb != "" {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
	if !claude.NotePending(path) {
		t.Fatal("the second waiter took the note")
	}
}

func TestNoteWaitDoesNothingWithoutASessionOrItsFolder(t *testing.T) {
	env, _ := noteEnv(t, "")
	if code, errb := waitRun(env, time.Second); code != 0 || errb != "" {
		t.Errorf("no session: code %d, stderr %q", code, errb)
	}
	gone := envOf(map[string]string{"SFY_HOME": t.TempDir(), "SFY_AGENT": "worker-t9"})
	if code, errb := waitRun(gone, time.Second); code != 0 || errb != "" {
		t.Errorf("no folder: code %d, stderr %q", code, errb)
	}
}

func TestNoteWaitIsAHookFormOfAWorker(t *testing.T) {
	if !sessionOwnHook([]string{"hook", "note-wait"}) {
		t.Fatal("a worker may not run note-wait")
	}
}

// The SessionStart output must be one JSON object with watchPaths inside hookSpecificOutput, as the Claude Code hooks reference says.
func TestNoteWatchOutputIsOneJSONObjectWithAbsoluteWatchPaths(t *testing.T) {
	env, path := noteEnv(t, "worker-t1")

	code, out, _ := noteHookRun(env, "note-watch")

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil || code != 0 {
		t.Fatalf("code %d, output %q: %v", code, out, err)
	}
	if len(raw) != 1 || raw["hookSpecificOutput"] == nil {
		t.Fatalf("top-level keys %v", raw)
	}
	var inner struct {
		Event string   `json:"hookEventName"`
		Paths []string `json:"watchPaths"`
	}
	if err := json.Unmarshal(raw["hookSpecificOutput"], &inner); err != nil {
		t.Fatal(err)
	}
	if inner.Event != "SessionStart" || len(inner.Paths) != 1 || !filepath.IsAbs(inner.Paths[0]) || inner.Paths[0] != path {
		t.Fatalf("hookSpecificOutput %+v", inner)
	}
}
