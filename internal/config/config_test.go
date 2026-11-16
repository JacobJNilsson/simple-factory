package config_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
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

func failingLookup() (string, error) { return "", errors.New("no getconf") }

func TestLoadGivesSpecDefaultsForAnEmptyFolder(t *testing.T) {
	home := t.TempDir()
	wantCache := "~/.cache"
	if runtime.GOOS == "darwin" {
		wantCache = "~/Library/Caches"
	}
	got, err := config.LoadWithTempLookup(home, failingLookup)

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
		Allow:         []string{"git", "make", "go", "gofmt"},
		DenyRead: []string{
			"~/.ssh", "~/.aws", "~/.gnupg",
			"~/.netrc", "~/.docker", "~/.kube", "~/.claude.json",
		},
		AllowWrite: []string{wantCache, "~/go/pkg/mod"},
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

func TestLoadReadsSessionSettingsPerKind(t *testing.T) {
	home := writeConfig(t, "[session_settings.worker]\nflag = \"x\"\nlevel = 2\n[session_settings.reviewer]\nflag = \"y\"\n")

	c, err := config.Load(home)

	if err != nil {
		t.Fatal(err)
	}
	if c.SessionSettings["worker"]["flag"] != "x" || c.SessionSettings["worker"]["level"] != int64(2) {
		t.Errorf("worker table: %v", c.SessionSettings["worker"])
	}
	if c.SessionSettings["reviewer"]["flag"] != "y" || len(c.SessionSettings["main"]) != 0 {
		t.Errorf("tables: %v", c.SessionSettings)
	}
}

func TestLoadRefusesAReservedSessionSettingsKeyNamingIt(t *testing.T) {
	for _, key := range []string{"permissions", "hooks", "sandbox"} {
		home := writeConfig(t, "[session_settings.worker]\n"+key+" = \"x\"\n")

		_, err := config.Load(home)

		var bad *config.SessionSettingsError
		if !errors.As(err, &bad) || bad.Kind != "worker" || bad.Key != key {
			t.Errorf("%s: want a SessionSettingsError naming the key, got %v", key, err)
		}
	}
}

func TestLoadReadsSessionRulesPerKind(t *testing.T) {
	home := writeConfig(t, "[session_rules.worker]\nallow = [\"Bash(tool open *)\"]\nexcluded_commands = [\"tool *\"]\n")

	c, err := config.Load(home)

	if err != nil {
		t.Fatal(err)
	}
	got := c.SessionRules["worker"]
	if len(got.Allow) != 1 || got.Allow[0] != "Bash(tool open *)" || len(got.ExcludedCommands) != 1 || got.ExcludedCommands[0] != "tool *" {
		t.Errorf("worker rules: %+v", got)
	}
}

func TestLoadRefusesAnUnknownSessionRulesKind(t *testing.T) {
	home := writeConfig(t, "[session_rules.workr]\nallow = [\"x\"]\n")

	_, err := config.Load(home)

	var bad *config.SessionSettingsError
	if !errors.As(err, &bad) || bad.Kind != "workr" {
		t.Fatalf("want a SessionSettingsError naming the kind, got %v", err)
	}
}

func TestLoadRefusesAnUnknownSessionSettingsKind(t *testing.T) {
	home := writeConfig(t, "[session_settings.workr]\nflag = \"x\"\n")

	_, err := config.Load(home)

	var bad *config.SessionSettingsError
	if !errors.As(err, &bad) || bad.Kind != "workr" || bad.Key != "" {
		t.Fatalf("want a SessionSettingsError naming the kind, got %v", err)
	}
}

func TestLoadFailsNamingResendMinutesBelowOne(t *testing.T) {
	home := writeConfig(t, "resend_minutes = 0\n")

	_, err := config.Load(home)

	assertSetting(t, err, "resend_minutes")
}

func TestLoadReadsSandboxPathLists(t *testing.T) {
	home := writeConfig(t, "deny_read = [\"~/.secrets\"]\nallow_write = [\"~/.cache/x\"]\n")

	got, err := config.Load(home)

	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.DenyRead, []string{"~/.secrets"}) || !reflect.DeepEqual(got.AllowWrite, []string{"~/.cache/x"}) {
		t.Fatalf("got deny_read %v, allow_write %v", got.DenyRead, got.AllowWrite)
	}
}

func TestLoadKeepsAnEmptySandboxListEmpty(t *testing.T) {
	home := writeConfig(t, "deny_read = []\nallow_write = []\n")

	got, err := config.Load(home)

	if err != nil {
		t.Fatal(err)
	}
	if len(got.DenyRead) != 0 || len(got.AllowWrite) != 0 {
		t.Fatalf("got deny_read %v, allow_write %v, want both empty", got.DenyRead, got.AllowWrite)
	}
}

func TestLoadReadsPassEnvAndDefaultsToNone(t *testing.T) {
	got, err := config.Load(writeConfig(t, "pass_env = [\"HTTPS_PROXY\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.PassEnv, []string{"HTTPS_PROXY"}) {
		t.Fatalf("pass_env = %v", got.PassEnv)
	}
	def, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(def.PassEnv) != 0 {
		t.Fatalf("default pass_env = %v", def.PassEnv)
	}
}

func TestLoadAllowsTheMacOSUserTempFolder(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the user temp folder default applies to macOS only")
	}
	out, err := exec.CommandContext(context.Background(), "getconf", "DARWIN_USER_TEMP_DIR").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimRight(strings.TrimSpace(string(out)), "/")

	got, err := config.Load(t.TempDir())

	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.AllowWrite, want) {
		t.Fatalf("allow_write %v lacks %q", got.AllowWrite, want)
	}
}

func TestLoadLeavesOutTheUserTempFolderWhenTheLookupFails(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the user temp folder default applies to macOS only")
	}

	got, err := config.LoadWithTempLookup(t.TempDir(), failingLookup)

	if err != nil {
		t.Fatal(err)
	}
	want := []string{"~/Library/Caches", "~/go/pkg/mod"}
	if !slices.Equal(got.AllowWrite, want) {
		t.Fatalf("allow_write %v, want %v", got.AllowWrite, want)
	}
}
