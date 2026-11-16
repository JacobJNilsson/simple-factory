package engine

// PreflightError says that a session cannot start until the operator fixes the machine.
// Msg names the cause and the fix.
type PreflightError struct{ Msg string }

func (e *PreflightError) Error() string { return "session cannot start: " + e.Msg }
