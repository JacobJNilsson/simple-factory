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

type launcher struct {
	mux.Mux
	p   provider.Provider
	log io.Writer
	// showAgents makes worker and reviewer sessions visible. The main agent is always visible.
	showAgents bool
}

// New returns a Launcher that starts the provider's command in a multiplexer session.
// Workers and reviewers get a show only when showAgents is true. A failed show goes to log, which may be nil.
// CONTRACT: the engine takes a Launcher, so New returns the interface.
func New(m mux.Mux, p provider.Provider, log io.Writer, showAgents bool) engine.Launcher {
	return launcher{Mux: m, p: p, log: log, showAgents: showAgents}
}

// Start asks the provider for the session's command, runs it in a new session, and shows the session.
// A failed show leaves the start successful, because the agent runs either way.
func (l launcher) Start(spec engine.SessionSpec) error {
	argv, err := l.p.Command(spec)
	if err != nil {
		return err
	}
	if err := l.Mux.Start(spec.Name, spec.Dir, argv); err != nil {
		return err
	}
	if spec.Kind != "main" && !l.showAgents {
		return nil
	}
	if err := l.Show(spec.Name); err != nil && l.log != nil {
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
