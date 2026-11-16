package engine

// PreflightCause names why a session cannot start.
type PreflightCause string

// The causes that a provider reports.
const (
	// CauseProgramMissing means the provider program is not found.
	CauseProgramMissing PreflightCause = "program-missing"
	// CauseToolsMissing means a tool that the sandbox needs is not installed.
	CauseToolsMissing PreflightCause = "tools-missing"
	// CauseSandboxBlocked means the sandbox tools exist but the system blocks them.
	CauseSandboxBlocked PreflightCause = "sandbox-blocked"
)

// PreflightError says that a session cannot start until the operator fixes the machine.
// Cause names the kind of failure. Msg names the cause and the fix in words.
type PreflightError struct {
	Cause PreflightCause
	Msg   string
}

func (e *PreflightError) Error() string { return "session cannot start: " + e.Msg }
