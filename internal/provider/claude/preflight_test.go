package claude_test

import (
	"errors"
	"reflect"
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
		want engine.PreflightCause // empty means the check passes
	}{
		{"all present", "linux", "worker", claude.Machine{Tools: all}, ""},
		{"missing both tools", "linux", "worker", claude.Machine{Tools: []string{"claude"}}, engine.CauseToolsMissing},
		{"missing socat", "linux", "reviewer", claude.Machine{Tools: []string{"claude", "bwrap"}}, engine.CauseToolsMissing},
		{"bwrap blocked", "linux", "worker", blocked, engine.CauseSandboxBlocked},
		{"main needs no sandbox", "linux", "main", claude.Machine{Tools: []string{"claude"}}, ""},
		{"macOS skips the check", "darwin", "worker", claude.Machine{Tools: []string{"claude"}, BwrapErr: errors.New("x")}, ""},
		{"claude missing", "linux", "worker", claude.Machine{Tools: []string{"bwrap", "socat"}}, engine.CauseProgramMissing},
		{"claude missing on macOS", "darwin", "main", claude.Machine{}, engine.CauseProgramMissing},
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
			// The message text is free-form, so the want value copies it and a separate check demands a fix.
			want := &engine.PreflightError{Cause: tt.want, Msg: pre.Msg}
			if !reflect.DeepEqual(pre, want) {
				t.Fatalf("error = %+v, want cause %q", pre, tt.want)
			}
			if pre.Msg == "" {
				t.Fatal("the error names no fix")
			}
		})
	}
}
