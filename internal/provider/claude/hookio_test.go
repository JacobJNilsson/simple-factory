package claude

import (
	"encoding/json"
	"testing"
)

func TestHookBodyNamesTheToolAndItsMainInput(t *testing.T) {
	const ids = `"session_id":"s1","transcript_path":"/x/y.jsonl"`
	cases := map[string]struct{ input, want string }{
		"bash":                 {`{` + ids + `,"tool_name":"Bash","tool_input":{"command":"ls -la","description":"d"}}`, "Tool: Bash. Command: ls -la"},
		"edit":                 {`{` + ids + `,"tool_name":"Edit","tool_input":{"file_path":"/w/a.go","old_string":"x"}}`, "Tool: Edit. File: /w/a.go"},
		"read":                 {`{` + ids + `,"tool_name":"Read","tool_input":{"file_path":"/w/c.go"}}`, "Tool: Read. File: /w/c.go"},
		"unknown tool":         {`{` + ids + `,"tool_name":"Grep","tool_input":{ "pattern": "x" }}`, `Tool: Grep. Input: {"pattern":"x"}`},
		"missing tool input":   {`{"tool_name":"Task"}`, "Tool: Task"},
		"non-string command":   {`{"tool_name":"Bash","tool_input":{"command":5}}`, `Tool: Bash. Input: {"command":5}`},
		"body that is no JSON": {"Run rm -rf build?", "Run rm -rf build?"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := HookBody("permission", []byte(c.input)); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestHookBodyIsEmptyForEventsWithoutABody(t *testing.T) {
	for _, event := range []string{"turn-ended", "idle", "busy"} {
		if got := HookBody(event, []byte(`{"session_id":"s1","tool_name":"Bash"}`)); got != "" {
			t.Errorf("%s: got %q", event, got)
		}
	}
}

func TestHookReplyDeniesAPermissionWithTheAnswerAsMessage(t *testing.T) {
	for _, reason := range []string{"why", "say \"no\"\nthen stop"} {
		var got struct {
			Out struct {
				Event    string `json:"hookEventName"`
				Decision struct {
					Behavior string `json:"behavior"`
					Message  string `json:"message"`
				} `json:"decision"`
			} `json:"hookSpecificOutput"`
		}
		text := HookReply("permission", reason)
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("reply %q: %v", text, err)
		}
		d := got.Out.Decision
		if got.Out.Event != "PermissionRequest" || d.Behavior != "deny" || d.Message != reason {
			t.Errorf("reply %q", text)
		}
	}
}

func TestHookReplyLeavesOtherAnswersAsTheyAre(t *testing.T) {
	if got := HookReply("permission", ""); got != "" {
		t.Errorf("empty permission answer gave %q", got)
	}
	if got := HookReply("turn-ended", "x"); got != "x" {
		t.Errorf("turn-ended answer gave %q", got)
	}
}
