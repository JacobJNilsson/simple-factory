package taskcheck_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/taskcheck"
)

func TestWrapOnLinuxBindsTheWritePathsAndHidesTheDeniedOnes(t *testing.T) {
	dir, tmp, secret, token := t.TempDir(), t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "sfy.sock")
	if err := os.WriteFile(token, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	argv, err := taskcheck.Sandbox{DenyRead: []string{secret, token, "/no/such/path"}}.Wrap("linux", dir, tmp, []string{"sh", "-c", "true"})

	if err != nil {
		t.Fatal(err)
	}
	line := " " + strings.Join(argv, " ") + " "
	for _, want := range []string{" bwrap ", " --ro-bind / / ", " --bind " + realPath(t, dir) + " ", " --tmpfs " + realPath(t, secret) + " ", " --ro-bind /dev/null " + realPath(t, token) + " ", " --chdir " + dir + " -- sh -c true "} {
		if !strings.Contains(line, want) {
			t.Errorf("argv lacks %q: %v", want, argv)
		}
	}
	if strings.Contains(line, "/no/such/path") {
		t.Errorf("argv names a missing path: %v", argv)
	}
}

func TestWrapOnMacOSWritesTheProfileForSandboxExec(t *testing.T) {
	dir, tmp, secret := t.TempDir(), t.TempDir(), t.TempDir()

	argv, err := taskcheck.Sandbox{DenyRead: []string{secret}}.Wrap("darwin", dir, tmp, []string{"sh", "-c", "true"})

	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != "sandbox-exec" || argv[1] != "-p" || !slices.Equal(argv[3:], []string{"--", "sh", "-c", "true"}) {
		t.Fatalf("argv = %v", argv)
	}
	for _, want := range []string{"(deny file-write*)", `(allow file-write* (subpath "` + realPath(t, dir) + `"))`, `(deny file-read* (subpath "` + realPath(t, secret) + `"))`} {
		if !strings.Contains(argv[2], want) {
			t.Errorf("profile lacks %q:\n%s", want, argv[2])
		}
	}
}

func TestWrapLetsAWorktreeWriteItsGitFolder(t *testing.T) {
	dir, tmp, gitDir := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	argv, err := taskcheck.Sandbox{}.Wrap("linux", dir, tmp, []string{"true"})

	if err != nil || !strings.Contains(strings.Join(argv, " "), " --bind "+realPath(t, gitDir)+" ") {
		t.Fatalf("argv = %v, err = %v", argv, err)
	}
}

func TestWrapRefusesAnOperatingSystemWithoutASandbox(t *testing.T) {
	if _, err := (taskcheck.Sandbox{}).Wrap("plan9", "/", "/", nil); err == nil {
		t.Fatal("want an error")
	}
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// TestRunInBwrapKeepsTheCommandInsideTheSandbox runs a real bwrap. It skips where bwrap cannot create a sandbox.
func TestRunInBwrapKeepsTheCommandInsideTheSandbox(t *testing.T) {
	if _, err := os.Stat("/usr/bin/bwrap"); err != nil {
		t.Skip("no bwrap")
	}
	probe := taskcheck.Run(context.Background(), t.TempDir(), "true", time.Minute, func(_, _ string, argv []string) ([]string, error) {
		return append([]string{"bwrap", "--unshare-user", "--ro-bind", "/", "/", "--"}, argv...), nil
	})
	if probe.Err != nil {
		t.Skipf("bwrap cannot run here: %v %s", probe.Err, probe.Output)
	}
	dir, outside, secret := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(secret, "key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	sb := taskcheck.Sandbox{DenyRead: []string{secret}}
	wrap := func(d, tmp string, argv []string) ([]string, error) { return sb.Wrap("linux", d, tmp, argv) }
	command := "touch ok && ! touch " + filepath.Join(outside, "no") + " && test ! -e " + filepath.Join(secret, "key")

	res := taskcheck.Run(context.Background(), dir, command, time.Minute, wrap)

	if res.Err != nil {
		t.Fatalf("err = %v, output = %q", res.Err, res.Output)
	}
	if _, err := os.Stat(filepath.Join(dir, "ok")); err != nil {
		t.Errorf("the command could not write its workspace: %v", err)
	}
}
