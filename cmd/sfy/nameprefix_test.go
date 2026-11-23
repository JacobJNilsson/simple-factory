package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
