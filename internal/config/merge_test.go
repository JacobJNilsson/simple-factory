package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// These tests pin how the machine file and the factory file combine in the three map tables.

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
		"[session_rules.worker]\nallow = [\"Bash(a *)\"]\nexcluded_commands = [\"a *\"]\n[session_rules.main]\nallow = [\"Bash(m *)\"]\n",
		"[session_rules.worker]\nallow = [\"Bash(b *)\"]\n[session_rules.reviewer]\nallow = [\"Bash(r *)\"]\n",
	)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]config.SessionRule{
		"main":     {Allow: []string{"Bash(m *)"}},
		"worker":   {Allow: []string{"Bash(b *)"}},
		"reviewer": {Allow: []string{"Bash(r *)"}},
	}
	if !reflect.DeepEqual(got.SessionRules, want) {
		t.Fatalf("session_rules %+v, want %+v", got.SessionRules, want)
	}
}

func TestLoadFactoryMergesSessionSettingsAcrossTwoFiles(t *testing.T) {
	got, err := loadTwo(t,
		"[session_settings.worker]\nflag = \"machine\"\nlevel = 1\n[session_settings.worker.deep]\nx = 1\ny = 2\n[session_settings.main]\nkeep = true\n",
		"[session_settings.worker]\nflag = \"factory\"\n[session_settings.worker.deep]\nx = 3\n",
	)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]config.SessionValues{
		"main":   {"keep": true},
		"worker": {"flag": "factory", "deep": map[string]any{"x": int64(3)}},
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
		"tmux":  {"start": "factory start"},
		"herdr": {"notify": "n"},
	}
	if !reflect.DeepEqual(got.Multiplexers, want) {
		t.Fatalf("multiplexers %+v, want %+v", got.Multiplexers, want)
	}
}
