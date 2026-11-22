package prompts_test

import (
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/prompts"
)

func TestForReadsTheOldBaseOfTheLeadWhenALayerHasNoNewOne(t *testing.T) {
	machine, factory := twoLayers(t)
	writePrompt(t, machine, "main.md", "old base")

	got, err := prompts.For("lead", []string{machine, factory})

	if err != nil || got != "old base" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForPrefersTheNewBaseOfTheLeadInTheSameLayer(t *testing.T) {
	machine, factory := twoLayers(t)
	writePrompt(t, factory, "main.md", "old base")
	writePrompt(t, factory, "lead.md", "new base")

	got, err := prompts.For("lead", []string{machine, factory})

	if err != nil || got != "new base" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForLetsAnOldBaseInAHigherLayerWinOverANewBaseInALowerLayer(t *testing.T) {
	machine, factory := twoLayers(t)
	writePrompt(t, machine, "lead.md", "machine base")
	writePrompt(t, factory, "main.md", "factory base")

	got, err := prompts.For("lead", []string{machine, factory})

	if err != nil || got != "factory base" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForAddsTheOldAdditionsOfTheLeadByFileName(t *testing.T) {
	machine, factory := twoLayers(t)
	writePrompt(t, factory, "lead.md", "base")
	writePrompt(t, factory, "main.d/a.md", "old a")
	writePrompt(t, factory, "main.d/b.md", "old b")
	writePrompt(t, factory, "lead.d/b.md", "new b")
	writePrompt(t, factory, "lead.d/c.md", "new c")

	got, err := prompts.For("lead", []string{machine, factory})

	if err != nil || got != "base\n\nold a\n\nnew b\n\nnew c" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestSourcesNamesTheOldFilesThatTheLeadReads(t *testing.T) {
	machine, factory := twoLayers(t)
	writePrompt(t, machine, "main.md", "old base")
	writePrompt(t, factory, "main.d/a.md", "old a")

	base, additions, err := prompts.Sources("lead", []string{machine, factory})

	if err != nil {
		t.Fatal(err)
	}
	if base != machine+"/prompts/main.md" || len(additions) != 1 || additions[0].File != factory+"/prompts/main.d/a.md" {
		t.Fatalf("base %q, additions %+v", base, additions)
	}
}
