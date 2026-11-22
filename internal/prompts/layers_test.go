package prompts_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/prompts"
)

// twoLayers returns a machine layer and a factory layer below one user home, lowest first.
func twoLayers(t *testing.T) (machine, factory string) {
	t.Helper()
	userHome := t.TempDir()
	machine = filepath.Join(userHome, ".sfy")
	factory = filepath.Join(machine, "factories", "acme")
	if err := os.MkdirAll(factory, 0o700); err != nil {
		t.Fatal(err)
	}
	return machine, factory
}

func defaultWorker(t *testing.T) string {
	t.Helper()
	text, err := prompts.For("worker", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(text, "\n")
}

func TestForGivesAFactoryTheMachineAdditions(t *testing.T) {
	machine, factory := twoLayers(t)
	writePrompt(t, machine, "worker.d/rules.md", "machine rule")

	got, err := prompts.For("worker", []string{machine, factory})

	if err != nil || got != defaultWorker(t)+"\n\nmachine rule" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForLetsAFactoryFileReplaceTheMachineFileOfItsName(t *testing.T) {
	machine, factory := twoLayers(t)
	writePrompt(t, machine, "worker.d/a.md", "machine a")
	writePrompt(t, machine, "worker.d/b.md", "machine b")
	writePrompt(t, factory, "worker.d/a.md", "factory a")

	got, err := prompts.For("worker", []string{machine, factory})

	if err != nil || got != defaultWorker(t)+"\n\nfactory a\n\nmachine b" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForDropsTheMachineFileThatAnEmptyFactoryFileNames(t *testing.T) {
	machine, factory := twoLayers(t)
	writePrompt(t, machine, "worker.d/a.md", "machine a")
	writePrompt(t, machine, "worker.d/b.md", "machine b")
	writePrompt(t, factory, "worker.d/a.md", "")

	got, err := prompts.For("worker", []string{machine, factory})

	if err != nil || got != defaultWorker(t)+"\n\nmachine b" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForTakesTheFactoryBaseAndTheAdditionsOfBothLayers(t *testing.T) {
	machine, factory := twoLayers(t)
	writePrompt(t, machine, "reviewer.md", "machine base")
	writePrompt(t, factory, "reviewer.md", "factory base")
	writePrompt(t, machine, "reviewer.d/1.md", "one")
	writePrompt(t, factory, "reviewer.d/2.md", "two")

	got, err := prompts.For("reviewer", []string{machine, factory})

	if err != nil || got != "factory base\n\none\n\ntwo" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForReadsALinkedAdditionThroughItsLink(t *testing.T) {
	machine, factory := twoLayers(t)
	target := filepath.Join(t.TempDir(), "shared.md")
	if err := os.WriteFile(target, []byte("linked"), 0o600); err != nil {
		t.Fatal(err)
	}
	writePrompt(t, factory, "lead.d/placeholder.md", "x")
	if err := os.Symlink(target, filepath.Join(factory, "prompts", "lead.d", "shared.md")); err != nil {
		t.Fatal(err)
	}

	got, err := prompts.For("lead", []string{machine, factory})

	if err != nil || !strings.HasSuffix(got, "\n\nx\n\nlinked") {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForReadsEachFileOfTheDefaultFactoryOnce(t *testing.T) {
	userHome := t.TempDir()
	def := config.DefaultHome(userHome)
	writePrompt(t, def, "worker.d/a.md", "once")

	got, err := prompts.For("worker", config.Layers(def, userHome))

	if err != nil || strings.Count(got, "once") != 1 {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestSourcesNamesTheBaseAndEachAdditionInAppendOrder(t *testing.T) {
	machine, factory := twoLayers(t)
	writePrompt(t, factory, "worker.md", "base")
	writePrompt(t, machine, "worker.d/a.md", "machine a")
	writePrompt(t, machine, "worker.d/b.md", "machine b")
	writePrompt(t, factory, "worker.d/b.md", "")

	base, additions, err := prompts.Sources("worker", []string{machine, factory})

	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(factory, "prompts", "worker.md"); base != want {
		t.Errorf("base %q, want %q", base, want)
	}
	want := []prompts.Addition{
		{Name: "a.md", File: filepath.Join(machine, "prompts", "worker.d", "a.md")},
		{Name: "b.md", File: filepath.Join(factory, "prompts", "worker.d", "b.md"), Removed: true},
	}
	if !reflect.DeepEqual(additions, want) {
		t.Errorf("additions %+v, want %+v", additions, want)
	}
}

func TestSourcesNamesTheDefaultBase(t *testing.T) {
	base, additions, err := prompts.Sources("lead", []string{t.TempDir()})

	if err != nil || base != prompts.DefaultBase || len(additions) != 0 {
		t.Fatalf("got %q, %v, %v", base, additions, err)
	}
}
