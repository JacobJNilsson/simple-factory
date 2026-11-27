package up

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/drop"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/launcher"
	"github.com/JacobJNilsson/simple-factory/internal/mux"
	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/prompts"
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
	proc     process.Process
	bin      string
	mux      mux.Mux
	whoami   func() (string, error)
	launcher launcher.Launcher
	// agent is the provider that the launcher runs, kept for the check at start-up.
	agent provider.Provider
	// move moves the factory folder from the old name of the lead to the new one, as moveToLead does.
	move func() error
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
	cfg, proc, err := loadDefinition(home, env("HOME"))
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
	// The move asks the plain set, because the old terminal name has the prefix and no recorded address.
	move := func() error { return moveToLead(home, prefix, m.Alive) }
	m = mux.Prefixed(mux.Registered(m, prefix+leadSession, leadAddressPath(home), leadGone(home), registration.Path(home), registration.PidPath(home)), prefix)
	who, _ := m.(mux.Whoamier)
	layers, err := config.Layers(home, env("HOME"))
	if err != nil {
		return parts{}, err
	}
	agent := claude.New(cfg, home, bin, layers)
	adapters := map[string]provider.Provider{"claude": agent}
	if err := checkHarnesses(cfg, proc, adapters); err != nil {
		return parts{}, err
	}
	l := launcher.WithAdapters(m, adapters, cfg.Provider, logAppender{path: filepath.Join(home, logName)})
	return parts{home: home, layers: layers, cfg: cfg, proc: proc, bin: bin, mux: m, whoami: who.Whoami, agent: agent, move: move, launcher: l}, nil
}

// loadDefinition loads the settings and the task process, and refuses a process that the engine cannot run.
// It also refuses a process that removes the position of an open task, so sfy restart keeps the old process running.
func loadDefinition(home, userHome string) (config.Config, process.Process, error) {
	cfg, proc, err := config.LoadProcess(home, userHome)
	if err != nil {
		return cfg, proc, err
	}
	if err := engine.Runnable(proc); err != nil {
		return cfg, proc, err
	}
	layers, err := config.Layers(home, userHome)
	if err != nil {
		return cfg, proc, err
	}
	if err := checkPrompts(proc, layers); err != nil {
		return cfg, proc, err
	}
	tasks, err := store.Files(home).Tasks()
	if err != nil {
		return cfg, proc, err
	}
	open := map[string]process.Position{}
	for _, t := range tasks {
		if t.Open() {
			open[t.ID] = t.Position()
		}
	}
	return cfg, proc, proc.Validate(open)
}

