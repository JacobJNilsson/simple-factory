package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
	"github.com/JacobJNilsson/simple-factory/internal/store"
)

func noteHookRun(env func(string) string, event string) (code int, out, errb string) {
	return noteHookRunWith(env, event, "{}")
}

func noteHookRunWith(env func(string) string, event, stdin string) (code int, out, errb string) {
	var o, e bytes.Buffer
	code = Run([]string{"hook", event}, env, strings.NewReader(stdin), &o, &e)
	return code, o.String(), e.String()
}

func noteEnv(t *testing.T, agent string) (func(string) string, string) {
	t.Helper()
	home := t.TempDir()
	path, _ := claude.NotePath(home, "worker-t1")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	return envOf(map[string]string{"SFY_HOME": home, "SFY_AGENT": agent}), path
}

func TestNoteHookPrintsTheNotesToStderrAndExitsWithTwo(t *testing.T) {
	env, path := noteEnv(t, "worker-t1")
	if err := claude.AppendNote(path, "", "From main:\nPlease rebase."); err != nil {
		t.Fatal(err)
	}

	code, out, errb := noteHookRun(env, "note")

	if code != 2 || out != "" || errb != "From main:\nPlease rebase.\n" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errb)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() != 0 {
		t.Fatalf("the note file is not empty and in place: %v", err)
	}
}

// storeMessage puts an unread message for worker-t1 into the state folder that the hook env names.
func storeMessage(t *testing.T, env func(string) string, id, body string) store.Store {
	t.Helper()
	st := store.Files(env("SFY_HOME"))
	if err := st.Send(store.Message{ID: id, From: "main", To: "worker-t1", Body: body, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestNoteHookMarksADeliveredMessageReadOnlyWhenItDrains(t *testing.T) {
	env, path := noteEnv(t, "worker-t1")
	st := storeMessage(t, env, "m1", "Please rebase.")
	if err := claude.AppendNote(path, "m1", "From main:\nPlease rebase."); err != nil {
		t.Fatal(err)
	}
	if unread, _ := st.Unread("worker-t1"); len(unread) != 1 {
		t.Fatalf("unread before the drain: %+v", unread)
	}

	code, _, errb := noteHookRun(env, "note")

	if code != 2 || errb != "From main:\nPlease rebase.\n" {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
	if unread, _ := st.Unread("worker-t1"); len(unread) != 0 {
		t.Fatalf("unread after the drain: %+v", unread)
	}
}

func TestNoteHookSkipsAMessageThatSfyInboxAlreadyShowed(t *testing.T) {
	env, path := noteEnv(t, "worker-t1")
	st := storeMessage(t, env, "m1", "Please rebase.")
	if err := claude.AppendNote(path, "m1", "From main:\nPlease rebase."); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRead("worker-t1", []string{"m1"}); err != nil {
		t.Fatal(err)
	}

	code, out, errb := noteHookRun(env, "note")

	if code != 0 || out != "" || errb != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errb)
	}
}

func TestNoteHookDeliversAResentMessageOnce(t *testing.T) {
	env, path := noteEnv(t, "worker-t1")
	storeMessage(t, env, "m1", "Please rebase.")
	for range 2 {
		if err := claude.AppendNote(path, "m1", "From main:\nPlease rebase."); err != nil {
			t.Fatal(err)
		}
	}

	_, _, errb := noteHookRun(env, "note")

	if errb != "From main:\nPlease rebase.\n" {
		t.Fatalf("stderr %q", errb)
	}
}

func TestNoteWatchHookRecordsThatTheHooksRan(t *testing.T) {
	env, path := noteEnv(t, "worker-t1")

	noteHookRun(env, "note-watch")

	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "hooks-ready")); err != nil {
		t.Fatal(err)
	}
}

func TestNoteHookWithoutANoteExitsQuietly(t *testing.T) {
	env, _ := noteEnv(t, "worker-t1")

	code, out, errb := noteHookRun(env, "note")

	if code != 0 || out != "" || errb != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errb)
	}
}

