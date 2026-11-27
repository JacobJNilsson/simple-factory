package up

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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

	env, err := factoryEnv(factoryTestEnv(home, ""), func() (string, error) { return dir, nil }, io.Discard, true)
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

	env, err = factoryEnv(factoryTestEnv(home, ""), func() (string, error) { return sub, nil }, io.Discard, true)
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

	env, err := factoryEnv(factoryTestEnv(home, "/custom"), getwd, io.Discard, true)
	if err != nil || env("SFY_HOME") != "/custom" {
		t.Errorf("override: SFY_HOME %q, err %v", env("SFY_HOME"), err)
	}
	env, err = factoryEnv(factoryTestEnv(home, ""), getwd, io.Discard, true)
	if err != nil || env("SFY_HOME") != filepath.Join(home, ".sfy") {
		t.Errorf("home folder: SFY_HOME %q, err %v", env("SFY_HOME"), err)
	}
	if _, err := os.Stat(filepath.Join(home, ".sfy", "factories")); err == nil {
		t.Error("the home folder created a factory")
	}
}

func prefixedEnv(home, prefix string) func(string) string {
	return func(k string) string {
		if k == config.NamePrefixEnv {
			return prefix
		}
		return factoryTestEnv(home, "")(k)
	}
}

func TestFactoryEnvMakesOneFactoryForEachNamePrefixInOneFolder(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(home, "Developer", "acme")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	getwd := func() (string, error) { return dir, nil }
	var note bytes.Buffer
	plain := filepath.Join(home, ".sfy", "factories", "acme")
	other := filepath.Join(home, ".sfy", "factories", "ex-acme")

	first, err := factoryEnv(prefixedEnv(home, ""), getwd, &note, true)
	if err != nil || first("SFY_HOME") != plain || note.Len() != 0 {
		t.Fatalf("first: SFY_HOME %q, err %v, note %q, want %q and no note", first("SFY_HOME"), err, note.String(), plain)
	}
	// A wrapper's first start goes through sfy factory-folder --create, which makes the factory beside another name prefix.
	second, err := factoryEnv(prefixedEnv(home, "ex-"), getwd, &note, false)
	if err != nil || second("SFY_HOME") != other {
		t.Fatalf("second: SFY_HOME %q, err %v, want %q", second("SFY_HOME"), err, other)
	}
	if !strings.Contains(note.String(), plain) || strings.Count(note.String(), "\n") != 1 {
		t.Errorf("note %q, want one line that names %s", note.String(), plain)
	}
	again, err := factoryEnv(prefixedEnv(home, ""), getwd, io.Discard, true)
	if err != nil || again("SFY_HOME") != plain {
		t.Errorf("a second plain sfy: SFY_HOME %q, err %v, want %q", again("SFY_HOME"), err, plain)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".sfy", "factories")); len(entries) != 2 {
		t.Errorf("%d factories exist, want 2", len(entries))
	}
}

// folderWithAnotherPrefix returns a user home and a working folder whose one factory has the name prefix ex-.
func folderWithAnotherPrefix(t *testing.T) (home, dir, other string) {
	t.Helper()
	home, _ = filepath.EvalSymlinks(t.TempDir())
	dir = filepath.Join(home, "Developer", "web")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	other, err := config.CreateFactory(home, dir, "ex-")
	if err != nil {
		t.Fatal(err)
	}
	return home, dir, other
}

func TestFactoryEnvRefusesAFolderWithFactoriesOfOtherNamePrefixesOnly(t *testing.T) {
	home, dir, other := folderWithAnotherPrefix(t)

	_, err := factoryEnv(prefixedEnv(home, ""), func() (string, error) { return dir, nil }, io.Discard, true)

	var refused *otherPrefixError
	if !errors.As(err, &refused) || refused.prefix != "" || len(refused.others) != 1 || refused.others[0] != other {
		t.Fatalf("error %#v, want an otherPrefixError that names %s", err, other)
	}
	if entries, _ := os.ReadDir(config.FactoriesDir(home)); len(entries) != 1 {
		t.Errorf("%d factories exist, want only the other one", len(entries))
	}
}

func TestOtherPrefixTextNamesEachFactoryAndHowToReachIt(t *testing.T) {
	text := otherPrefixText("", []string{"/f/ex-web", "/f/web"})

	for _, want := range []string{
		"no factory without a name prefix",
		"/f/ex-web has no name prefix. Run sfy with SFY_NAME_PREFIX unset, or set SFY_HOME=/f/ex-web.",
		"Run sfy up --new-factory to make a factory without a name prefix here.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text %q lacks %q", text, want)
		}
	}
}

