package claude_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
)

func notePath(t *testing.T) string {
	t.Helper()
	path, ok := claude.NotePath(t.TempDir(), "worker-t1")
	if !ok {
		t.Fatal("NotePath refused a valid name")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNotePathLivesInTheSessionFolder(t *testing.T) {
	got, ok := claude.NotePath("/home/sfy", "worker-t1")

	if !ok || got != "/home/sfy/sessions/worker-t1/note.md" {
		t.Fatalf("got %q, %v", got, ok)
	}
}

func TestNotePathRefusesAnUnsafeName(t *testing.T) {
	if _, ok := claude.NotePath("/home/sfy", "../x"); ok {
		t.Fatal("NotePath accepted a path in the name")
	}
}

func TestSeveralNotesBeforeADrainArriveTogether(t *testing.T) {
	path := notePath(t)
	for _, n := range []string{"first", "second\n"} {
		if err := claude.AppendNote(path, n); err != nil {
			t.Fatal(err)
		}
	}

	got, err := claude.DrainNote(path)

	if err != nil || got != "first\n\nsecond" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDrainMovesTheFileAside(t *testing.T) {
	path := notePath(t)
	if err := claude.AppendNote(path, "once"); err != nil {
		t.Fatal(err)
	}
	if _, err := claude.DrainNote(path); err != nil {
		t.Fatal(err)
	}

	again, err := claude.DrainNote(path)

	if err != nil || again != "" {
		t.Fatalf("second drain returned %q, %v", again, err)
	}
	if entries, _ := filepath.Glob(path + "*"); len(entries) != 1 || !strings.HasSuffix(entries[0], ".lock") {
		t.Fatalf("files left behind: %v", entries)
	}
}

func TestDrainWithoutAFileReturnsNothing(t *testing.T) {
	got, err := claude.DrainNote(notePath(t))

	if err != nil || got != "" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDrainKeepsTheNoteOfAnEarlierFailedDrain(t *testing.T) {
	path := notePath(t)
	if err := os.WriteFile(path+".taken", []byte("old\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := claude.AppendNote(path, "new"); err != nil {
		t.Fatal(err)
	}

	got, err := claude.DrainNote(path)

	if err != nil || got != "old\n\nnew" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestNoNoteIsLostWhenWrittenDuringDrains(t *testing.T) {
	path := notePath(t)
	const total = 200
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range total {
			if err := claude.AppendNote(path, fmt.Sprintf("note-%03d", i)); err != nil {
				t.Error(err)
			}
		}
	}()
	var drained strings.Builder
	for range 50 {
		got, err := claude.DrainNote(path)
		if err != nil {
			t.Fatal(err)
		}
		drained.WriteString(got + "\n")
	}
	wg.Wait()
	last, err := claude.DrainNote(path)
	if err != nil {
		t.Fatal(err)
	}
	drained.WriteString(last)

	for i := range total {
		want := fmt.Sprintf("note-%03d", i)
		if n := strings.Count(drained.String(), want); n != 1 {
			t.Fatalf("%s arrived %d times", want, n)
		}
	}
}

func TestDeliverAppendsToTheNoteFileOfASessionWithSettings(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())

	ok, err := p.Deliver("worker-t1", "From main:\nPlease rebase.")
	if err != nil || !ok {
		t.Fatalf("ok %v, err %v", ok, err)
	}

	path, _ := claude.NotePath(home, "worker-t1")
	got, err := claude.DrainNote(path)
	if err != nil || got != "From main:\nPlease rebase." {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestCommandRemovesTheNoteOfAnEarlierSessionWithTheSameName(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())
	if ok, err := p.Deliver("worker-t1", "old note"); err != nil || !ok {
		t.Fatalf("ok %v, err %v", ok, err)
	}

	command(t, p, worker())

	path, _ := claude.NotePath(home, "worker-t1")
	for _, f := range []string{path, path + ".lock"} {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived the session start: %v", f, err)
		}
	}
}

func TestDeliverDeclinesASessionThatSfyDidNotSetUp(t *testing.T) {
	p, home := setup(t, bin)

	ok, err := p.Deliver("main", "hi")

	if err != nil || ok {
		t.Fatalf("ok %v, err %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(home, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("Deliver created files: %v", err)
	}
}

func TestDeliverDeclinesAnUnsafeSessionName(t *testing.T) {
	p, _ := setup(t, bin)

	if ok, err := p.Deliver("../worker-t1", "hi"); err != nil || ok {
		t.Fatalf("ok %v, err %v", ok, err)
	}
}

func TestWatchNoteNamesTheFileInTheSessionStartOutput(t *testing.T) {
	path := notePath(t)

	out, err := claude.WatchNote(path)

	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Output struct {
			Event string   `json:"hookEventName"`
			Paths []string `json:"watchPaths"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	if got.Output.Event != "SessionStart" || len(got.Output.Paths) != 1 || got.Output.Paths[0] != path {
		t.Fatalf("got %+v", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the watched file does not exist: %v", err)
	}
}

func TestSessionSettingsWatchTheNoteFileAndWakeOnAChange(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())

	s := readSettings(t, home, "worker-t1")

	if got := hookOf(t, s, "SessionStart").Command; got != bin+" hook note-watch" {
		t.Errorf("SessionStart runs %q", got)
	}
	change := hookOf(t, s, "FileChanged")
	if change.Command != bin+" hook note" || !change.AsyncRewake {
		t.Errorf("FileChanged hook %+v", change)
	}
	if got := s.Hooks["FileChanged"][0].Matcher; got != "" {
		t.Errorf("FileChanged matcher %q", got)
	}
}

func TestMainSettingsHaveTheNoteHooksToo(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, mainSpec())

	s := readSettings(t, home, "main")

	if !hookOf(t, s, "FileChanged").AsyncRewake || hookOf(t, s, "SessionStart").Command == "" {
		t.Fatalf("hooks %+v", s.Hooks)
	}
}
