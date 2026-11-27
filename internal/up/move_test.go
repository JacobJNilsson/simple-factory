package up

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/mux"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

// oldFactory returns a factory folder that an sfy from before the rename left: the lead's session folder, its inbox and its prompt under the old name.
func oldFactory(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, "sessions", "main", "conversation-id"), "11111111-2222-4333-8444-555555555555\n")
	writeTestFile(t, filepath.Join(home, "inbox", "main", "m1.md"), "a message\n")
	writeTestFile(t, filepath.Join(home, "main-prompt.md"), "the old prompt\n")
	return home
}

// aliveOnly returns an alive check that reports the named terminals as running and counts its calls.
func aliveOnly(calls *int, running ...string) func(string) (bool, error) {
	return func(name string) (bool, error) {
		*calls++
		for _, r := range running {
			if r == name {
				return true, nil
			}
		}
		return false, nil
	}
}

func TestMoveToLeadMovesTheFilesLeavesLinksAndRecordsTheRunningTerminal(t *testing.T) {
	home := oldFactory(t)
	calls := 0

	if err := moveToLead(home, "acme-", aliveOnly(&calls, "acme-main")); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{"sessions/lead/conversation-id", "inbox/lead/m1.md", "lead-prompt.md"} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	for rel, target := range map[string]string{"sessions/main": "lead", "inbox/main": "lead", "main-prompt.md": "lead-prompt.md"} {
		if got, err := os.Readlink(filepath.Join(home, rel)); err != nil || got != target {
			t.Errorf("%s links to %q, %v, want %q", rel, got, err, target)
		}
	}
	if b, err := os.ReadFile(filepath.Join(home, "sessions", "main", "conversation-id")); err != nil || !strings.HasPrefix(string(b), "1111") {
		t.Errorf("the old path does not reach the conversation id: %q, %v", b, err)
	}
	if b, err := os.ReadFile(registration.AddressPath(home)); err != nil || string(b) != "acme-main\n" {
		t.Errorf("address %q, %v, want acme-main", b, err)
	}
}

func TestMoveToLeadRecordsNoAddressWhenTheOldTerminalIsGone(t *testing.T) {
	home := oldFactory(t)
	calls := 0

	if err := moveToLead(home, "", aliveOnly(&calls)); err != nil {
		t.Fatal(err)
	}

	if calls != 1 {
		t.Errorf("alive ran %d times, want once", calls)
	}
	if _, err := os.Stat(registration.AddressPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("address file: %v, want none", err)
	}
}

func TestMoveToLeadKeepsTheAddressOfARegisteredLead(t *testing.T) {
	home := oldFactory(t)
	writeTestFile(t, filepath.Join(home, "sessions", "main", "address"), "dev\n")
	calls := 0

	if err := moveToLead(home, "", aliveOnly(&calls, "main")); err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(registration.AddressPath(home)); string(b) != "dev\n" {
		t.Errorf("address %q, want dev", b)
	}
}

func TestASecondMoveToLeadMovesNothingAndAsksNothing(t *testing.T) {
	home := oldFactory(t)
	calls := 0
	if err := moveToLead(home, "", aliveOnly(&calls, "main")); err != nil {
		t.Fatal(err)
	}

	if err := moveToLead(home, "", aliveOnly(&calls)); err != nil {
		t.Fatal(err)
	}

	if calls != 1 {
		t.Errorf("alive ran %d times, want once", calls)
	}
	if got, err := os.Readlink(filepath.Join(home, "sessions", "main")); err != nil || got != "lead" {
		t.Errorf("sessions/main links to %q, %v", got, err)
	}
}

func TestMoveToLeadOfANewFactoryAsksNothing(t *testing.T) {
	home := t.TempDir()
	calls := 0

	if err := moveToLead(home, "", aliveOnly(&calls)); err != nil {
		t.Fatal(err)
	}

	if calls != 0 {
		t.Errorf("alive ran %d times, want never", calls)
	}
}

func TestAFailedAliveStopsTheMoveBeforeItChangesAnything(t *testing.T) {
	home := oldFactory(t)

	err := moveToLead(home, "", func(string) (bool, error) { return false, errors.New("no multiplexer") })

	if err == nil {
		t.Fatal("the move went on without an answer from alive")
	}
	if info, err := os.Lstat(filepath.Join(home, "sessions", "main")); err != nil || !info.IsDir() {
		t.Errorf("sessions/main changed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "sessions", "lead")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sessions/lead: %v, want none", err)
	}
}

func TestAMovedTokenAuthenticatesTheOldNameOnce(t *testing.T) {
	home := oldFactory(t)
	token, err := identity.Folder{Home: home}.Issue("main")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := moveToLead(home, "", aliveOnly(&calls, "main")); err != nil {
		t.Fatal(err)
	}

	got, err := identity.Folder{Home: home}.Authenticate(protocol.Caller{Agent: "main", Token: token})

	if err != nil || got != "lead" {
		t.Fatalf("got %q, %v, want lead", got, err)
	}
}

func TestRestartKeepsALeadThatRunsUnderTheOldName(t *testing.T) {
	r := newRig(t)
	home := r.o.home
	writeTestFile(t, filepath.Join(home, "sessions", "main", "conversation-id"), "11111111-2222-4333-8444-555555555555\n")
	writeTestFile(t, filepath.Join(home, "main-prompt.md"), "the old prompt\n")
	r.m.answering = true
	r.m.alive["sfy"] = true
	r.m.alive["main"] = true
	m := mux.Registered(r.m, "lead", leadAddressPath(home), nil, registration.Path(home))
	r.o.mux = m
	r.o.launcher = registeredLauncher{r.l, m}
	calls := 0
	r.o.move = func() error { return moveToLead(home, "", aliveOnly(&calls, "main")) }

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	if len(r.l.specs) != 0 || len(r.l.commands) != 0 {
		t.Errorf("restart started a second lead: %v %v", r.l.specs, r.l.commands)
	}
	want := "sfy restarted. Your prompt changed. Read the new text in " + filepath.Join(home, "lead-prompt.md") + " with the Read tool."
	if len(r.calls) != 1 || r.calls[0].Body != want {
		t.Errorf("requests %v, want the note %q", r.calls, want)
	}
}

func TestServeFailsWhenItCannotTellWhetherTheOldLeadRuns(t *testing.T) {
	home := oldFactory(t)
	writeTestFile(t, filepath.Join(home, "config.toml"), "multiplexer = \"broken\"\n[multiplexers.broken]\nstart = \"true\"\nsend = \"true\"\nalive = \"exit 3\"\nstop = \"true\"\n")
	env := func(k string) string { return map[string]string{"HOME": t.TempDir(), "SFY_HOME": home}[k] }

	err := Serve(context.Background(), env, io.Discard)

	var moved *moveError
	// The factory folder is not the default one, so the terminal name has the folder's prefix.
	if !errors.As(err, &moved) || !strings.HasSuffix(moved.Terminal, "-main") {
		t.Fatalf("got %v, want a moveError for the prefixed main", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "sessions", "lead")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sessions/lead: %v, want none", err)
	}
}
