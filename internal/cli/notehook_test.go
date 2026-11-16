package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
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
	if err := claude.AppendNote(path, "From main:\nPlease rebase."); err != nil {
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
