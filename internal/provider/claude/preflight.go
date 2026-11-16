package claude

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
)

// system is the part of the machine that the preflight check looks at. Tests replace it.
type system struct {
	// lookPath finds a program on PATH, like exec.LookPath.
	lookPath func(name string) (string, error)
	// run runs a program and returns its error and combined output.
	run func(name string, args ...string) (string, error)
	// isFile reports whether path is an executable file.
	isFile func(path string) bool
	home   string
}

func realSystem() system {
	home, _ := os.UserHomeDir()
	return system{
		lookPath: exec.LookPath,
		run: func(name string, args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			return strings.TrimSpace(string(out)), err
		},
		isFile: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
		},
		home: home,
	}
}

const (
	aptFix      = "sudo apt install bubblewrap socat"
	apparmorFix = `Ubuntu 24.04 and later stop bwrap from creating user namespaces. Create /etc/apparmor.d/bwrap with this text:

abi <abi/4.0>,
include <tunables/global>

profile bwrap /usr/bin/bwrap flags=(unconfined) {
  userns,

  include if exists <local/bwrap>
}

Then run: sudo apparmor_parser -r /etc/apparmor.d/bwrap`
)

// claudePath returns the claude binary that a session starts.
// It falls back to ~/.local/bin/claude, the default install folder, when PATH lacks claude.
// The second result is false when no claude binary exists.
func (a adapter) claudePath() (string, bool) {
	if p, err := a.sys.lookPath("claude"); err == nil {
		return p, true
	}
	if a.sys.home != "" {
		if p := filepath.Join(a.sys.home, ".local", "bin", "claude"); a.sys.isFile(p) {
			return p, true
		}
	}
	return "", false
}

// Preflight checks that a session of the given kind can start.
// Every kind needs the claude binary. On Linux, workers and reviewers also need a working sandbox.
func (a adapter) Preflight(kind string) error {
	if _, ok := a.claudePath(); !ok {
		return &engine.PreflightError{Msg: "the claude binary is not on PATH and not at ~/.local/bin/claude. " +
			"The process that runs sfy may have a shorter PATH than your shell. Install Claude Code, or start sfy with the folder of claude on PATH."}
	}
	if kind == "main" || a.goos != "linux" {
		return nil
	}
	var missing []string
	for _, tool := range []string{"bwrap", "socat"} {
		if _, err := a.sys.lookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		return &engine.PreflightError{Msg: "the Claude Code sandbox needs " + strings.Join(missing, " and ") +
			", which is not installed. Run: " + aptFix}
	}
	out, err := a.sys.run("bwrap", "--unshare-user", "--unshare-net", "--ro-bind", "/", "/", "true")
	if err != nil {
		msg := "bwrap cannot create a sandbox here"
		if out != "" {
			msg += " (" + out + ")"
		}
		return &engine.PreflightError{Msg: msg + ". " + apparmorFix}
	}
	return nil
}
