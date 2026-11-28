package engine_test

// These tests check sfy lead command: the operator types one slash command into the lead when it waits,
// and sfy holds the command while the lead does not wait. sfy refuses the commands that end or replace the lead's conversation.

import (
	"slices"
	"testing"
)

func TestLeadCommandTypesTheTextIntoALeadThatWaits(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("lead", "", "hook idle", ""))

	a := v.run("", "", "lead command", "", "/effort", "high")

	mustOK(t, a)
	if want := []string{"lead: /effort high"}; !slices.Equal(v.l.sent, want) || a.Text != "Typed /effort high." {
		t.Fatalf("typed %q, text %q", v.l.sent, a.Text)
	}
}

func TestLeadCommandHoldsTheTextUntilTheTurnOfTheLeadEnds(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("lead", "", "hook busy", ""))

	a := v.run("", "", "lead command", "", "/model", "opus")

	mustOK(t, a)
	if len(v.l.sent) != 0 || a.Text != "The lead does not wait for input now. sfy types /model opus when it waits." {
		t.Fatalf("typed %q, text %q", v.l.sent, a.Text)
	}
	mustOK(t, v.run("lead", "", "hook turn-ended", ""))
	if want := []string{"lead: /model opus"}; !slices.Equal(v.l.sent, want) {
		t.Fatalf("typed %q, want %q", v.l.sent, want)
	}
}

func TestLeadCommandRefusesTextThatIsNotOneAllowedSlashCommand(t *testing.T) {
	cases := []struct {
		text    string
		mention string
	}{
		{"", "starts with /"},
		{"effort high", "starts with /"},
		{"/model opus\n/exit", "starts with /"},
		{"/model \x1b[A", "starts with /"},
		{"/clear", "refuses /clear"},
		{"/CLEAR now", "refuses /clear"},
		{"/reset", "refuses /reset"},
		{"/new", "refuses /new"},
		{"/exit", "refuses /exit"},
		{"/quit", "refuses /quit"},
		{"/logout", "refuses /logout"},
	}
	for _, c := range cases {
		v := newEnv(t)
		mustOK(t, v.run("lead", "", "hook idle", ""))

		mustFail(t, v.run("", "", "lead command", c.text), c.mention)
		if len(v.l.sent) != 0 {
			t.Errorf("%q: typed %q", c.text, v.l.sent)
		}
	}
}

func TestLeadCommandCancelDropsTheHeldCommands(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("", "", "lead command", "", "/model", "opus"))
	mustOK(t, v.run("", "", "lead command", "", "/effort", "high"))

	a := v.run("", "", "lead command", "", "--cancel")
	mustOK(t, v.run("lead", "", "hook idle", ""))

	mustOK(t, a)
	if len(v.l.sent) != 0 || a.Text != "Dropped 2 held commands." {
		t.Fatalf("typed %q, text %q", v.l.sent, a.Text)
	}
}

func TestLeadCommandWithoutALeadFails(t *testing.T) {
	v := newEnv(t)
	v.l.dead = map[string]bool{"lead": true}

	mustFail(t, v.run("", "", "lead command", "", "/compact"), "no lead runs")
}

func TestOnlyTheOperatorMayRunLeadCommand(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("lead", "", "hook idle", ""))

	mustFail(t, v.run("lead", "", "lead command", "", "/compact"), "not for a lead")
	mustFail(t, v.run("worker-t1", "t1", "lead command", "", "/compact"), "not for a worker")
	if len(v.l.sent) != 0 {
		t.Fatalf("typed %q", v.l.sent)
	}
}
