// Package provider defines the adapter that starts agent sessions for one agent CLI.
package provider

import "github.com/JacobJNilsson/factory/internal/engine"

// Provider turns a session spec into the command that runs the agent CLI.
type Provider interface {
	// Command prepares the session's files and returns the argv to run in the session's folder.
	// The argv holds one element for each word, unquoted. The mux runner quotes them.
	Command(spec engine.SessionSpec) ([]string, error)
}
