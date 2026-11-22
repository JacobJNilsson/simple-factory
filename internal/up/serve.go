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
	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/launcher"
	"github.com/JacobJNilsson/simple-factory/internal/mux"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/provider"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
	"github.com/JacobJNilsson/simple-factory/internal/registration"
	"github.com/JacobJNilsson/simple-factory/internal/server"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/taskcheck"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

const (
	// tickEvery is the pause between the engine's ticks.
	tickEvery = 2 * time.Second
	// probeTimeout limits how long up waits for one answer from the socket.
	probeTimeout = 2 * time.Second
	// logName is the log file of the sfy process, inside the factory folder.
	logName = "sfy.log"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func socket(home string) string { return filepath.Join(home, "sfy.sock") }

// parts is what Serve and Command both build from the config.
type parts struct {
	home string
	// layers are the folders of the factory's definition, lowest first, as config.Layers returns them.
	layers   []string
	cfg      config.Config
	bin      string
	mux      mux.Mux
	whoami   func() (string, error)
	launcher launcher.Launcher
	// agent is the provider that the launcher runs, kept for the check at start-up.
	agent provider.Provider
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
	cfg, err := config.LoadFactory(home, env("HOME"))
	if err != nil {
		return parts{}, err
	}
	bin, err := runningBinary()
	if err != nil {
		return parts{}, err
	}
	m, err := launcher.Mux(cfg, home)
	if err != nil {
		return parts{}, err
	}
	// Registered sits inside Prefixed, so a recorded address stays as the terminal tool printed it.
	prefix := config.Prefix(home, env)
	m = mux.Prefixed(mux.Registered(m, prefix+mainSession, mainAddressPath(home), registration.Path(home)), prefix)
	who, _ := m.(mux.Whoamier)
	layers := config.Layers(home, env("HOME"))
	agent := claude.New(cfg, home, bin, layers)
	return parts{home: home, layers: layers, cfg: cfg, bin: bin, mux: m, whoami: who.Whoami, agent: agent, launcher: launcher.New(m, agent, logAppender{path: filepath.Join(home, logName)})}, nil
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

// Serve runs the sfy process until ctx ends. It logs to stderr and to sfy.log in the factory folder.
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
	log := io.Writer(io.MultiWriter(logFile, stderr))
	// A detached process has stderr in sfy.log, so a second copy would repeat each line.
	if env(headlessEnv) != "" {
		log = logFile
	}
	err = serve(ctx, env, log)
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
	d, err := buildDefinition(p.cfg, p.layers)
	if err != nil {
		return err
	}
	if err := writeDefinition(p.home, d); err != nil {
		return err
	}
	// A failed check does not stop sfy. Each session start repeats it and reports the cause.
	// It runs in the background, because the trial run of bwrap may be slow.
	go func() {
		if err := p.agent.Preflight("worker"); err != nil {
			_, _ = fmt.Fprintf(log, "%s warning: %v\n", time.Now().Format(time.RFC3339), err)
		}
	}()
	// Sessions that started under an older sfy have no token, and restart keeps them running.
	if err := (identity.Folder{Home: p.home}).IssueLegacy(); err != nil {
		return err
	}
	eng := engine.New(store.Files(p.home), p.launcher, p.agent, workspace.NewPrefixed(p.home, config.Prefix(p.home, env)), p.cfg, systemClock{})
	eng.SetLog(log)
	eng.SetRepoConfig(repoConfig(p.cfg, env))
	eng.SetChecker(checker(ctx, p))
	eng.SetRegistration(registration.Folder{Home: p.home})
	eng.SetAuth(identity.Folder{Home: p.home})
	return server.Serve(ctx, socket(p.home), eng, tickEvery, log)
}

// checker returns the runner of task checks. It has the sandbox paths of a worker, and it also hides the sfy socket.
func checker(ctx context.Context, p parts) taskcheck.Runner {
	deny := append([]string{filepath.Join(p.home, "sessions"), socket(p.home)}, p.cfg.DenyRead...)
	return taskcheck.Runner{
		Ctx: ctx, Timeout: p.cfg.CheckTimeout,
		Sandbox: taskcheck.Sandbox{DenyRead: deny, AllowWrite: p.cfg.AllowWrite},
	}
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
	return fmt.Sprintf("the socket path %s has %d bytes, and this system allows %d at most. Set SFY_HOME to a shorter path, or start the factory in a folder with a shorter name", e.path, len(e.path), e.max)
}

// checkSocketPath fails when the socket path is too long to bind.
func checkSocketPath(path string) error {
	limit := socketPathLimit()
	if len(path) >= limit {
		return &socketPathError{path: path, max: limit - 1}
	}
	return nil
}

// Command runs a plain sfy for the factory folder that env names.
func Command(environ []string, env func(string) string, stdout, stderr io.Writer) error {
	env, err := factoryEnv(env, os.Getwd)
	if err != nil {
		return err
	}
	o, err := newOptions(environ, env, stdout, stderr)
	if err != nil {
		return err
	}
	return run(o)
}

// factoryEnv returns env with SFY_HOME set to the factory that sfy up starts in the current folder.
// SFY_HOME wins. The user's home folder and a folder with no factory above it work as the rules say:
// the first gets the default factory, and the second gets a new factory in the factories folder.
func factoryEnv(env func(string) string, getwd func() (string, error)) (func(string) string, error) {
	if env("SFY_HOME") != "" {
		return env, nil
	}
	userHome := env("HOME")
	cwd, err := getwd()
	if err != nil {
		return nil, err
	}
	state := config.DefaultHome(userHome)
	if config.CanonicalPath(cwd) != config.CanonicalPath(userHome) {
		found, ok := config.FindFactory(userHome, cwd)
		if !ok {
			if found, err = config.CreateFactory(userHome, cwd); err != nil {
				return nil, err
			}
		}
		state = found
	}
	return func(key string) string {
		if key == "SFY_HOME" {
			return state
		}
		return env(key)
	}, nil
}

// Restart runs sfy restart for the factory folder that env names. The operator and the main agent may run it.
// With update set, it upgrades through brew or from the newest GitHub release before it reads the config, and restarts only when that succeeds.
// version is the running sfy version, which the release update compares with the newest tag.
// The upgraded binary then reads the config, because a config written for the new version can fail the old one.
func Restart(environ []string, env func(string) string, stdout, stderr io.Writer, version string, update bool) error {
	return restartWith(systemUpdater(version), environ, env, stdout, stderr, update)
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
		home: p.home, layers: p.layers, dir: config.WorkFolder(p.home), config: p.cfg, bin: p.bin, environ: environ,
		mux: p.mux, launcher: p.launcher, warn: stderr, spawn: spawnDetached, brew: runBrew, whoami: p.whoami, exec: execInPlace,
		session: func() string { return claude.CurrentSession(env) },
		answers: func() bool { return server.Ping(sock, probeTimeout) == nil },
		call:    func(req protocol.Request) (protocol.Answer, error) { return server.Call(sock, req) },
		wait:    10 * time.Second, poll: 100 * time.Millisecond, out: stdout,
	}, nil
}

// repoConfig returns the lookup of the per-repo config: the .sfy.toml files from a repo folder up to the user's home folder, on top of cfg.
func repoConfig(cfg config.Config, env func(string) string) func(folder string) (config.Config, error) {
	return func(folder string) (config.Config, error) {
		return config.ForRepo(cfg, env("HOME"), folder)
	}
}
