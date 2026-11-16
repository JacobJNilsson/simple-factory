package claude

// CurrentSession returns the id of the Claude Code session that runs the calling process.
// It returns an empty string outside a session. Claude Code sets the variable for its own tools and hooks.
func CurrentSession(env func(string) string) string { return env("CLAUDE_CODE_SESSION_ID") }
