package envrun_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/envrun"
	"github.com/JacobJNilsson/simple-factory/internal/envrun/envruntest"
)

// TestMain lets the test binary act as the sfy run entry when a test re-executes it.
// The re-executed argv is: binary, a placeholder for the subcommand word, then the words after `sfy run`.
func TestMain(m *testing.M) {
	if os.Getenv("ENVRUN_AS_SFY") == "1" {
		if err := envrun.Run(os.Args[3:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

func wrap(t *testing.T, dir string, env []string, clean bool, cmd ...string) []string {
	t.Helper()
	argv, err := envrun.Wrap(dir, env, clean, "/opt/sfy", cmd)
	if err != nil {
		t.Fatal(err)
	}
	return argv
}

func TestWrapKeepsEnvironmentValuesOutOfArgv(t *testing.T) {
	argv := wrap(t, t.TempDir(), []string{"GITHUB_TOKEN=s3cret-token", "PATH=/usr/bin"}, true, "claude")

	if strings.Contains(strings.Join(argv, " "), "s3cret-token") {
		t.Errorf("argv holds a value: %v", argv)
	}
}

func TestWrapReturnsASfyRunCommand(t *testing.T) {
	argv := wrap(t, t.TempDir(), []string{"A=1"}, true, "claude", "--name", "main")

	c := envruntest.Parse(t, argv)

	if c.Bin != "/opt/sfy" || !c.Clean || !slices.Equal(c.Cmd, []string{"claude", "--name", "main"}) {
		t.Errorf("parsed %+v", c)
	}
}

func TestWrapOmitsTheCleanFlagWhenAsked(t *testing.T) {
	c := envruntest.Parse(t, wrap(t, t.TempDir(), []string{"A=1"}, false, "x"))

	if c.Clean {
		t.Error("clean flag set")
	}
}

func TestWrapWritesTheEnvironmentToTheFile(t *testing.T) {
	c := envruntest.Parse(t, wrap(t, t.TempDir(), []string{"A=1", "B=x\ny"}, true, "x"))

	if want := []string{"A=1", "B=x\ny"}; !slices.Equal(c.Env, want) {
		t.Errorf("env %q, want %q", c.Env, want)
	}
}

func TestWrapWritesTheEnvFileWithMode0600(t *testing.T) {
	c := envruntest.Parse(t, wrap(t, t.TempDir(), []string{"A=1"}, true, "x"))

	info, err := os.Stat(c.File)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestWrapWritesTheEnvFileInsideDir(t *testing.T) {
	dir := t.TempDir()

	c := envruntest.Parse(t, wrap(t, dir, []string{"A=1"}, true, "x"))

	if filepath.Dir(c.File) != dir {
		t.Errorf("file %s is outside %s", c.File, dir)
	}
}

// runAsSfy runs `sfy run` through the test binary and returns the environment the command printed.
func runAsSfy(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), os.Args[0], append([]string{"x"}, args...)...)
	cmd.Env = []string{"ENVRUN_AS_SFY=1", "OUTER=kept"}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestRunCleanExecsWithExactlyTheListedEnvironment(t *testing.T) {
	argv := wrap(t, t.TempDir(), []string{"PATH=/usr/bin:/bin", "TOKEN=a=b c"}, true, "env")

	out, err := runAsSfy(t, argv[1:]...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	got := strings.Split(strings.TrimSpace(out), "\n")
	slices.Sort(got)
	if want := []string{"PATH=/usr/bin:/bin", "TOKEN=a=b c"}; !slices.Equal(got, want) {
		t.Errorf("environment = %v, want %v", got, want)
	}
}

func TestRunDeletesTheEnvFile(t *testing.T) {
	dir := t.TempDir()
	argv := wrap(t, dir, []string{"PATH=/usr/bin:/bin"}, true, "env")

	if out, err := runAsSfy(t, argv[1:]...); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("env file stays: %v", left)
	}
}

func TestRunOverlayAddsToTheCurrentEnvironment(t *testing.T) {
	dir := t.TempDir()
	argv := wrap(t, dir, []string{"PATH=/usr/bin:/bin", "SFY_AGENT=main"}, false, "env")

	out, err := runAsSfy(t, argv[1:]...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	for _, want := range []string{"OUTER=kept", "SFY_AGENT=main"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %s in %q", want, out)
		}
	}
}

func TestRunFailsOnAMissingCommand(t *testing.T) {
	argv := wrap(t, t.TempDir(), []string{"PATH=/usr/bin"}, true, "no-such-command-xyz")

	if _, err := runAsSfy(t, argv[1:]...); err == nil {
		t.Error("run succeeded")
	}
}

func TestRunLeavesNoFileWhenTheCommandIsMissing(t *testing.T) {
	dir := t.TempDir()
	argv := wrap(t, dir, []string{"PATH=/usr/bin"}, true, "no-such-command-xyz")

	_, _ = runAsSfy(t, argv[1:]...)

	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("env file stays: %v", left)
	}
}

func TestWrapRemovesEnvFilesOfEarlierCalls(t *testing.T) {
	dir := t.TempDir()
	first := envruntest.Parse(t, wrap(t, dir, []string{"A=1"}, true, "x"))

	wrap(t, dir, []string{"A=2"}, true, "x")

	if _, err := os.Stat(first.File); err == nil {
		t.Error("the first env file stays")
	}
	if left, _ := os.ReadDir(dir); len(left) != 1 {
		t.Errorf("files: %v", left)
	}
}

func TestWrapTightensAWideSessionFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	wrap(t, dir, []string{"A=1"}, true, "x")

	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}
