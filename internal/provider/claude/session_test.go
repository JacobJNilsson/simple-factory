package claude

import "testing"

func TestCurrentSessionReadsTheSessionVariable(t *testing.T) {
	env := func(k string) string {
		if k == "CLAUDE_CODE_SESSION_ID" {
			return "s-1"
		}
		return ""
	}

	if got := CurrentSession(env); got != "s-1" {
		t.Fatalf("got %q", got)
	}
	if got := CurrentSession(func(string) string { return "" }); got != "" {
		t.Fatalf("outside a session got %q", got)
	}
}
