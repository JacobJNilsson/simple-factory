package config_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

func TestLoadFactoryReadsTheOldKeysAsTheNewOnes(t *testing.T) {
	got, err := loadTwo(t,
		"[models]\nmain = \"m\"\n[session_rules.main]\nallow = [\"Bash(m *)\"]\n[multiplexers.set]\nshow_main = \"show it\"\n",
		"[session_settings.main]\nkeep = true\n",
	)
	if err != nil {
		t.Fatal(err)
	}

	if got.Models.Lead != "m" {
		t.Errorf("models.lead %q, want m", got.Models.Lead)
	}
	if rules := got.SessionRules["lead"].Allow; !slices.Equal(rules, []string{"Bash(m *)"}) {
		t.Errorf("session_rules.lead %v", rules)
	}
	if got.SessionSettings["lead"]["keep"] != true {
		t.Errorf("session_settings.lead %v", got.SessionSettings["lead"])
	}
	if got.Multiplexers["set"]["show_lead"] != "show it" || got.Multiplexers["set"]["show_main"] != "" {
		t.Errorf("multiplexers.set %v", got.Multiplexers["set"])
	}
}

func TestAHigherFileWithTheOldKeyChangesTheNewKeyOfALowerFile(t *testing.T) {
	got, err := loadTwo(t, "[models]\nlead = \"lower\"\n", "[models]\nmain = \"higher\"\n")
	if err != nil {
		t.Fatal(err)
	}

	if got.Models.Lead != "higher" {
		t.Errorf("models.lead %q, want higher", got.Models.Lead)
	}
}

func TestAFileThatSetsTheOldAndTheNewKeyFailsAndNamesBoth(t *testing.T) {
	home := userHome(t)
	path := filepath.Join(home, ".sfy", "config.toml")
	writeFile(t, path, "[models]\nmain = \"a\"\nlead = \"b\"\n")

	_, err := config.LoadFactory(filepath.Join(home, ".sfy"), home)

	var bad *config.FileError
	if !errors.As(err, &bad) || bad.Path != path {
		t.Fatalf("got %v, want a FileError for %s", err, path)
	}
	var both *config.BothKeysError
	if !errors.As(err, &both) || both.Old != "models.main" || both.New != "models.lead" {
		t.Errorf("got %v, want a BothKeysError for models.main and models.lead", err)
	}
}

func TestOldNamesListsEachOldKeyAndPromptName(t *testing.T) {
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "acme")
	machine := filepath.Join(home, ".sfy", "config.toml")
	writeFile(t, machine, "[multiplexers.set]\nshow_main = \"x\"\nstart = \"s\"\n")
	factory := filepath.Join(state, "config.toml")
	writeFile(t, factory, "[models]\nmain = \"m\"\n")
	writeFile(t, filepath.Join(state, "prompts", "main.d", "a.md"), "A\n")
	writeFile(t, filepath.Join(home, ".sfy", "prompts", "main.md"), "Base\n")

	got, err := config.OldNames(state, home)

	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		factory + " sets models.main. sfy reads it as models.lead. Rename the key.",
		machine + " sets multiplexers.set.show_main. sfy reads it as multiplexers.set.show_lead. Rename the key.",
		filepath.Join(state, "prompts", "main.d") + " has the old name. sfy reads it as lead.d. Rename the folder.",
		filepath.Join(home, ".sfy", "prompts", "main.md") + " has the old name. sfy reads it as lead.md. Rename the file.",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestOldNamesIsEmptyForAFactoryWithTheNewNames(t *testing.T) {
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "acme")
	writeFile(t, filepath.Join(state, "config.toml"), "[models]\nlead = \"m\"\n")
	writeFile(t, filepath.Join(state, "prompts", "lead.d", "a.md"), "A\n")

	got, err := config.OldNames(state, home)

	if err != nil || len(got) != 0 {
		t.Fatalf("got %q, %v, want no old names", got, err)
	}
}
