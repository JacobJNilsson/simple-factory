package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestLoadGivesSpecDefaultsForAnEmptyFolder(t *testing.T) {
	home := t.TempDir()

	got, err := config.Load(home)

	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		ReviewRounds:  3,
		MaxWorkers:    2,
		ResendMinutes: 5,
		Provider:      "claude",
		Multiplexer:   "tmux",
		Models:        config.Models{Main: "opus", Worker: "sonnet", Reviewer: "sonnet"},
		Allow:         []string{"git", "gh", "make", "go"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLoadKeepsDefaultsForSettingsLeftOut(t *testing.T) {
	home := writeConfig(t, "max_workers = 5\n")

	got, err := config.Load(home)

	if err != nil {
		t.Fatal(err)
	}
	want, _ := config.Load(t.TempDir())
	want.MaxWorkers = 5
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLoadReadsModelsAndMultiplexerSets(t *testing.T) {
	home := writeConfig(t, "[models]\nworker = \"haiku\"\n\n[multiplexers.tmux]\nstart = \"tmux new -d\"\n")

	got, err := config.Load(home)

	if err != nil {
		t.Fatal(err)
	}
	if got.Models.Worker != "haiku" || got.Models.Main != "opus" {
		t.Fatalf("models: %+v", got.Models)
	}
	if got.Multiplexers["tmux"]["start"] != "tmux new -d" {
		t.Fatalf("multiplexers: %+v", got.Multiplexers)
	}
}

func TestLoadFailsNamingReviewRoundsBelowOne(t *testing.T) {
	home := writeConfig(t, "review_rounds = 0\n")

	_, err := config.Load(home)

	assertSetting(t, err, "review_rounds")
}

func TestLoadFailsNamingMaxWorkersBelowOne(t *testing.T) {
	home := writeConfig(t, "max_workers = 0\n")

	_, err := config.Load(home)

	assertSetting(t, err, "max_workers")
}

func TestHomeUsesSfyHomeOtherwiseDotSfy(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	set := config.Home(env(map[string]string{"SFY_HOME": "/x", "HOME": "/h"}))
	unset := config.Home(env(map[string]string{"HOME": "/h"}))

	if set != "/x" || unset != filepath.Join("/h", ".sfy") {
		t.Fatalf("got %q and %q", set, unset)
	}
}

func assertSetting(t *testing.T, err error, setting string) {
	t.Helper()
	var bad *config.SettingError
	if !errors.As(err, &bad) || bad.Setting != setting {
		t.Fatalf("want a SettingError for %s, got %v", setting, err)
	}
}

func TestLoadFailsNamingAnUnknownKey(t *testing.T) {
	home := writeConfig(t, "max_workerz = 3\n")

	_, err := config.Load(home)

	assertSetting(t, err, "max_workerz")
}

func TestLoadFailsNamingAnUnknownKeyInATable(t *testing.T) {
	home := writeConfig(t, "[models]\nwroker = \"x\"\n")

	_, err := config.Load(home)

	assertSetting(t, err, "models.wroker")
}

func TestLoadAcceptsAKeyInAMultiplexerSet(t *testing.T) {
	home := writeConfig(t, "[multiplexers.herdr]\nnotify = \"x\"\n")

	_, err := config.Load(home)

	if err != nil {
		t.Fatal(err)
	}
}

func TestLoadFailsNamingResendMinutesBelowOne(t *testing.T) {
	home := writeConfig(t, "resend_minutes = 0\n")

	_, err := config.Load(home)

	assertSetting(t, err, "resend_minutes")
}