func TestNoteHookDoesNothingWithoutASession(t *testing.T) {
	env, _ := noteEnv(t, "")

	for _, event := range []string{"note", "note-watch"} {
		if code, out, errb := noteHookRun(env, event); code != 0 || out != "" || errb != "" {
			t.Errorf("%s: code %d, stdout %q, stderr %q", event, code, out, errb)
		}
	}
}

func TestNoteWatchHookNamesTheSessionsNoteFile(t *testing.T) {
	env, path := noteEnv(t, "worker-t1")

	code, out, _ := noteHookRun(env, "note-watch")

	if code != 0 || !strings.Contains(out, `"watchPaths":["`+path+`"]`) {
		t.Fatalf("code %d, stdout %q", code, out)
	}
}

func TestNoteWatchHookNamesTheEventOfItsInput(t *testing.T) {
	for _, tc := range []struct{ stdin, want string }{
		{`{"hook_event_name":"CwdChanged"}`, "CwdChanged"},
		{`{"hook_event_name":"SessionStart"}`, "SessionStart"},
		{`{}`, "SessionStart"},
		{``, "SessionStart"},
	} {
		env, _ := noteEnv(t, "worker-t1")

		_, out, _ := noteHookRunWith(env, "note-watch", tc.stdin)

		if !strings.Contains(out, `"hookEventName":"`+tc.want+`"`) {
			t.Errorf("input %q: stdout %q", tc.stdin, out)
		}
	}
}

func TestTurnEndedHookDeliversAQueuedNoteAndKeepsTheTurnGoing(t *testing.T) {
	env, path := noteEnv(t, "worker-t1")
	if err := claude.AppendNote(path, "", "From main:\nPlease rebase."); err != nil {
		t.Fatal(err)
	}

	code, _, errb := noteHookRun(env, "turn-ended")

	if code != 2 || !strings.HasPrefix(errb, "From main:\nPlease rebase.") {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
}

// socketEnv returns the env of worker-t1 and a short state folder, because a socket path has a length limit.
func socketEnv(t *testing.T) (func(string) string, string) {
	t.Helper()
	home, err := os.MkdirTemp("", "fc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	if err := os.MkdirAll(filepath.Join(home, "sessions", "worker-t1"), 0o700); err != nil {
		t.Fatal(err)
	}
	return envOf(map[string]string{"SFY_HOME": home, "SFY_AGENT": "worker-t1"}), home
}

// fakeSfy listens on the socket in home, records the command of each request and answers OK.
func fakeSfy(t *testing.T, home string) *[]string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "unix", filepath.Join(home, "sfy.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	var commands []string
	var mu sync.Mutex
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			var req protocol.Request
			if json.NewDecoder(bufio.NewReader(conn)).Decode(&req) == nil {
				mu.Lock()
				commands = append(commands, req.Command)
				mu.Unlock()
			}
			_ = json.NewEncoder(conn).Encode(protocol.Answer{OK: true})
			_ = conn.Close()
		}
	}()
	return &commands
}

func TestTurnEndedHookTellsSfyThatTheTurnGoesOn(t *testing.T) {
	env, home := socketEnv(t)
	path, _ := claude.NotePath(home, "worker-t1")
	commands := fakeSfy(t, home)
	if err := claude.AppendNote(path, "", "From main:\nPlease rebase."); err != nil {
		t.Fatal(err)
	}

	code, _, _ := noteHookRun(env, "turn-ended")

	if code != 2 || !slices.Equal(*commands, []string{"hook busy"}) {
		t.Fatalf("code %d, commands %q", code, *commands)
	}
}

func TestTurnEndedHookWithoutANoteReportsTheTurnEnd(t *testing.T) {
	env, home := socketEnv(t)
	commands := fakeSfy(t, home)

	code, _, _ := noteHookRun(env, "turn-ended")

	if code != 0 || !slices.Equal(*commands, []string{"hook turn-ended"}) {
		t.Fatalf("code %d, commands %q", code, *commands)
	}
}
