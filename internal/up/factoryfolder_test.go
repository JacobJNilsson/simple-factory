package up

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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

	err := FactoryFolder(factoryTestEnv(home, ""), &out, &bytes.Buffer{}, false)

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

func TestFactoryFolderWithCreateMakesTheFactoryFolderAndPrintsIt(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(home, "work", "acme")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	inFolder(t, dir)
	var out bytes.Buffer
	want := filepath.Join(home, ".sfy", "factories", "ex-acme")

	err := FactoryFolder(prefixedEnv(home, "ex-"), &out, &bytes.Buffer{}, true)

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
	if err := FactoryFolder(prefixedEnv(home, "ex-"), &out, &bytes.Buffer{}, false); err != nil || out.String() != want+"\n" {
		t.Errorf("a second run without create printed %q, %v, want %q", out.String(), err, want)
	}
}
