package up

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

func factoryTestEnv(home, sfyHome string) func(string) string {
	return func(k string) string {
		switch k {
		case "HOME":
			return home
		case "SFY_HOME":
			return sfyHome
		}
		return ""
	}
}

func TestFactoryEnvCreatesAFactoryAndJoinsItFromASubfolder(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(home, "Developer", "acme")
	sub := filepath.Join(dir, "web")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	env, err := factoryEnv(factoryTestEnv(home, ""), func() (string, error) { return dir, nil })
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".sfy", "factories", "acme")
	if got := env("SFY_HOME"); got != want {
		t.Fatalf("SFY_HOME %q, want %q", got, want)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || entries[0].Name() != "web" {
		t.Errorf("the folder holds %v, want only web", entries)
	}

	env, err = factoryEnv(factoryTestEnv(home, ""), func() (string, error) { return sub, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := env("SFY_HOME"); got != want {
		t.Errorf("from a subfolder SFY_HOME %q, want %q", got, want)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".sfy", "factories")); len(entries) != 1 {
		t.Errorf("%d factories exist, want 1", len(entries))
	}
}

func TestFactoryEnvKeepsSFYHOMEAndUsesTheDefaultInTheHomeFolder(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	getwd := func() (string, error) { return home, nil }

	env, err := factoryEnv(factoryTestEnv(home, "/custom"), getwd)
	if err != nil || env("SFY_HOME") != "/custom" {
		t.Errorf("override: SFY_HOME %q, err %v", env("SFY_HOME"), err)
	}
	env, err = factoryEnv(factoryTestEnv(home, ""), getwd)
	if err != nil || env("SFY_HOME") != filepath.Join(home, ".sfy") {
		t.Errorf("home folder: SFY_HOME %q, err %v", env("SFY_HOME"), err)
	}
	if _, err := os.Stat(filepath.Join(home, ".sfy", "factories")); err == nil {
		t.Error("the home folder created a factory")
	}
}

func TestMainAgentWorksInTheWorkingFolder(t *testing.T) {
	if got := (options{home: "/s", dir: "/w"}).mainDir(); got != "/w" {
		t.Errorf("factory: %q, want /w", got)
	}
	if got := (options{home: "/s"}).mainDir(); got != "/s" {
		t.Errorf("default: %q, want /s", got)
	}
}

func TestSocketPathOfALongNamedFactoryStaysWithinTheLimit(t *testing.T) {
	long := config.FactoryName("/h/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	path := socket(filepath.Join("/Users/someoneelse", ".sfy", "factories", long))
	if err := checkSocketPath(path); err != nil {
		t.Errorf("socket path %q: %v", path, err)
	}
}

func TestShowMainGetsTheFolderRulesFactory(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(home, "Developer", "acme")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	// The caller's env has no SFY_HOME, so only the folder rules can name the factory.
	t.Setenv("SFY_HOME", "")
	cfg := "multiplexer = \"probe\"\n\n[multiplexers.probe]\nstart = \"true\"\nsend = \"true\"\nalive = \"true\"\nstop = \"true\"\n" +
		"show_main = \"printf '%s|%s' {home} \\\"$SFY_HOME\\\" > '" + out + "'\"\n"
	env, err := factoryEnv(factoryTestEnv(home, ""), func() (string, error) { return dir, nil })
	if err != nil {
		t.Fatal(err)
	}
	state := env("SFY_HOME")
	if err := os.WriteFile(filepath.Join(state, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := newParts(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.mux.ShowMain("main"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if want := state + "|" + state; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
