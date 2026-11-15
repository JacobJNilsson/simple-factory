// Package launcher joins a multiplexer and a provider into the engine's Launcher.
package launcher

import (
	"fmt"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/mux"
	"github.com/JacobJNilsson/simple-factory/internal/provider"
)

type launcher struct {
	mux.Mux
	p provider.Provider
}

// New returns a Launcher that starts the provider's command in a multiplexer session.
// CONTRACT: the engine takes a Launcher, so New returns the interface.
func New(m mux.Mux, p provider.Provider) engine.Launcher {
	return launcher{Mux: m, p: p}
}

// Start asks the provider for the session's command and runs it in a new session.
func (l launcher) Start(spec engine.SessionSpec) error {
	argv, err := l.p.Command(spec)
	if err != nil {
		return err
	}
	return l.Mux.Start(spec.Name, spec.Dir, argv)
}

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
