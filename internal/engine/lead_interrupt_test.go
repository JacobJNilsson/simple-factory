package engine_test

// These tests check sfy lead interrupt: the operator presses Escape once in the lead's pane,
// and only while a hook reports that the lead runs a turn.

import (
	"slices"
	"testing"
)

func TestLeadInterruptPressesEscapeInALeadThatRunsATurn(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("lead", "", "hook busy", ""))

	a := v.run("", "", "lead interrupt", "")

	mustOK(t, a)
	if !slices.Equal(v.l.escaped, []string{"lead"}) || len(v.l.sent) != 0 || a.Text != "Pressed Escape in the lead." {
		t.Fatalf("escaped %v, typed %q, text %q", v.l.escaped, v.l.sent, a.Text)
	}
}

func TestLeadInterruptPressesNothingUnlessTheLeadRunsATurn(t *testing.T) {
	cases := []struct {
		name    string
		state   func(t *testing.T, v env)
		mention string
	}{
		{"unknown state", func(*testing.T, env) {}, "runs no turn"},
		{"idle", func(t *testing.T, v env) { mustOK(t, v.run("lead", "", "hook idle", "")) }, "runs no turn"},
		{"turn ended", func(t *testing.T, v env) {
			mustOK(t, v.run("lead", "", "hook busy", ""))
			mustOK(t, v.run("lead", "", "hook turn-ended", ""))
		}, "runs no turn"},
		{"interrupted before", func(t *testing.T, v env) {
			mustOK(t, v.run("lead", "", "hook busy", ""))
			mustOK(t, v.run("", "", "lead interrupt", ""))
			v.l.escaped = nil
		}, "runs no turn"},
		{"session gone", func(t *testing.T, v env) {
			mustOK(t, v.run("lead", "", "hook busy", ""))
			v.l.dead = map[string]bool{"lead": true}
		}, "no lead runs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := newEnv(t)
			c.state(t, v)

			mustFail(t, v.run("", "", "lead interrupt", ""), c.mention)
			if len(v.l.escaped) != 0 {
				t.Fatalf("escaped %v", v.l.escaped)
			}
		})
	}
}

func TestOnlyTheOperatorMayInterruptTheLead(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("lead", "", "hook busy", ""))

	mustFail(t, v.run("lead", "", "lead interrupt", ""), "not for a lead")
	mustFail(t, v.run("worker-t1", "t1", "lead interrupt", ""), "not for a worker")
	if len(v.l.escaped) != 0 {
		t.Fatalf("escaped %v", v.l.escaped)
	}
}
