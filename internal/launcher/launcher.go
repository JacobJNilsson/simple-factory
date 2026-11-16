// Package launcher joins a multiplexer and a provider into the engine's Launcher.
package launcher

import (
	"fmt"
	"io"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/mux"
	"github.com/JacobJNilsson/simple-factory/internal/provider"
)

// Launcher is the engine's Launcher with the calls that starting sfy needs for the main agent.
type Launcher interface {
	engine.Launcher
	// ShowMain shows the main agent's session again with the show_main template.
	ShowMain(name string) error
	// StartMain starts the main agent. It resumes the last conversation when it can, and reports whether it did.
	StartMain(spec engine.SessionSpec) (resumed bool, err error)
	// MainCommand prepares the main agent's session and returns the argv that runs it, without starting a session.
	// The caller runs the argv in its own terminal. resumed reports whether the argv resumes the last conversation.
	MainCommand(spec engine.SessionSpec) (argv []string, resumed bool, err error)
}

type launcher struct {
	mux.Mux
	p   provider.Provider
	log io.Writer
}

// resumeGrace is how long a resumed main agent must run before sfy counts the resume as working.
// It is long enough for a failed resume to exit. Tests shorten it.
var resumeGrace = 2 * time.Second

// New returns a Launcher that starts the provider's command in a multiplexer session.
// A failed show goes to log, which may be nil.
// CONTRACT: callers take an interface, so New returns it.
func New(m mux.Mux, p provider.Provider, log io.Writer) Launcher {
	return launcher{Mux: m, p: p, log: log}
}

// Start asks the provider for the session's command, runs it in a new session, and shows the session.
// The main agent runs the show_main template and every other agent runs the show template.
// A failed show leaves the start successful, because the agent runs either way.
func (l launcher) Start(spec engine.SessionSpec) error {
	if spec.Kind == "main" {
		_, err := l.StartMain(spec)
		return err
	}
	return l.begin(spec)
}

// StartMain starts the main agent and resumes its last conversation when the provider can.
// When the resumed session ends within the grace time, it starts a new conversation instead.
func (l launcher) StartMain(spec engine.SessionSpec) (bool, error) {
	if spec.Fresh || !l.p.Resumes(spec) {
		return false, l.begin(spec)
	}
	if err := l.begin(spec); err != nil {
		return false, err
	}
	time.Sleep(resumeGrace)
	alive, err := l.Alive(spec.Name)
	if err != nil {
		return false, err
	}
	if alive {
		return true, nil
	}
	spec.Fresh = true
	return false, l.begin(spec)
}

// MainCommand returns the provider's argv for the main agent and whether it resumes the last conversation.
// It starts no session and runs no show template.
func (l launcher) MainCommand(spec engine.SessionSpec) ([]string, bool, error) {
	// The provider records a new conversation in Command, so it must answer Resumes first.
	resumed := !spec.Fresh && l.p.Resumes(spec)
	argv, err := l.p.Command(spec)
	return argv, resumed, err
}

// begin runs the provider's command in a new session and shows the session.
func (l launcher) begin(spec engine.SessionSpec) error {
	argv, err := l.p.Command(spec)
	if err != nil {
		return err
	}
	if err := l.Mux.Start(spec.Name, spec.Dir, argv); err != nil {
		return err
	}
	show := l.Show
	if spec.Kind == "main" {
		show = l.ShowMain
	}
	if err := show(spec.Name); err != nil && l.log != nil {
		_, _ = fmt.Fprintf(l.log, "%s show of %s failed: %v\n", time.Now().Format(time.RFC3339), spec.Name, err)
	}
	return nil
}

// TrustWarning asks the provider whether it would stop at a trust prompt for dir.
func (l launcher) TrustWarning(dir string) string { return l.p.TrustWarning(dir) }

// unknownError reports a multiplexer name with no config set and no built-in set.
type unknownError struct{ Name string }

func (e *unknownError) Error() string { return fmt.Sprintf("unknown multiplexer %q", e.Name) }

// Mux builds the multiplexer that cfg.Multiplexer names. A config set wins over a built-in set.
func Mux(cfg config.Config) (mux.Mux, error) {
	name := cfg.Multiplexer
	if m, ok := cfg.Multiplexers[name]; ok {
		t, err := mux.FromMap(m)
		if err != nil {
			return nil, fmt.Errorf("multiplexer %q: %w", name, err)
		}
		return mux.Runner(t), nil
	}
	t, ok := mux.Builtin(name)
	if !ok {
		return nil, &unknownError{Name: name}
	}
	return mux.Runner(t), nil
}
