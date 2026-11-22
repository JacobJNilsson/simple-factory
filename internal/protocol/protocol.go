// Package protocol defines the request and answer that the CLI and the sfy process exchange.
package protocol

// Caller identifies who runs a command.
type Caller struct {
	// Agent is the session name, and empty for the operator and for outside programs.
	Agent string
	// Task is the task id for workers and reviewers.
	Task string
	// Token is the secret of the session that runs the command. The operator has none.
	Token string
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
	// Warning is one line for stderr, such as a note that the request used an old name. The command still succeeds or fails as before.
	Warning string
}

// Lead is the session name of the agent that leads a factory.
const Lead = "lead"

// OldLead is the name that the lead had before. sfy reads it as Lead until a later release removes it.
const OldLead = "main"

// AgentName returns name with the old name of the lead read as Lead. Every other name stays as it is.
func AgentName(name string) string {
	if name == OldLead {
		return Lead
	}
	return name
}

// OldLeadWarning is the warning for a command that names the lead by its old name.
const OldLeadWarning = "sfy: main is now lead. Run: sfy send lead <text>."
