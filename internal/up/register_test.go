package up

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

func registerRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.o.whoami = func() (string, error) { return "dev", nil }
	r.o.session = func() string { return "s-1" }
	return r
}

func TestRegisterRefusesWhenALeadRuns(t *testing.T) {
	r := registerRig(t)
	r.m.alive["lead"] = true

	err := register(r.o)

	if !errors.Is(err, ErrLeadRuns) {
		t.Fatalf("got %v", err)
	}
	if _, ok := registration.Read(r.o.home); ok || len(r.m.starts) != 0 {
		t.Errorf("a refused register changed state, starts %v", r.m.starts)
	}
}

func TestRegisterRefusesWithoutWhoami(t *testing.T) {
	for name, whoami := range map[string]func() (string, error){
		"no template":   nil,
		"empty address": func() (string, error) { return "", nil },
	} {
		r := registerRig(t)
		r.o.whoami = whoami

		err := register(r.o)

		if !errors.Is(err, ErrNoWhoami) {
			t.Fatalf("%s: got %v", name, err)
		}
		if len(r.m.starts) != 0 {
			t.Errorf("%s: the sfy process started: %v", name, r.m.starts)
		}
	}
}

func TestRegisterRefusesWithoutASession(t *testing.T) {
	r := registerRig(t)
	r.o.session = func() string { return "" }

	if err := register(r.o); !errors.Is(err, ErrNoSession) {
		t.Fatalf("got %v", err)
	}
}

func TestRegisterRecordsTheSessionAndTheAddressAndStartsTheSfyProcess(t *testing.T) {
	r := registerRig(t)
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	if err := register(r.o); err != nil {
		t.Fatal(err)
	}

	got, ok := registration.Read(r.o.home)
	if !ok || got.ID != "s-1" || got.Dir != dir {
		t.Errorf("record %+v, %v", got, ok)
	}
	if addr, ok := readAddress(t, r.o); !ok || addr != "dev" {
		t.Errorf("address %q, %v", addr, ok)
	}
	if len(r.m.starts) != 1 || r.m.starts[0][0] != "sfy" {
		t.Errorf("starts %v, want the sfy process only", r.m.starts)
	}
	if len(r.l.specs) != 0 || len(r.l.commands) != 0 {
		t.Errorf("sfy started a lead: %v %v", r.l.specs, r.l.commands)
	}
	if _, err := os.Stat(filepath.Join(r.o.home, "lead-prompt.md")); err != nil {
		t.Errorf("lead-prompt.md: %v", err)
	}
}

func TestRegisterKeepsARunningSfyProcess(t *testing.T) {
	r := registerRig(t)
	r.m.answering = true

	if err := register(r.o); err != nil {
		t.Fatal(err)
	}

	if len(r.m.starts) != 0 {
		t.Errorf("starts %v", r.m.starts)
	}
}

func TestRegisterPrintsThePromptWithAdditions(t *testing.T) {
	r := registerRig(t)
	dir := filepath.Join(r.o.home, "prompts", "lead.d")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("Extra rule for this operator.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := register(r.o); err != nil {
		t.Fatal(err)
	}

	out := r.out.String()
	if first, _, _ := strings.Cut(out, "\n"); first != registerIntro {
		t.Errorf("first line %q", first)
	}
	for _, want := range []string{"Extra rule for this operator.", "sfy inbox", "notes.md", r.o.home} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q", want)
		}
	}
}

func TestRegisterPrintsTheLayersMemoryPartAndCreatesNoNotesFile(t *testing.T) {
	r := registerRig(t)
	writeTestFile(t, filepath.Join(r.o.home, "prompts", "lead-memory.md"), "Keep your memory in the canvas.\n")

	if err := register(r.o); err != nil {
		t.Fatal(err)
	}

	if out := r.out.String(); !strings.Contains(out, "Keep your memory in the canvas.") || strings.Contains(out, "notes.md") {
		t.Errorf("output %q", out)
	}
	if _, err := os.Stat(filepath.Join(r.o.home, "notes.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("notes.md exists or fails to stat: %v", err)
	}
}

func TestRegisterWorksAgainAfterTheRegistrationWasCleared(t *testing.T) {
	r := registerRig(t)
	if err := register(r.o); err != nil {
		t.Fatal(err)
	}
	if err := (registration.Folder{Home: r.o.home}).Clear(); err != nil {
		t.Fatal(err)
	}
	r.o.session = func() string { return "s-2" }

	if err := register(r.o); err != nil {
		t.Fatal(err)
	}

	if got, _ := registration.Read(r.o.home); got.ID != "s-2" {
		t.Errorf("record %+v", got)
	}
}
