package main

// These tests check sfy factory-folder --exact in a sub-folder of a working folder whose factory has the same name prefix.
// The flag must answer for exactly the current folder, so the parent's factory never counts.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// nested is a working folder with an lfy- factory and a sub-folder of it, where the tests run sfy with the name prefix lfy-.
type nested struct {
	home, work, sub, parent string
}

func setupNested(t *testing.T) nested {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n := nested{home: home, work: filepath.Join(home, "work"), sub: filepath.Join(home, "work", "sub")}
	if err := os.MkdirAll(n.sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if n.parent, err = config.CreateFactory(home, n.work, "lfy-"); err != nil {
		t.Fatal(err)
	}
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(before) })
	return n
}

// runIn runs sfy with the name prefix lfy- in dir.
func (n nested) runIn(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string {
		switch k {
		case "HOME":
			return n.home
		case "SFY_NAME_PREFIX":
			return "lfy-"
		}
		return ""
	}
	var out, errb bytes.Buffer
	a := app{getenv: env, stdout: &out, stderr: &errb}
	code = a.run(args)
	return code, out.String(), errb.String()
}

func TestFactoryFolderCreateExactMakesAFactoryForTheSubFolder(t *testing.T) {
	n := setupNested(t)

	code, stdout, stderr := n.runIn(t, n.sub, "factory-folder", "--create", "--exact")

	if code != 0 {
		t.Fatalf("code %d, stderr %q, want 0", code, stderr)
	}
	made := filepath.Join(config.FactoriesDir(n.home), "lfy-sub")
	if stdout != made+"\n" {
		t.Errorf("stdout %q, want only the line %q", stdout, made)
	}
	if got := config.WorkFolder(made); got != n.sub {
		t.Errorf("the folder file of %s names %q, want %q", made, got, n.sub)
	}
}

func TestFactoryFolderCreateExactReturnsTheFactoryOfTheFolderOnASecondRun(t *testing.T) {
	n := setupNested(t)
	_, first, _ := n.runIn(t, n.sub, "factory-folder", "--create", "--exact")

	code, second, stderr := n.runIn(t, n.sub, "factory-folder", "--create", "--exact")

	if code != 0 || second != first {
		t.Errorf("code %d, stdout %q, stderr %q, want 0 and %q", code, second, stderr, first)
	}
}

func TestLookupFindsTheNearestFactoryAfterCreateExact(t *testing.T) {
	n := setupNested(t)
	_, made, _ := n.runIn(t, n.sub, "factory-folder", "--create", "--exact")

	_, fromSub, _ := n.runIn(t, n.sub, "factory-folder")
	_, fromWork, _ := n.runIn(t, n.work, "factory-folder")

	if fromSub != made {
		t.Errorf("the lookup in the sub-folder gives %q, want %q", fromSub, made)
	}
	if fromWork != n.parent+"\n" {
		t.Errorf("the lookup in the working folder gives %q, want %q", fromWork, n.parent)
	}
}

func TestFactoryFolderCheckExactCountsOnlyAFactoryOfTheFolderItself(t *testing.T) {
	n := setupNested(t)

	before, stdout, stderr := n.runIn(t, n.sub, "factory-folder", "--check", "--exact")
	if before != 4 || stdout != "" || stderr != "" {
		t.Errorf("before the create: code %d, stdout %q, stderr %q, want 4 and nothing", before, stdout, stderr)
	}
	_, made, _ := n.runIn(t, n.sub, "factory-folder", "--create", "--exact")
	after, stdout, stderr := n.runIn(t, n.sub, "factory-folder", "--check", "--exact")
	if after != 0 || stdout != made {
		t.Errorf("after the create: code %d, stdout %q, stderr %q, want 0 and %q", after, stdout, stderr, made)
	}
}
