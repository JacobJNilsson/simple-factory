// Package provider defines the adapter that starts agent sessions for one agent CLI.
package provider

import "github.com/JacobJNilsson/simple-factory/internal/engine"

// Provider turns a session spec into the command that runs the agent CLI.
type Provider interface {
	// Command prepares the session's files and returns the argv to run in the session's folder.
	// The argv holds one element for each word, unquoted. The mux runner quotes them.
	Command(spec engine.SessionSpec) ([]string, error)
	// Resumes reports whether Command would resume the recorded conversation of the main agent in spec.
	Resumes(spec engine.SessionSpec) bool
	// Deliver gives text to the running session without typing into its input line.
	// It returns false, with nothing delivered, when the provider has no such way or the session has no delivery set up.
	Deliver(name, text string) (delivered bool, err error)
	// TrustWarning returns a warning when the agent CLI would stop at a trust prompt for dir, or when sfy cannot tell.
	// It returns an empty string for a trusted folder, and it never changes the CLI's config.
	TrustWarning(dir string) string
}
