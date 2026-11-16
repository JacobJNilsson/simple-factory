package up

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/launcher"
	"github.com/JacobJNilsson/simple-factory/internal/mux"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
	"github.com/JacobJNilsson/simple-factory/internal/server"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

const (
	// tickEvery is the pause between the engine's ticks.
	tickEvery = 2 * time.Second
	// probeTimeout limits how long up waits for one answer from the socket.
	probeTimeout = 2 * time.Second
	// logName is the log file of the sfy process, inside the sfy home.
	logName = "sfy.log"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func socket(home string) string { return filepath.Join(home, "sfy.sock") }

// parts is what Serve and Command both build from the config.
type parts struct {
	home     string
	cfg      config.Config
	bin      string
	mux      mux.Mux
	whoami   func() (string, error)
	launcher launcher.Launcher
}

// execInPlace changes into dir and replaces the sfy process with argv. It returns only when one of the two fails.
func execInPlace(dir string, argv []string) error {
	if err := os.Chdir(dir); err != nil {
		return err
	}
	return syscall.Exec(argv[0], argv, os.Environ())
}

func newParts(env func(string) string) (parts, error) {
	home := config.Home(env)
	cfg, err := config.Load(home)
	if err != nil {
		return parts{}, err
	}
	bin, err := runningBinary()
	if err != nil {
		return parts{}, err
	}
	m, err := launcher.Mux(cfg)
	if err != nil {
		return parts{}, err
	}
	// Registered sits inside Prefixed, so a recorded address stays as the terminal tool printed it.
	prefix := config.Prefix(home, env)
	m = mux.Prefixed(mux.Registered(m, prefix+mainSession, mainAddressPath(home)), prefix)
	who, _ := m.(mux.Whoamier)
	return parts{home: home, cfg: cfg, bin: bin, mux: m, whoami: who.Whoami, launcher: launcher.New(m, claude.New(cfg, home, bin), logAppender{path: filepath.Join(home, logName)})}, nil
}

// logAppender appends to sfy.log on each write, so a plain sfy and sfy serve both log without holding the file open.
type logAppender struct{ path string }

func (l logAppender) Write(p []byte) (int, error) {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := f.Write(p)
	return n, errors.Join(err, f.Close())
}

// Serve runs the sfy process until ctx ends. It logs to stderr and to sfy.log in the sfy home.
// A fatal error also goes to sfy.log, because the multiplexer pane may close before anyone reads stderr.
func Serve(ctx context.Context, env func(string) string, stderr io.Writer) error {
	home := config.Home(env)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(home, logName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	// The file comes first, because MultiWriter stops at the first failing writer and a closed pane makes stderr fail.
	err = serve(ctx, env, io.MultiWriter(logFile, stderr))
	if err != nil {
		_, _ = fmt.Fprintf(logFile, "%s fatal: %v\n", time.Now().Format(time.RFC3339), err)
	}
	return err
}

func serve(ctx context.Context, env func(string) string, log io.Writer) error {
	p, err := newParts(env)
	if err != nil {
		return err
	}
	if err := checkSocketPath(socket(p.home)); err != nil {
		return err
	}
	eng := engine.New(store.Files(p.home), p.launcher, workspace.NewPrefixed(p.home, config.Prefix(p.home, env)), p.cfg, systemClock{})
	eng.SetLog(log)
	return server.Serve(ctx, socket(p.home), eng, tickEvery, log)
}

// socketPathLimit is the longest Unix socket path in bytes, with the terminating zero byte.
// macOS allows 104 bytes and Linux 108.
func socketPathLimit() int {
	if runtime.GOOS == "darwin" {
		return 104
	}
	return 108
}

// socketPathError reports a socket path that is too long to bind.
type socketPathError struct {
	path string
	max  int
}

func (e *socketPathError) Error() string {
	return fmt.Sprintf("the socket path %s has %d bytes, and this system allows %d at most. Set SFY_HOME to a shorter path", e.path, len(e.path), e.max)
}

// checkSocketPath fails when the socket path is too long to bind.
func checkSocketPath(path string) error {
	limit := socketPathLimit()
	if len(path) >= limit {
		return &socketPathError{path: path, max: limit - 1}
	}
	return nil
}

// Command runs a plain sfy for the sfy home that env names.
func Command(environ []string, env func(string) string, stdout, stderr io.Writer) error {
	o, err := newOptions(environ, env, stdout, stderr)
	if err != nil {
		return err
	}
	return run(o)
}

// Restart runs sfy restart for the sfy home that env names. The operator and the main agent may run it.
// With update set, it updates the tap and runs brew upgrade sfy before it reads the config, and restarts only when that succeeds.
// The upgraded binary then reads the config, because a config written for the new version can fail the old one.
func Restart(environ []string, env func(string) string, stdout, stderr io.Writer, update bool) error {
	return restartWith(systemUpdater(), environ, env, stdout, stderr, update)
}

func restartWith(u updater, environ []string, env func(string) string, stdout, stderr io.Writer, update bool) error {
	if !mayRestart(env("SFY_AGENT")) {
		return ErrNotAllowed
	}
	if update {
		return u.updateAndRestart(environ, stdout, stderr)
	}
	o, err := newOptions(environ, env, stdout, stderr)
	if err != nil {
		return err
	}
	return restart(o)
}

func newOptions(environ []string, env func(string) string, stdout, stderr io.Writer) (options, error) {
	p, err := newParts(env)
	if err != nil {
		return options{}, err
	}
	sock := socket(p.home)
	if err := checkSocketPath(sock); err != nil {
		return options{}, err
	}
	return options{
		home: p.home, config: p.cfg, bin: p.bin, environ: environ,
		mux: p.mux, launcher: p.launcher, warn: stderr, brew: runBrew, whoami: p.whoami, exec: execInPlace,
		answers: func() bool { return server.Ping(sock, probeTimeout) == nil },
		wait:    10 * time.Second, poll: 100 * time.Millisecond, out: stdout,
	}, nil
}