// agentPrompts returns the prompt names of the claude agents that sfy config does not list already, sorted.
func agentPrompts(proc process.Process) []string {
	var names []string
	for _, a := range proc.Agents {
		if a.Harness != process.Claude {
			continue
		}
		for _, name := range []string{a.Prompt, a.RepoPrompt} {
			if name != "" && !slices.Contains(promptKinds, name) && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return names
}

// checkPrompts refuses a claude agent whose prompt or repo_prompt no layer has and sfy has no default for, so its session could not start.
func checkPrompts(proc process.Process, layers []string) error {
	var errs []error
	for _, name := range agentPrompts(proc) {
		has, err := prompts.Has(name, layers)
		if err == nil && !has {
			err = &prompts.MissingError{Name: name}
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// checkHarnesses refuses a provider key or a claude agent whose session harness has no adapter.
func checkHarnesses(cfg config.Config, proc process.Process, adapters map[string]provider.Provider) error {
	if _, ok := adapters[cfg.Provider]; !ok {
		return fmt.Errorf("config: provider %q has no adapter. sfy has an adapter for claude", cfg.Provider)
	}
	for _, name := range slices.Sorted(maps.Keys(proc.Agents)) {
		a := proc.Agents[name]
		if a.Harness == process.Claude {
			if _, ok := adapters[a.Harness]; !ok {
				return fmt.Errorf("process: agents.%s names the harness %q, which has no adapter", name, a.Harness)
			}
		}
	}
	return nil
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
	// The move comes before the socket listens, so no command finds the lead under a half-moved name.
	if err := p.move(); err != nil {
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
	eng.SetRepoConfig(repoConfig(p.cfg, p.home, env))
	eng.SetChecker(checker(ctx, p, env("HOME")))
	eng.SetProcess(p.proc)
	eng.SetRegistration(registration.Folder{Home: p.home})
	eng.SetLeadSpec(options{home: p.home, layers: p.layers, dir: config.WorkFolder(p.home), config: p.cfg}.wakeSpec())
	eng.SetAuth(identity.Folder{Home: p.home})
	eng.SetFactories(machine{home: p.home, env: env})
	if err := startDrops(eng, drop.Folder{Home: p.home}, log); err != nil {
		return err
	}
	return server.Serve(ctx, socket(p.home), eng, tickEvery, log)
}

// startDrops creates the folders that senders write, removes old records, and takes the drops that arrived while sfy was down.
// Only a folder that cannot be created stops the start. Any other failure goes to the log, and the next tick tries again.
func startDrops(eng *engine.Engine, f drop.Folder, log io.Writer) error {
	if err := f.Prepare(); err != nil {
		return err
	}
	eng.SetDrops(f)
	for _, err := range []error{f.Prune(time.Now()), eng.PickUpDrops()} {
		if err != nil {
			_, _ = fmt.Fprintf(log, "%s drops: %v\n", time.Now().Format(time.RFC3339), err)
		}
	}
	return nil
}

// checker returns the runner of task checks. It has the sandbox paths of a worker, and it also hides the sfy socket.
// It hides the sessions folder of every factory, because each holds tokens.
func checker(ctx context.Context, p parts, userHome string) taskcheck.Runner {
	own := filepath.Join(p.home, "sessions")
	deny := []string{own, socket(p.home)}
	for _, f := range config.SessionFolders(userHome) {
		if filepath.Clean(f) != filepath.Clean(own) {
			deny = append(deny, f)
		}
	}
	hidden := slices.Clone(deny)
	deny = append(deny, p.cfg.DenyRead...)
	return taskcheck.Runner{
		Ctx: ctx, Timeout: p.cfg.CheckTimeout,
		Sandbox: taskcheck.Sandbox{DenyRead: deny, AllowWrite: p.cfg.AllowWrite},
		Hidden:  hidden,
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

// Mode holds the flags of sfy up.
type Mode struct {
	// Headless starts only the sfy process, detached, as sfy up --headless does.
	Headless bool
	// NewFactory makes a factory in a folder whose factories all have other name prefixes, as sfy up --new-factory does.
	NewFactory bool
}

// Command runs a plain sfy for the factory folder that env names.
func Command(environ []string, env func(string) string, stdout, stderr io.Writer, mode Mode) error {
	env, err := factoryEnv(env, os.Getwd, stderr, !mode.NewFactory)
	if err != nil {
		return err
	}
	o, err := newOptions(environ, env, stdout, stderr)
	if err != nil {
		return err
	}
	warnOldNames(o.warn, o.home, env("HOME"))
	warnSkipped(o.warn, o.home, env("HOME"))
	if mode.Headless {
		o.headless = true
		return runHeadless(o)
	}
	return run(o)
}

// warnOldNames prints one warning line for each old name that the factory home still uses. A failure is a warning too and never stops sfy.
func warnOldNames(warn io.Writer, home, userHome string) {
	lines, err := config.OldNames(home, userHome)
	if err != nil {
		_, _ = fmt.Fprintln(warn, "sfy: warning: no check of old names: "+err.Error())
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(warn, "sfy: warning: "+line)
	}
}

// factoryEnv returns env with SFY_HOME set to the factory that sfy up starts in the current folder.
// SFY_HOME wins. The user's home folder and a folder with no factory above it work as the rules say:
// the first gets the default factory, and the second gets a new factory in the factories folder.
// The factory has the name prefix of SFY_NAME_PREFIX, and the user's home folder has no factory with a name prefix.
// In a folder whose factories all have other name prefixes, refuse makes it fail with an otherPrefixError and create nothing.
// Without refuse, it creates the factory and prints one line to note that names the other factory folder.
func factoryEnv(env func(string) string, getwd func() (string, error), note io.Writer, refuse bool) (func(string) string, error) {
	if env("SFY_HOME") != "" {
		return env, nil
	}
	prefix, err := config.NamePrefix(env)
	if err != nil {
		return nil, err
	}
	userHome := env("HOME")
	cwd, err := getwd()
	if err != nil {
		return nil, err
	}
	state := config.DefaultHome(userHome)
	switch {
	case config.CanonicalPath(cwd) != config.CanonicalPath(userHome):
		found, ok := config.FindFactory(userHome, cwd, prefix)
		if !ok {
			others := config.FactoriesAt(userHome, cwd)
			if refuse && len(others) > 0 {
				return nil, &otherPrefixError{prefix: prefix, others: others}
			}
			if found, err = config.CreateFactory(userHome, cwd, prefix); err != nil {
				return nil, err
			}
			if len(others) > 0 {
				_, _ = fmt.Fprintf(note, "sfy: this folder has another factory at %s. This command made its own, %s.\n", others[0], found)
			}
		}
		state = found
	case prefix != "":
		return nil, &config.NoFactoryError{Prefix: prefix}
	}
	return func(key string) string {
		if key == "SFY_HOME" {
			return state
		}
		return env(key)
	}, nil
}

// Restart runs sfy restart for the factory folder that env names. The operator and the lead may run it.
// With update set, it upgrades through brew or from the newest GitHub release before it reads the config, and restarts only when that succeeds.
// version is the running sfy version, which the release update compares with the newest tag.
// The upgraded binary then reads the config, because a config written for the new version can fail the old one.
// With headless set, the new sfy process runs detached and no lead starts, as sfy restart --headless says.
func Restart(environ []string, env func(string) string, stdout, stderr io.Writer, version string, update, headless bool) error {
	return restartWith(systemUpdater(version), environ, env, stdout, stderr, update, headless)
}

func restartWith(u updater, environ []string, env func(string) string, stdout, stderr io.Writer, update, headless bool) error {
	if !mayRestart(env("SFY_AGENT")) {
		return ErrNotAllowed
	}
	if err := checkRestartFactory(env, os.Getwd); err != nil {
		return err
	}
	if update {
		return u.updateAndRestart(environ, stdout, stderr, headless)
	}
	o, err := newOptions(environ, env, stdout, stderr)
	if err != nil {
		return err
	}
	o.headless = headless
	warnOldNames(o.warn, o.home, env("HOME"))
	warnSkipped(o.warn, o.home, env("HOME"))
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
		mux: p.mux, launcher: p.launcher, move: p.move, warn: stderr, spawn: spawnDetached, brew: runBrew, whoami: p.whoami, exec: execInPlace,
		session: func() string { return claude.CurrentSession(env) },
		answers: func() bool { return server.Ping(sock, probeTimeout) == nil },
		call:    func(req protocol.Request) (protocol.Answer, error) { return server.Call(sock, req) },
		wait:    10 * time.Second, poll: 100 * time.Millisecond, out: stdout,
	}, nil
}

// repoConfig returns the lookup of the per-repo config of the factory with the factory folder home:
// the .sfy folders and .sfy.toml files from a repo folder up to the walk of the factory's working folder, on top of cfg.
func repoConfig(cfg config.Config, home string, env func(string) string) func(folder string) (config.Config, error) {
	work := config.WorkFolder(home)
	return func(folder string) (config.Config, error) {
		return config.ForRepo(cfg, env("HOME"), work, folder)
	}
}
