package claude_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
)

func TestPreflight(t *testing.T) {
	all := []string{"claude", "bwrap", "socat"}
	blocked := claude.Machine{Tools: all, BwrapOut: "loopback: Failed RTM_NEWADDR", BwrapErr: errors.New("exit 1")}
	tests := []struct {
		name string
		goos string
		kind string
		m    claude.Machine
		want string // empty means the check passes
	}{
		{"all present", "linux", "worker", claude.Machine{Tools: all}, ""},
		{"missing both tools", "linux", "worker", claude.Machine{Tools: []string{"claude"}}, "bwrap and socat, which is not installed. Run: sudo apt install bubblewrap socat"},
		{"missing socat", "linux", "reviewer", claude.Machine{Tools: []string{"claude", "bwrap"}}, "needs socat,"},
		{"bwrap blocked names the fix", "linux", "worker", blocked, "sudo apparmor_parser -r /etc/apparmor.d/bwrap"},
		{"bwrap blocked names its output", "linux", "worker", blocked, "Failed RTM_NEWADDR"},
		{"main needs no sandbox", "linux", "main", claude.Machine{Tools: []string{"claude"}}, ""},
		{"macOS skips the check", "darwin", "worker", claude.Machine{Tools: []string{"claude"}, BwrapErr: errors.New("x")}, ""},
		{"claude missing", "linux", "worker", claude.Machine{Tools: []string{"bwrap", "socat"}}, "claude binary is not on PATH"},
		{"claude missing on macOS", "darwin", "main", claude.Machine{}, "claude binary is not on PATH"},
		{"claude in the default folder", "linux", "worker", claude.Machine{Tools: []string{"bwrap", "socat"}, ClaudeHome: "/home/u"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := claude.NewOnMachine(tt.goos, tt.m).Preflight(tt.kind)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Preflight: %v", err)
				}
				return
			}
			var pre *engine.PreflightError
			if !errors.As(err, &pre) {
				t.Fatalf("want a PreflightError, got %v", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q lacks %q", err, tt.want)
			}
		})
	}
}
