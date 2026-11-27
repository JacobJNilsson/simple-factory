// Package launcher joins a multiplexer and a provider into the engine's Launcher.
package launcher

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/mux"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/provider"
)

// Launcher is the engine's Launcher with the calls that starting sfy needs for the lead.
type Launcher interface {
	engine.Launcher
	// ShowLead shows the lead's session again with the show_lead template.
	ShowLead(name string) error
	// StartLead starts the lead. It resumes the last conversation when it can, and reports whether it did.
	StartLead(spec engine.SessionSpec) (resumed bool, err error)
	// LeadCommand prepares the lead's session and returns the argv that runs it, without starting a session.
	// The caller runs the argv in its own terminal. resumed reports whether the argv resumes the last conversation.
	LeadCommand(spec engine.SessionSpec) (argv []string, resumed bool, err error)
}

type launcher struct {
	mux.Mux
	// adapters holds the provider of each session harness. fallback names the one for a spec with no harness.
	adapters map[string]provider.Provider
	fallback string
	log      io.Writer
	// trustTold records that the lead heard about a failed trust mark, so it hears only once in each sfy process.
	trustTold *atomic.Bool
}

// resumeGrace is how long a resumed lead must run before sfy counts the resume as working.
// It is long enough for a failed resume to exit. Tests shorten it.
var resumeGrace = 2 * time.Second

// New returns a Launcher that starts the provider's command in a multiplexer session, with p as the claude harness.
// A failed show goes to log, which may be nil.
// CONTRACT: callers take an interface, so New returns it.
func New(m mux.Mux, p provider.Provider, log io.Writer) Launcher {
	return WithAdapters(m, map[string]provider.Provider{"claude": p}, "claude", log)
}

// WithAdapters returns a Launcher that starts each session through the adapter of its harness.
// fallback names the adapter of a spec with no harness, such as the lead. A harness with no adapter fails to start.
// CONTRACT: callers take an interface, so WithAdapters returns it.
func WithAdapters(m mux.Mux, adapters map[string]provider.Provider, fallback string, log io.Writer) Launcher {
	return launcher{Mux: m, adapters: adapters, fallback: fallback, log: log, trustTold: new(atomic.Bool)}
}

// UnknownHarnessError reports a session harness that the launcher has no adapter for.
type UnknownHarnessError struct{ Harness string }

func (e *UnknownHarnessError) Error() string {
	return fmt.Sprintf("no adapter for the session harness %q", e.Harness)
}

// adapter returns the provider of a harness. An empty harness gets the fallback.
func (l launcher) adapter(harness string) (provider.Provider, error) {
	if harness == "" {
		harness = l.fallback
	}
	p, ok := l.adapters[harness]
	if !ok {
		return nil, &UnknownHarnessError{Harness: harness}
	}
	return p, nil
}

// Preflight checks that a session of the kind can start through the harness's adapter.
func (l launcher) Preflight(harness, kind string) error {
	p, err := l.adapter(harness)
	if err != nil {
		return err
	}
	return p.Preflight(kind)
}

// Start asks the provider for the session's command, runs it in a new session, and shows the session.
// The lead runs the show_lead template and every other agent runs the show template.
// A failed show leaves the start successful, because the agent runs either way.
// A worker or a reviewer spec with Resume goes to resume, which never starts a new conversation.
func (l launcher) Start(spec engine.SessionSpec) error {
	if spec.Kind == "lead" {
		_, err := l.StartLead(spec)
		return err
	}
	if spec.Resume {
		return l.resume(spec)
	}
	return l.begin(spec)
}

// ResumeError reports a resume of a worker or a reviewer that did not work. Why says what went wrong.
type ResumeError struct{ Name, Why string }

func (e *ResumeError) Error() string {
	return fmt.Sprintf("cannot resume the conversation of %s: %s", e.Name, e.Why)
}

