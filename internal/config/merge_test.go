package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// These tests pin how the machine file and the factory file combine in the three map tables.
// The tables merge at every depth, so a key that only the machine file sets stays.

// loadTwo writes the machine config.toml and a factory's config.toml, then loads the factory.
func loadTwo(t *testing.T, machine, factory string) (config.Config, error) {
	t.Helper()
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "acme")
	if err := os.WriteFile(filepath.Join(home, ".sfy", "config.toml"), []byte(machine), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "config.toml"), []byte(factory), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.LoadFactory(state, home)
}

func TestLoadFactoryMergesSessionRulesAcrossTwoFiles(t *testing.T) {
	got, err := loadTwo(t,
		"[session_rules.worker]\nallow = [\"Bash(a *)\"]\nexcluded_commands = [\"a *\"]\n[session_rules.lead]\nallow = [\"Bash(m *)\"]\n",
		"[session_rules.worker]\nallow = [\"Bash(b *)\"]\n[session_rules.reviewer]\nallow = [\"Bash(r *)\"]\n",
	)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]config.SessionRule{
		"lead":     {Allow: []string{"Bash(m *)"}},
		"worker":   {Allow: []string{"Bash(b *)"}, ExcludedCommands: []string{"a *"}},
		"reviewer": {Allow: []string{"Bash(r *)"}},
	}
	if !reflect.DeepEqual(got.SessionRules, want) {
		t.Fatalf("session_rules %+v, want %+v", got.SessionRules, want)
	}
}

func TestLoadFactoryMergesSessionSettingsAcrossTwoFiles(t *testing.T) {
	got, err := loadTwo(t,
		"[session_settings.worker]\nflag = \"machine\"\nlevel = 1\n[session_settings.worker.deep]\nx = 1\ny = 2\n[session_settings.lead]\nkeep = true\n",
		"[session_settings.worker]\nflag = \"factory\"\n[session_settings.worker.deep]\nx = 3\n",
	)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]config.SessionValues{
		"lead":   {"keep": true},
		"worker": {"flag": "factory", "level": int64(1), "deep": map[string]any{"x": int64(3), "y": int64(2)}},
	}
	if !reflect.DeepEqual(got.SessionSettings, want) {
		t.Fatalf("session_settings %+v, want %+v", got.SessionSettings, want)
	}
}

func TestLoadFactoryMergesMultiplexersAcrossTwoFiles(t *testing.T) {
	got, err := loadTwo(t,
		"[multiplexers.tmux]\nstart = \"machine start\"\nkill = \"machine kill\"\n[multiplexers.herdr]\nnotify = \"n\"\n",
		"[multiplexers.tmux]\nstart = \"factory start\"\n",
	)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]map[string]string{
		"tmux":  {"start": "factory start", "kill": "machine kill"},
		"herdr": {"notify": "n"},
	}
	if !reflect.DeepEqual(got.Multiplexers, want) {
		t.Fatalf("multiplexers %+v, want %+v", got.Multiplexers, want)
	}
}

func TestLoadFactoryChangesANestedKeyAndKeepsItsSiblings(t *testing.T) {
	got, err := loadTwo(t, "[models]\nlead = \"m\"\nworker = \"w\"\n", "[models]\nworker = \"f\"\n")
	if err != nil {
		t.Fatal(err)
	}

	if want := (config.Models{Main: "m", Worker: "f", Reviewer: "sonnet"}); got.Models != want {
		t.Fatalf("models %+v, want %+v", got.Models, want)
	}
}

func TestLoadFactoryListReplacesTheMachineList(t *testing.T) {
	got, err := loadTwo(t, "allow = [\"git\", \"make\"]\n", "allow = [\"go\"]\n")
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got.Allow, []string{"go"}) {
		t.Fatalf("allow %v, want [go]", got.Allow)
	}
}

func TestLoadFactoryChecksTheMergedValues(t *testing.T) {
	_, err := loadTwo(t, "max_workers = 4\n", "max_workers = 0\n")

	assertSetting(t, err, "max_workers")
}

func TestLoadFactoryFailsNamingAnUnknownKeyOfEitherFile(t *testing.T) {
	_, err := loadTwo(t, "", "[models]\nwroker = \"x\"\n")

	assertSetting(t, err, "models.wroker")
}

func TestLoadFactoryNamesTheFileOfAValueWithTheWrongType(t *testing.T) {
	_, err := loadTwo(t, "", "max_workers = \"two\"\n")

	var bad *config.FileError
	if !errors.As(err, &bad) || !strings.HasSuffix(bad.Path, filepath.Join("factories", "acme", "config.toml")) {
		t.Fatalf("got %v, want a FileError that names the factory file", err)
	}
}

func TestLayersOfAFactoryAreTheMachineFolderThenTheFactoryFolder(t *testing.T) {
	home := userHome(t)
	state := filepath.Join(home, ".sfy", "factories", "acme")

	got := config.Layers(state, home)

	if want := []string{filepath.Join(home, ".sfy"), state}; !reflect.DeepEqual(got, want) {
		t.Fatalf("layers %v, want %v", got, want)
	}
}

func TestLayersOfTheDefaultFactoryAreOneFolder(t *testing.T) {
	home := userHome(t)

	got := config.Layers(filepath.Join(home, ".sfy")+"/", home)

	if want := []string{filepath.Join(home, ".sfy")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("layers %v, want %v", got, want)
	}
}

func TestSourcesNamesTheLastFileThatSetsEachKey(t *testing.T) {
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "acme")
	machine := filepath.Join(home, ".sfy", "config.toml")
	factory := filepath.Join(state, "config.toml")
	if err := os.WriteFile(machine, []byte("review_rounds = 4\nmax_workers = 3\n[models]\nworker = \"w\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(factory, []byte("max_workers = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := config.Sources(state, home)

	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"review_rounds": machine, "max_workers": factory, "models.worker": machine}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sources %v, want %v", got, want)
	}
}
