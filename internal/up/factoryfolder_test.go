package up

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// inFolder changes into dir for the rest of the test.
func inFolder(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func TestFactoryFolderWithoutCreateMakesNothingInANewFolder(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(home, "work", "acme")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	inFolder(t, dir)
	var out bytes.Buffer

	err := FactoryFolder(factoryTestEnv(home, ""), &out, &bytes.Buffer{}, FindFolder)

	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".sfy") + "\n"; out.String() != want {
		t.Errorf("printed %q, want %q", out.String(), want)
	}
	if _, err := os.Stat(filepath.Join(home, ".sfy", "factories")); err == nil {
		t.Error("the command made a factory")
	}
}

// With check, a folder without a factory of the name prefix gives ErrNoFactory and prints nothing.
// The default factory counts as none, so a plain sfy gets the same answer as a wrapper with a name prefix.
func TestFactoryFolderWithCheckReportsNoFactory(t *testing.T) {
	for _, prefix := range []string{"", "ex-"} {
		home, _ := filepath.EvalSymlinks(t.TempDir())
		dir := filepath.Join(home, "work", "acme")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		inFolder(t, dir)
		var out bytes.Buffer

		err := FactoryFolder(prefixedEnv(home, prefix), &out, &bytes.Buffer{}, CheckFolder)

		if !errors.Is(err, ErrNoFactory) || out.String() != "" {
			t.Errorf("prefix %q: printed %q, %v, want nothing and ErrNoFactory", prefix, out.String(), err)
		}
	}
}

func TestFactoryFolderWithCheckInASubFolderPrintsTheParentsFactoryFolder(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	work := filepath.Join(home, "work", "acme")
	sub := filepath.Join(work, "src")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	want, err := config.CreateFactory(home, work, "ex-")
	if err != nil {
		t.Fatal(err)
	}
	inFolder(t, sub)
	var out bytes.Buffer

	err = FactoryFolder(prefixedEnv(home, "ex-"), &out, &bytes.Buffer{}, CheckFolder)

	if err != nil || out.String() != want+"\n" {
		t.Errorf("printed %q, %v, want %q", out.String(), err, want)
	}
}

func TestFactoryFolderWithCreateMakesTheFactoryFolderAndPrintsIt(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(home, "work", "acme")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	inFolder(t, dir)
	var out bytes.Buffer
	want := filepath.Join(home, ".sfy", "factories", "ex-acme")

	err := FactoryFolder(prefixedEnv(home, "ex-"), &out, &bytes.Buffer{}, CreateFolder)

	if err != nil {
		t.Fatal(err)
	}
	if out.String() != want+"\n" {
		t.Errorf("printed %q, want %q", out.String(), want)
	}
	if got, err := os.ReadFile(filepath.Join(want, "folder")); err != nil || string(got) != dir+"\n" {
		t.Errorf("folder file %q, %v, want %q", got, err, dir)
	}
	out.Reset()
	if err := FactoryFolder(prefixedEnv(home, "ex-"), &out, &bytes.Buffer{}, FindFolder); err != nil || out.String() != want+"\n" {
		t.Errorf("a second run without create printed %q, %v, want %q", out.String(), err, want)
	}
}
