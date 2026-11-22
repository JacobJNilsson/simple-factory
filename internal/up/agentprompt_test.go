package up

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/prompts"
)

const drafter = "[agents.drafter]\nharness = \"claude\"\n"

func TestShowConfigPrintsThePromptOfAnAgentOfItsOwn(t *testing.T) {
	env, _, home := configFactory(t)
	writeTestFile(t, filepath.Join(home, "config.toml"), drafter)
	writeTestFile(t, filepath.Join(home, "prompts", "drafter.md"), "Draft.\n")

	got := showConfig(t, env)

	if want := "# drafter: base " + filepath.Join(home, "prompts", "drafter.md") + "\n"; !strings.Contains(got, want) {
		t.Fatalf("output lacks %q:\n%s", want, got)
	}
}

func TestLoadDefinitionRefusesAnAgentWithoutAPrompt(t *testing.T) {
	_, userHome, home := configFactory(t)
	writeTestFile(t, filepath.Join(home, "config.toml"), drafter)

	_, _, err := loadDefinition(home, userHome)

	var missing *prompts.MissingError
	if !errors.As(err, &missing) || missing.Name != "drafter" {
		t.Fatalf("loadDefinition = %v, want a missing prompt drafter", err)
	}
}
