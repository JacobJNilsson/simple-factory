package up

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/JacobJNilsson/factory/internal/config"
	"github.com/JacobJNilsson/factory/internal/engine"
	"github.com/JacobJNilsson/factory/internal/launcher"
	"github.com/JacobJNilsson/factory/internal/mux"
	"github.com/JacobJNilsson/factory/internal/provider/claude"
	"github.com/JacobJNilsson/factory/internal/server"
	"github.com/JacobJNilsson/factory/internal/store"
	"github.com/JacobJNilsson/factory/internal/workspace"
)

const (
	// tickEvery is the pause between the engine's ticks.
	tickEvery = 2 * time.Second
	// probeTimeout limits how long up waits for one answer from the socket.
	probeTimeout = 2 * time.Second
	// logName is the log file of the factory process, inside the factory home.
	logName = "factory.log"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func socket(home string) string { return filepath.Join(home, "factory.sock") }

// parts is what Serve and Command both build from the config.
type parts struct {
	home     string
	cfg      config.Config
	bin      string
	mux      mux.Mux
	launcher engine.Launcher
}

func newParts(env func(string) string) (parts, error) {
	home := config.Home(env)
	cfg, err := config.Load(home)
	if err != nil {
		return parts{}, err
	}
	bin, err := os.Executable()
	if err != nil {
		return parts{}, err
	}
	m, err := launcher.Mux(cfg)
	if err != nil {
		return parts{}, err
	}
	return parts{home: home, cfg: cfg, bin: bin, mux: m, launcher: launcher.New(m, claude.New(cfg, home, bin))}, nil
}

// Serve runs the factory process until ctx ends. It logs to stderr and to factory.log in the factory home.
func Serve(ctx context.Context, env func(string) string, stderr io.Writer) error {
	p, err := newParts(env)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.home, 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(p.home, logName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	eng := engine.New(store.Files(p.home), p.launcher, workspace.New(p.home), p.cfg, systemClock{})
	return server.Serve(ctx, socket(p.home), eng, tickEvery, io.MultiWriter(stderr, logFile))
}

// Command runs factory up for the factory home that env names.
func Command(environ []string, env func(string) string, stdout io.Writer) error {
	p, err := newParts(env)
	if err != nil {
		return err
	}
	sock := socket(p.home)
	return run(options{
		home: p.home, config: p.cfg, bin: p.bin, environ: environ,
		mux: p.mux, launcher: p.launcher,
		answers: func() bool { return server.Ping(sock, probeTimeout) == nil },
		wait:    10 * time.Second, poll: 100 * time.Millisecond, out: stdout,
	})
}
