package prompts_test

import (
	"errors"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/prompts"
)

func TestForReadsThePromptOfAnyAgentName(t *testing.T) {
	machine, factory := twoLayers(t)
	writePrompt(t, factory, "loader.md", "Load the source.")
	writePrompt(t, machine, "loader.d/rules.md", "machine rule")

	got, err := prompts.For("loader", []string{machine, factory})

	if err != nil || got != "Load the source.\n\nmachine rule" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForNamesAnAgentPromptThatNoLayerHas(t *testing.T) {
	_, err := prompts.For("loader", []string{t.TempDir()})

	var missing *prompts.MissingError
	if !errors.As(err, &missing) || missing.Name != "loader" {
		t.Fatalf("For = %v, want a missing prompt loader", err)
	}
}

func TestHasFindsALayerFileOrAnEmbeddedDefault(t *testing.T) {
	_, factory := twoLayers(t)
	writePrompt(t, factory, "auditor.md", "Audit.")
	for name, want := range map[string]bool{"auditor": true, "reviewer": true, "loader": false} {
		if got, err := prompts.Has(name, []string{factory}); err != nil || got != want {
			t.Errorf("Has(%s) = %v, %v, want %v", name, got, err, want)
		}
	}
}
