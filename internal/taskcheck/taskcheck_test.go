package taskcheck_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/taskcheck"
)

// plain runs the command with no sandbox, so the tests work where a sandbox cannot nest.
func plain(_, _ string, argv []string) ([]string, error) { return argv, nil }

func run(t *testing.T, command string, timeout time.Duration) engine.CheckResult {
	t.Helper()
	return taskcheck.Run(context.Background(), t.TempDir(), command, timeout, plain)
}

func TestRunPassesACommandThatExitsWithZero(t *testing.T) {
	if res := run(t, "true", time.Minute); res.Err != nil {
		t.Fatalf("err = %v", res.Err)
	}
}

func TestRunReportsTheExitCodeAndBothOutputStreams(t *testing.T) {
	res := run(t, "echo out; echo err >&2; exit 3", time.Minute)

	var exit *exec.ExitError
	if !errors.As(res.Err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("err = %v", res.Err)
	}
	if !strings.Contains(res.Output, "out\n") || !strings.Contains(res.Output, "err\n") {
		t.Errorf("output = %q", res.Output)
	}
}

func TestRunStartsInTheGivenFolder(t *testing.T) {
	dir := t.TempDir()

	res := taskcheck.Run(context.Background(), dir, "pwd -P", time.Minute, plain)

	if !strings.HasSuffix(strings.TrimSpace(res.Output), strings.TrimPrefix(dir, "/private")) {
		t.Errorf("output = %q, dir = %q", res.Output, dir)
	}
}

func TestRunKillsTheWholeProcessGroupOnTimeout(t *testing.T) {
	begin := time.Now()

	res := run(t, "sh -c 'sleep 30 & wait'", 200*time.Millisecond)

	if !res.TimedOut || res.Err == nil {
		t.Fatalf("result = %+v", res)
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Errorf("took %v", took)
	}
}

func TestRunKeepsTheLastBytesOfLongOutputFromALineStart(t *testing.T) {
	res := run(t, "seq 1 5000", time.Minute)

	if res.Cut == 0 || len(res.Output) > taskcheck.MaxOutput {
		t.Fatalf("cut = %d, len = %d", res.Cut, len(res.Output))
	}
	if !strings.HasSuffix(res.Output, "5000\n") || strings.Contains(res.Output, "\n\n") {
		t.Errorf("tail = %q", res.Output[len(res.Output)-20:])
	}
	first, _, _ := strings.Cut(res.Output, "\n")
	if first == "" || strings.Trim(first, "0123456789") != "" {
		t.Errorf("first line = %q", first)
	}
	if res.Cut+len(res.Output) != len("1\n")*9+len("10\n")*90+len("100\n")*900+len("1000\n")*4001 {
		t.Errorf("cut %d and kept %d do not add up to the whole output", res.Cut, len(res.Output))
	}
}

func TestRunGivesTheCommandNoSessionIdentity(t *testing.T) {
	t.Setenv("SFY_TOKEN", "x")
	t.Setenv("GIT_DIR", "/elsewhere")

	res := run(t, `test -z "$SFY_TOKEN" && test -z "$GIT_DIR"`, time.Minute)

	if res.Err != nil {
		t.Fatalf("err = %v, output = %q", res.Err, res.Output)
	}
}

func TestRunGivesTheCommandItsOwnTempFolder(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()

	res := taskcheck.Run(context.Background(), dir, `test -d "$TMPDIR" && echo "$TMPDIR"`, time.Minute, plain)

	if res.Err != nil || strings.TrimSpace(res.Output) == other || !strings.Contains(res.Output, "sfy-check-") {
		t.Fatalf("err = %v, output = %q", res.Err, res.Output)
	}
}

func TestRunReportsAWrapperError(t *testing.T) {
	boom := errors.New("no sandbox")

	res := taskcheck.Run(context.Background(), t.TempDir(), "true", time.Minute, func(_, _ string, _ []string) ([]string, error) { return nil, boom })

	if !errors.Is(res.Err, boom) {
		t.Fatalf("err = %v", res.Err)
	}
}

func TestStartCallsDoneOnceWithTheResult(t *testing.T) {
	got := make(chan engine.CheckResult, 2)
	r := taskcheck.Runner{Ctx: context.Background(), Timeout: time.Minute}
	// The runner wraps in the sandbox, so a sandbox that cannot start still gives a result.
	r.Start(t.TempDir(), "true", func(res engine.CheckResult) { got <- res })

	select {
	case <-got:
	case <-time.After(30 * time.Second):
		t.Fatal("done was not called")
	}
	select {
	case <-got:
		t.Fatal("done was called twice")
	case <-time.After(100 * time.Millisecond):
	}
}
