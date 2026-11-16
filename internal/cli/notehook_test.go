package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
	"github.com/JacobJNilsson/simple-factory/internal/store"
)

func noteHookRun(env func(string) string, event string) (code int, out, errb string) {
	var o, e bytes.Buffer
	code = Run([]string{"hook", event}, env, strings.NewReader("{}"), &o, &e)
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
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the note file stayed: %v", err)
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
