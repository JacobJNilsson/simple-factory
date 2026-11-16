package claude

import "testing"

func TestHookSessionReadsTheSessionIDOfTheInput(t *testing.T) {
	cases := map[string]string{
		`{"session_id":"s-1","hook_event_name":"Stop"}`: "s-1",
		`{"hook_event_name":"Stop"}`:                    "",
		`not json`:                                      "",
		``:                                              "",
	}
	for input, want := range cases {
		if got := HookSession([]byte(input)); got != want {
			t.Errorf("input %q got %q, want %q", input, got, want)
		}
	}
}

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
