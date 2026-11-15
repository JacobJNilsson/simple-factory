// Package protocol defines the request and answer that the CLI and the sfy process exchange.
package protocol

// Caller identifies who runs a command.
type Caller struct {
	// Agent is the session name, and empty for the operator and for outside programs.
	Agent string
	// Task is the task id for workers and reviewers.
	Task string
}

// Request is one command from the CLI.
type Request struct {
	Caller Caller
	// Command is the words after sfy, for example "task new" or "report done".
	Command string
	Args    []string
	// Body carries file contents and long text.
	Body string
}

// Answer is the result of a command.
type Answer struct {
	OK    bool
	Text  string
	Error string
}
