package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

func prefixEnv(home, prefix string) func(string) string {
	return func(k string) string {
		switch k {
		case "HOME":
			return home
		case config.NamePrefixEnv:
			return prefix
		}
		return ""
	}
}

func TestNamePrefixAcceptsAWellFormedValueAndNamesTheVariableOtherwise(t *testing.T) {
	for _, ok := range []string{"", "ex-", "a1-b2-"} {
		if got, err := config.NamePrefix(prefixEnv("/h", ok)); err != nil || got != ok {
			t.Errorf("NamePrefix(%q) = %q, %v, want it back with no error", ok, got, err)
		}
	}
	for _, bad := range []string{"EX-", "ex", "-", "ex_-", "abcdefghijklmnop-"} {
		_, err := config.NamePrefix(prefixEnv("/h", bad))
		var badValue *config.NamePrefixError
		if !errors.As(err, &badValue) || badValue.Value != bad {
			t.Errorf("NamePrefix(%q) error %v, want a NamePrefixError for it", bad, err)
		}
	}
}

func TestCreateFactoryPutsTheNamePrefixInFrontAndRecordsIt(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work", "web")

	state, err := config.CreateFactory(home, dir, "ex-")

	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(config.FactoriesDir(home), "ex-web"); state != want {
		t.Errorf("factory folder %q, want %q", state, want)
	}
	if b, err := os.ReadFile(filepath.Join(state, "name-prefix")); err != nil || string(b) != "ex-\n" {
		t.Errorf("name-prefix file %q, %v, want ex-", b, err)
	}
	plain, err := config.CreateFactory(home, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plain, "name-prefix")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an empty name prefix wrote a name-prefix file: %v", err)
	}
	if got, want := config.NamePrefixOf(state)+","+config.NamePrefixOf(plain), "ex-,"; got != want {
		t.Errorf("NamePrefixOf gives %q, want %q", got, want)
	}
}

func TestCreateFactoryCutsTheWholeNameToTheLimit(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "a-very-long-folder-name-that-goes-past-the-limit")

	state, err := config.CreateFactory(home, dir, "ex-")

	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(state)
	if len(name) != 32 || !strings.HasPrefix(name, "ex-a-very-long") {
		t.Errorf("name %q has %d characters, want 32 that start with ex-a-very-long", name, len(name))
	}
}

func TestFindFactoryTakesTheFactoryOfTheNamePrefix(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work", "web")
	plain, _ := config.CreateFactory(home, dir, "")
	other, _ := config.CreateFactory(home, dir, "ex-")

	if got, ok := config.FindFactory(home, dir, ""); !ok || got != plain {
		t.Errorf("empty name prefix: got %q %v, want %q", got, ok, plain)
	}
	if got, ok := config.FindFactory(home, dir, "ex-"); !ok || got != other {
		t.Errorf("ex-: got %q %v, want %q", got, ok, other)
	}
	if got, ok := config.FindFactory(home, dir, "no-"); ok {
		t.Errorf("no-: got %q, want none", got)
	}
}

func TestFindFactoryGoesUpPastAFolderThatHasOnlyAFactoryOfAnotherNamePrefix(t *testing.T) {
	home := userHome(t)
	outer := mkdir(t, home, "work")
	inner := mkdir(t, outer, "web")
	outerState, _ := config.CreateFactory(home, outer, "ex-")
	if _, err := config.CreateFactory(home, inner, ""); err != nil {
		t.Fatal(err)
	}

	if got, ok := config.FindFactory(home, inner, "ex-"); !ok || got != outerState {
		t.Errorf("got %q %v, want the outer factory %q", got, ok, outerState)
	}
}

func TestAFactoryWithoutTheNamePrefixFileBelongsToTheEmptyNamePrefix(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work", "web")
	states := twoFactoriesAt(t, home, dir)

	if got, ok := config.FindFactory(home, dir, ""); !ok || got != states[0] {
		t.Errorf("empty name prefix: got %q %v, want the first of %v", got, ok, states)
	}
	if got, ok := config.FindFactory(home, dir, "ex-"); ok {
		t.Errorf("ex-: got %q, want none", got)
	}
}

func TestSessionPrefixesOfTwoFactoriesOfOneFolderDiffer(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work", "web")
	plain, _ := config.CreateFactory(home, dir, "")
	other, _ := config.CreateFactory(home, dir, "ex-")
	env := func(string) string { return home }

	if a, b := config.Prefix(plain, env), config.Prefix(other, env); a != "web-" || b != "ex-web-" {
		t.Errorf("prefixes %q and %q, want web- and ex-web-", a, b)
	}
}

func TestLookupWithANamePrefixFailsWhereTheDefaultFactoryWouldAnswer(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work", "web")
	if _, err := config.CreateFactory(home, dir, ""); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	for _, cwd := range []string{dir, home} {
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
		_, err := config.Lookup(prefixEnv(home, "ex-"))
		var none *config.NoFactoryError
		if !errors.As(err, &none) || none.Prefix != "ex-" {
			t.Errorf("in %s: error %v, want a NoFactoryError for ex-", cwd, err)
		}
	}
	if err := os.Chdir(home); err != nil {
		t.Fatal(err)
	}
	if got, err := config.Lookup(prefixEnv(home, "")); err != nil || got != config.DefaultHome(home) {
		t.Errorf("empty name prefix in the home folder: %q, %v, want the default factory", got, err)
	}
}
