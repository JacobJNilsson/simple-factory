// Package taskcheck runs a task's check command for the engine, in a sandbox with the worker's path lists.
package taskcheck

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
)

// MaxOutput is how many bytes of output a result keeps. The kept bytes are the last ones.
const MaxOutput = 8 << 10

// waitDelay is how long to wait for the output pipes after the command ends or is killed.
const waitDelay = 10 * time.Second

// Wrapper turns argv into the argv that runs it in dir, and may restrict it. tmp is the command's own temp folder.
type Wrapper func(dir, tmp string, argv []string) ([]string, error)

// Runner starts checks for the engine.
type Runner struct {
	// Ctx ends every running check.
	Ctx     context.Context
	Timeout time.Duration
	Sandbox Sandbox
}

// Start runs command in dir on a new goroutine and calls done with the result.
func (r Runner) Start(dir, command string, done func(engine.CheckResult)) {
	wrap := func(dir, tmp string, argv []string) ([]string, error) {
		return r.Sandbox.Wrap(runtime.GOOS, dir, tmp, argv)
	}
	go func() { done(Run(r.Ctx, dir, command, r.Timeout, wrap)) }()
}

// Run runs `sh -c command` in dir and waits for it. The command gets its own session, no stdin and the environment from checkEnv.
// A timeout or the end of ctx kills the whole process group.
func Run(ctx context.Context, dir, command string, timeout time.Duration, wrap Wrapper) engine.CheckResult {
	start := time.Now()
	out := &tailBuffer{max: MaxOutput}
	timedOut, err := run(ctx, dir, command, timeout, wrap, out)
	text, cut := out.String()
	return engine.CheckResult{Output: text, Cut: cut, Err: err, TimedOut: timedOut, Took: time.Since(start)}
}

// run reports whether the timeout, and not ctx, stopped the command.
func run(ctx context.Context, dir, command string, timeout time.Duration, wrap Wrapper, out *tailBuffer) (timedOut bool, err error) {
	tmp, err := os.MkdirTemp("", "sfy-check-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	argv, err := wrap(dir, tmp, []string{"sh", "-c", command})
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = dir, checkEnv(os.Environ(), tmp)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = waitDelay
	err = cmd.Run()
	return err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded), err
}

// checkEnv returns environ without the session identity of sfy and without the git variables that point at another repository.
// The command gets tmp as its temp folder and no git prompts.
func checkEnv(environ []string, tmp string) []string {
	var out []string
	for _, e := range environ {
		name, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(name, "SFY_") || isGitLocation(name) || name == "TMPDIR" {
			continue
		}
		out = append(out, e)
	}
	return append(out, "TMPDIR="+tmp, "GIT_TERMINAL_PROMPT=0")
}

func isGitLocation(name string) bool {
	switch name {
	case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_PREFIX":
		return true
	}
	return false
}
