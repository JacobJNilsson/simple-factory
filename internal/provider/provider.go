// Package provider defines the adapter that starts agent sessions for one agent CLI.
package provider

import "github.com/JacobJNilsson/simple-factory/internal/engine"

// Provider turns a session spec into the command that runs the agent CLI.
type Provider interface {
	// Command prepares the session's files and returns the argv to run in the session's folder.
	// The argv holds one element for each word, unquoted. The mux runner quotes them.
	Command(spec engine.SessionSpec) ([]string, error)
	// Resumes reports whether Command would resume the recorded conversation of the lead in spec.
	Resumes(spec engine.SessionSpec) bool
	// Deliver gives text to the running session without typing into its input line.
	// It returns false, with nothing delivered, when the provider has no such way or the running session has no delivery set up. The core then types the note.
	// A non-empty messageID names the stored message that the text carries. It stays unread until the session takes the text.
	Deliver(name, text, messageID string) (delivered bool, err error)
	// Stuck reports whether delivered text waits for a session that cannot take it.
	Stuck(name string) bool
	// Drain takes the stuck text that is no stored message and returns it.
	Drain(name string) (string, error)
	// Preflight checks that a session of the given kind can start, for example that the agent CLI and its sandbox exist.
	// A failure is an *engine.PreflightError, and starting the session again cannot fix it.
	Preflight(kind string) error
	// TrustWarning returns a warning when the agent CLI would stop at a trust prompt for dir, or when sfy cannot tell.
	// It returns an empty string for a trusted folder, and it never changes the CLI's config.
	TrustWarning(dir string) string
}