// resume continues the recorded conversation of a worker or a reviewer.
// It fails when the provider has no recorded conversation, or when the resumed session ends within the grace time.
func (l launcher) resume(spec engine.SessionSpec) error {
	p, err := l.adapter(spec.Harness)
	if err != nil {
		return err
	}
	if !p.Resumes(spec) {
		return &ResumeError{Name: spec.Name, Why: "it has no recorded conversation"}
	}
	if err := l.begin(spec); err != nil {
		return err
	}
	time.Sleep(resumeGrace)
	alive, err := l.Alive(spec.Name)
	if err != nil {
		return err
	}
	if !alive {
		return &ResumeError{Name: spec.Name, Why: fmt.Sprintf("the session ended within %s", resumeGrace)}
	}
	return nil
}

// StartLead starts the lead and resumes its last conversation when the provider can.
// When the resumed session ends within the grace time, it starts a new conversation instead.
func (l launcher) StartLead(spec engine.SessionSpec) (bool, error) {
	p, err := l.adapter(spec.Harness)
	if err != nil {
		return false, err
	}
	if spec.Fresh || !p.Resumes(spec) {
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

// LeadCommand returns the provider's argv for the lead and whether it resumes the last conversation.
// It starts no session and runs no show template.
func (l launcher) LeadCommand(spec engine.SessionSpec) ([]string, bool, error) {
	// The provider records a new conversation in Command, so it must answer Resumes first.
	p, err := l.adapter(spec.Harness)
	if err != nil {
		return nil, false, err
	}
	if err := p.Preflight(spec.Kind); err != nil {
		return nil, false, err
	}
	resumed := !spec.Fresh && p.Resumes(spec)
	argv, err := p.Command(spec)
	return argv, resumed, err
}

// begin runs the provider's command in a new session and shows the session.
func (l launcher) begin(spec engine.SessionSpec) error {
	p, err := l.adapter(spec.Harness)
	if err != nil {
		return err
	}
	if err := p.Preflight(spec.Kind); err != nil {
		return err
	}
	if spec.Kind == "worker" || spec.Kind == "reviewer" {
		l.trust(p, spec)
	}
	argv, err := p.Command(spec)
	if err != nil {
		return err
	}
	if err := l.Mux.Start(spec.Name, spec.Dir, argv); err != nil {
		return err
	}
	show := l.Show
	if spec.Kind == "lead" {
		show = l.ShowLead
	}
	if err := show(spec.Name); err != nil && l.log != nil {
		_, _ = fmt.Fprintf(l.log, "%s show of %s failed: %v\n", time.Now().Format(time.RFC3339), spec.Name, err)
	}
	return nil
}

// trust asks the provider to trust the session's folder, so the agent shows no trust dialog.
// A failure never stops the start. sfy logs each failure and tells the lead about the first one of this process.
func (l launcher) trust(p provider.Provider, spec engine.SessionSpec) {
	err := p.Trust(spec.Dir)
	if err == nil {
		return
	}
	l.logf("trust of %s for %s failed: %v", spec.Dir, spec.Name, err)
	if l.trustTold.Swap(true) {
		return
	}
	text := fmt.Sprintf("sfy could not mark %s trusted: %v. The agent may show the trust dialog.", spec.Dir, err)
	if err := l.Notify(protocol.Lead, text); err != nil {
		l.logf("note to the lead about the trust of %s failed: %v", spec.Dir, err)
	}
}

func (l launcher) logf(format string, args ...any) {
	if l.log != nil {
		_, _ = fmt.Fprintf(l.log, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
	}
}

// TrustWarning asks the provider whether it would stop at a trust prompt for dir.
func (l launcher) TrustWarning(dir string) string { return l.adapters[l.fallback].TrustWarning(dir) }

// unknownError reports a multiplexer name with no config set and no built-in set.
type unknownError struct{ Name string }

func (e *unknownError) Error() string { return fmt.Sprintf("unknown multiplexer %q", e.Name) }

// Mux builds the multiplexer that cfg.Multiplexer names, for the factory folder home. A config set wins over a built-in set.
func Mux(cfg config.Config, home string) (mux.Mux, error) {
	name := cfg.Multiplexer
	if m, ok := cfg.Multiplexers[name]; ok {
		t, err := mux.FromMap(m)
		if err != nil {
			return nil, fmt.Errorf("multiplexer %q: %w", name, err)
		}
		return mux.RunnerFor(t, home), nil
	}
	t, ok := mux.Builtin(name)
	if !ok {
		return nil, &unknownError{Name: name}
	}
	return mux.RunnerFor(t, home), nil
}
