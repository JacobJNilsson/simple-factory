package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// HookBody returns the request body that the sfy process gets for a hook event.
// For a permission prompt it names the tool and its main input. Every other event has no body.
func HookBody(event string, input []byte) string {
	if event != "permission" {
		return ""
	}
	var p struct {
		Tool  string          `json:"tool_name"`
		Input json.RawMessage `json:"tool_input"`
	}
	// An input that is not a payload with a tool name stays as it is.
	if json.Unmarshal(input, &p) != nil || p.Tool == "" {
		return string(input)
	}
	var in map[string]any
	_ = json.Unmarshal(p.Input, &in)
	label, key := "Input", ""
	switch p.Tool {
	case "Bash":
		label, key = "Command", "command"
	case "Edit", "Write", "Read":
		label, key = "File", "file_path"
	}
	if v, ok := in[key].(string); ok && v != "" {
		return fmt.Sprintf("Tool: %s. %s: %s", p.Tool, label, v)
	}
	if len(p.Input) == 0 {
		return fmt.Sprintf("Tool: %s", p.Tool)
	}
	compact := &bytes.Buffer{}
	if json.Compact(compact, p.Input) != nil {
		return fmt.Sprintf("Tool: %s", p.Tool)
	}
	return fmt.Sprintf("Tool: %s. Input: %s", p.Tool, compact)
}

// permissionReply is the PermissionRequest hook output that Claude Code reads.
type permissionReply struct {
	Output struct {
		Event    string `json:"hookEventName"`
		Decision struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message"`
		} `json:"decision"`
	} `json:"hookSpecificOutput"`
}

// HookReply returns the hook output that Claude Code reads for the answer of the sfy process.
// A permission answer with text becomes a denial that carries the text. Every other answer stays as it is.
func HookReply(event, answer string) string {
	if event != "permission" || answer == "" {
		return answer
	}
	var r permissionReply
	r.Output.Event = "PermissionRequest"
	r.Output.Decision.Behavior = "deny"
	r.Output.Decision.Message = answer
	// Marshal cannot fail for a struct of strings.
	out, _ := json.Marshal(r)
	return string(out)
}