func TestOtherPrefixTextNamesTheWrapperOfAPrefix(t *testing.T) {
	_, _, other := folderWithAnotherPrefix(t)

	text := otherPrefixText("", []string{other})

	want := other + " has the name prefix ex-. Run the wrapper that sets SFY_NAME_PREFIX=ex-, or set SFY_HOME=" + other + "."
	if !strings.Contains(text, want) {
		t.Errorf("text %q lacks %q", text, want)
	}
}

func TestFactoryEnvWithNewFactoryCreatesItBesideAnotherNamePrefix(t *testing.T) {
	home, dir, _ := folderWithAnotherPrefix(t)

	env, err := factoryEnv(prefixedEnv(home, ""), func() (string, error) { return dir, nil }, io.Discard, false)

	if want := filepath.Join(config.FactoriesDir(home), "web"); err != nil || env("SFY_HOME") != want {
		t.Fatalf("SFY_HOME %q, err %v, want %q", env("SFY_HOME"), err, want)
	}
}

func TestFactoryEnvWithSFYHOMEIgnoresTheOtherNamePrefix(t *testing.T) {
	home, dir, _ := folderWithAnotherPrefix(t)
	env := func(k string) string {
		if k == "SFY_HOME" {
			return filepath.Join(home, "chosen")
		}
		return prefixedEnv(home, "")(k)
	}
	getwd := func() (string, error) { return dir, nil }

	got, err := factoryEnv(env, getwd, io.Discard, true)
	if err != nil || got("SFY_HOME") != filepath.Join(home, "chosen") {
		t.Errorf("SFY_HOME %q, err %v", got("SFY_HOME"), err)
	}
	if err := checkRestartFactory(env, getwd); err != nil {
		t.Errorf("restart: %v", err)
	}
}

func TestRestartRefusesAFolderWithFactoriesOfOtherNamePrefixesOnly(t *testing.T) {
	home, dir, other := folderWithAnotherPrefix(t)

	err := checkRestartFactory(prefixedEnv(home, ""), func() (string, error) { return dir, nil })

	var refused *otherPrefixError
	if !errors.As(err, &refused) || len(refused.others) != 1 || refused.others[0] != other {
		t.Fatalf("error %v, want an otherPrefixError that names %s", err, other)
	}
}

func TestRestartWithANamePrefixAndNoFactoryFailsAsALookup(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())

	err := checkRestartFactory(prefixedEnv(home, "ex-"), func() (string, error) { return home, nil })

	var none *config.NoFactoryError
	if !errors.As(err, &none) {
		t.Errorf("error %v, want a NoFactoryError", err)
	}
}

func TestFactoryEnvWithANamePrefixFailsInTheHomeFolderAndOnABadValue(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	getwd := func() (string, error) { return home, nil }

	_, err := factoryEnv(prefixedEnv(home, "ex-"), getwd, io.Discard, true)
	var none *config.NoFactoryError
	if !errors.As(err, &none) {
		t.Errorf("home folder: error %v, want a NoFactoryError", err)
	}
	_, err = factoryEnv(prefixedEnv(home, "EX"), getwd, io.Discard, true)
	var bad *config.NamePrefixError
	if !errors.As(err, &bad) {
		t.Errorf("bad value: error %v, want a NamePrefixError", err)
	}
}

func TestLeadWorksInTheWorkingFolder(t *testing.T) {
	if got := (options{home: "/s", dir: "/w"}).leadDir(); got != "/w" {
		t.Errorf("factory: %q, want /w", got)
	}
	if got := (options{home: "/s"}).leadDir(); got != "/s" {
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

func TestShowLeadGetsTheFolderRulesFactory(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(home, "Developer", "acme")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	// The caller's env has no SFY_HOME, so only the folder rules can name the factory.
	t.Setenv("SFY_HOME", "")
	cfg := "multiplexer = \"probe\"\n\n[multiplexers.probe]\nstart = \"true\"\nsend = \"true\"\nalive = \"true\"\nstop = \"true\"\n" +
		"show_lead = \"printf '%s|%s' {home} \\\"$SFY_HOME\\\" > '" + out + "'\"\n"
	env, err := factoryEnv(factoryTestEnv(home, ""), func() (string, error) { return dir, nil }, io.Discard, true)
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
	if err := p.mux.ShowLead("lead"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if want := state + "|" + state; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
