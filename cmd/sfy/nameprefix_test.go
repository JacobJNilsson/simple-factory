package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/up"
)

// inEmptyFolder runs sfy with a name prefix in a working folder below a home folder that has no factory.
func inEmptyFolder(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	home := t.TempDir()
	work := filepath.Join(home, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(before) })
	env := func(k string) string {
		switch k {
		case "HOME":
			return home
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

// inFolderOfAnotherPrefix runs plain sfy in a working folder whose one factory has the name prefix ex-.
// It returns the other factory folder and the factories that exist afterwards.
func inFolderOfAnotherPrefix(t *testing.T, args ...string) (code int, stdout, stderr, other string, factories int) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(home, "web")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if other, err = config.CreateFactory(home, work, "ex-"); err != nil {
		t.Fatal(err)
	}
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(before) })
	env := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	var out, errb bytes.Buffer
	a := app{getenv: env, stdout: &out, stderr: &errb}
	a.start = func(mode up.Mode) error { return up.Command(nil, env, &out, &errb, mode) }
	code = a.run(args)
	entries, err := os.ReadDir(config.FactoriesDir(home))
	if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String(), other, len(entries)
}

func TestUpAndRestartRefuseAFolderWithAFactoryOfAnotherNamePrefix(t *testing.T) {
	for _, args := range [][]string{{"up"}, {"up", "--headless"}, {"restart"}, {"restart", "--headless"}} {
		code, stdout, stderr, other, factories := inFolderOfAnotherPrefix(t, args...)

		if code != 1 || stdout != "" {
			t.Errorf("%v: code %d, stdout %q, want 1 and nothing", args, code, stdout)
		}
		for _, want := range []string{other, "SFY_NAME_PREFIX=ex-", "SFY_HOME", "sfy up --new-factory"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%v: stderr %q lacks %q", args, stderr, want)
			}
		}
		if factories != 1 {
			t.Errorf("%v: %d factories exist, want only the other one", args, factories)
		}
	}
}

func TestFactoryFolderCreateStillMakesAFactoryBesideAnotherNamePrefix(t *testing.T) {
	code, stdout, stderr, other, factories := inFolderOfAnotherPrefix(t, "factory-folder", "--create")

	if code != 0 || strings.TrimSpace(stdout) == other || factories != 2 {
		t.Errorf("code %d, stdout %q, stderr %q, %d factories, want 0, a new folder and 2", code, stdout, stderr, factories)
	}
}

func TestFactoryFolderCreateMakesAFactoryWhereNoneExists(t *testing.T) {
	code, stdout, stderr := inEmptyFolder(t, "factory-folder", "--create")

	if code != 0 {
		t.Fatalf("code %d, stderr %q, want 0", code, stderr)
	}
	folder := strings.TrimSpace(stdout)
	if folder == "" {
		t.Fatal("stdout is empty, want the new factory folder")
	}
	for _, f := range []string{"folder", "name-prefix"} {
		if _, err := os.Stat(filepath.Join(folder, f)); err != nil {
			t.Errorf("factory folder %s lacks %s: %v", folder, f, err)
		}
	}
}

func TestFactoryFolderWithoutCreateFailsWhereNoFactoryExists(t *testing.T) {
	code, stdout, stderr := inEmptyFolder(t, "factory-folder")

	if code != 1 {
		t.Errorf("code %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("stdout %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "no factory with the name prefix lfy- here") {
		t.Errorf("stderr %q, want the NoFactoryError text", stderr)
	}
}

func TestACommandFailsOnABadNamePrefixButHelpAndVersionDoNot(t *testing.T) {
	env := func(k string) string {
		if k == "SFY_NAME_PREFIX" {
			return "EX"
		}
		return ""
	}
	for _, c := range []struct {
		args []string
		code int
	}{{[]string{"status"}, 1}, {[]string{"config"}, 1}, {[]string{"help"}, 0}, {[]string{"--version"}, 0}} {
		var out, errb bytes.Buffer
		a := app{getenv: env, stdout: &out, stderr: &errb}

		code := a.run(c.args)

		if code != c.code {
			t.Errorf("%v: code %d, want %d", c.args, code, c.code)
		}
		if c.code == 1 && !strings.Contains(errb.String(), "SFY_NAME_PREFIX") {
			t.Errorf("%v: stderr %q, want it to name SFY_NAME_PREFIX", c.args, errb.String())
		}
	}
}
