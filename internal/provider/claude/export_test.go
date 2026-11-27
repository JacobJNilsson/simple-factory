package claude

import (
	"os/exec"
	"slices"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// ErrConfigLocked and ConfigPath expose the trust write's lock error and config lookup to the tests.
var (
	ErrConfigLocked = errConfigLocked
	ConfigPath      = configPath
)

// ShortenLockWait sets the wait for the config lock to d until the test ends.
func ShortenLockWait(t interface{ Cleanup(func()) }, d time.Duration) {
	old := lockWait
	lockWait = d
	t.Cleanup(func() { lockWait = old })
}

// NewForOS returns the adapter as if it ran on goos.
func NewForOS(cfg config.Config, home, sfyBin, goos string, uid int) adapter {
	return adapter{cfg: cfg, home: home, sfyBin: sfyBin, layers: []string{home}, goos: goos, uid: uid, sys: realSystem()}
}

// Machine describes a fake machine for the preflight check.
type Machine struct {
	// Tools lists the programs on PATH, by name.
	Tools []string
	// ClaudeHome is the home folder that holds .local/bin/claude, or empty for none.
	ClaudeHome string
	// BwrapOut and BwrapErr are the output and the failure of the bwrap trial run.
	BwrapOut string
	BwrapErr error
}

// NewOnMachine returns the adapter on goos with a fake machine.
func NewOnMachine(goos string, m Machine) adapter {
	return adapter{goos: goos, sys: system{
		lookPath: func(n string) (string, error) {
			if slices.Contains(m.Tools, n) {
				return "/usr/bin/" + n, nil
			}
			return "", exec.ErrNotFound
		},
		run:    func(string, ...string) (string, error) { return m.BwrapOut, m.BwrapErr },
		isFile: func(p string) bool { return m.ClaudeHome != "" && p == m.ClaudeHome+"/.local/bin/claude" },
		home:   m.ClaudeHome,
	}}
}
