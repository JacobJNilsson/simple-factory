package claude

import "encoding/json"

// CurrentSession returns the id of the Claude Code session that runs the calling process.
// It returns an empty string outside a session. Claude Code sets the variable for its own tools and hooks.
func CurrentSession(env func(string) string) string { return env("CLAUDE_CODE_SESSION_ID") }

// HookSession returns the id of the session that a hook's JSON input names, or an empty string when it names none.
func HookSession(input []byte) string {
	var in struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	return in.SessionID
}
