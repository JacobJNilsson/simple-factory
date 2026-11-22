// Package taskcheck runs a task's check command for the engine, in a sandbox with the worker's path lists.
package taskcheck

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
	// Hidden lists the paths that every sandboxed command is denied, also one whose agent has its own deny_read list.
	// Sandbox.DenyRead holds them too.
	Hidden []string
}

// Start runs command in dir on a new goroutine and calls done with the result.
func (r Runner) Start(dir, command string, done func(engine.CheckResult)) {
	wrap := func(dir, tmp string, argv []string) ([]string, error) {
		return r.Sandbox.Wrap(runtime.GOOS, dir, tmp, argv)
	}
	go func() { done(Run(r.Ctx, dir, command, r.Timeout, wrap)) }()
}

// StartShell runs a shell agent's command with the agent's own lists, on a new goroutine, and calls done with the result.
// An agent with sandbox set to false runs with no wrapper. The result holds the files of the drop folder, which StartShell creates.
func (r Runner) StartShell(spec engine.ShellSpec, done func(engine.CheckResult)) {
	box := r.Sandbox
	if spec.DenyRead != nil {
		box.DenyRead = append(slices.Clone(r.Hidden), *spec.DenyRead...)
	}
	if spec.AllowWrite != nil {
		box.AllowWrite = slices.Clone(*spec.AllowWrite)
	}
	var mkErr error
	if spec.Drop != "" {
		mkErr = os.MkdirAll(spec.Drop, 0o700)
		box.AllowWrite = append(box.AllowWrite, spec.Drop)
	}
	wrap := func(dir, tmp string, argv []string) ([]string, error) {
		if mkErr != nil {
			return nil, mkErr
		}
		if !spec.Sandbox {
			return argv, nil
		}
		return box.Wrap(runtime.GOOS, dir, tmp, argv)
	}
	keep := keepAll
	if spec.PassEnv != nil {
		keep = keepOnly(*spec.PassEnv)
	}
	go func() {
		res := runWith(r.Ctx, spec.Dir, spec.Command, r.Timeout, wrap, keep)
		res.Drops = readDrops(spec.Drop)
		done(res)
	}()
}

// shellBase lists the variables that a shell agent with its own pass_env list keeps besides that list and the LC_ variables.
var shellBase = []string{"HOME", "USER", "LOGNAME", "SHELL", "TERM", "LANG", "PATH"}

// keepAll keeps every variable of sfy's environment that checkEnv lets through.
func keepAll(string) bool { return true }

// keepOnly keeps the variables of shellBase, the LC_ variables and the named ones.
func keepOnly(names []string) func(string) bool {
	return func(name string) bool {
		return slices.Contains(shellBase, name) || slices.Contains(names, name) || strings.HasPrefix(name, "LC_")
	}
}

// readDrops returns the .md files of a drop folder in name order. A folder that cannot be read gives none.
func readDrops(dir string) [][]byte {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out [][]byte
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			out = append(out, b)
		}
	}
	return out
}

// Run runs `sh -c command` in dir and waits for it. The command gets its own session, no stdin and the environment from checkEnv.
// A timeout or the end of ctx kills the whole process group.
func Run(ctx context.Context, dir, command string, timeout time.Duration, wrap Wrapper) engine.CheckResult {
	return runWith(ctx, dir, command, timeout, wrap, keepAll)
}

// runWith is Run with keep, which picks the variables of sfy's environment that the command gets.
func runWith(ctx context.Context, dir, command string, timeout time.Duration, wrap Wrapper, keep func(string) bool) engine.CheckResult {
	start := time.Now()
	out := &tailBuffer{max: MaxOutput}
	timedOut, err := run(ctx, dir, command, timeout, wrap, keep, out)
	text, cut := out.String()
	return engine.CheckResult{Output: text, Cut: cut, Err: err, TimedOut: timedOut, Took: time.Since(start)}
}

// run reports whether the timeout, and not ctx, stopped the command.
func run(ctx context.Context, dir, command string, timeout time.Duration, wrap Wrapper, keep func(string) bool, out *tailBuffer) (timedOut bool, err error) {
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
	var environ []string
	for _, e := range os.Environ() {
		if name, _, _ := strings.Cut(e, "="); keep(name) {
			environ = append(environ, e)
		}
	}
	cmd.Dir, cmd.Env = dir, checkEnv(environ, tmp)
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
