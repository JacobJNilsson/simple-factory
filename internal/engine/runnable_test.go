package engine_test

import (
	"errors"
	"os"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/process"
)

const gateAgent = `
[agents.gate]
harness = "shell"
run = ["sh gate.sh"]
`

func parseProcess(t *testing.T, text string) process.Process {
	t.Helper()
	p, err := process.Parse("claude", text)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(nil); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunnableAcceptsTheDefaultAndAGateBeforeTheReviewer(t *testing.T) {
	for name, text := range map[string]string{
		"the default":              "",
		"a gate before the review": gateProcess,
		"a worker's own allow list": `
[agents.worker.permissions]
push = true
allow = ["Bash(gh pr view *)"]`,
		"a model for the reviewer": "[agents.reviewer]\nmodel = \"opus\"\n",
		"a gate outside the sandbox": gateAgent + `permissions = { sandbox = false, deny_read = ["~/.ssh"] }
[lists.build]
agents = ["worker", "gate", "check", "reviewer"]`,
	} {
		if err := engine.Runnable(parseProcess(t, text)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// ingestionText returns the ingestion process of the factory plan, which the process package keeps as a fixture.
func ingestionText(t *testing.T) string {
	t.Helper()
	text, err := os.ReadFile("../process/testdata/ingestion.toml")
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func TestRunnableAcceptsAProcessOfItsOwn(t *testing.T) {
	for name, text := range map[string]string{
		"the ingestion process":   ingestionText(t),
		"a gate after the review": `[lists.build]` + "\n" + `agents = ["worker", "check", "reviewer", "gate"]` + gateAgent,
		"a gate before integrate": `[lists.land]` + "\n" + `agents = ["gate", "integrate"]` + gateAgent,
		"a gate first in held":    `[lists.held]` + "\n" + `agents = ["gate", "release", "ship"]` + gateAgent,
		"a pass back to the worker": `
[lists.build.then]
pass = "build"`,
		"a pass from land to another list": `
[lists.land.then]
pass = "ship"`,
		"a fail to a list after the review": gateAgent + `
[lists.build]
agents = ["worker", "gate", "check", "reviewer"]
[lists.build.then]
fail.gate = "land"`,
		"a claude agent of its own": "[agents.drafter]\nharness = \"claude\"\n",
		"another worker prompt":     "[agents.worker]\nprompt = \"loader\"\n",
		"edit on the reviewer":      "[agents.reviewer.permissions]\nedit = true\n",
		"receives on the reviewer":  "[agents.reviewer]\nreceives = \"previous\"\n",
		"feedback on the push":      "[agents.push]\nfeedback = \"reopen\"\n",
		"a model on the push":       "[agents.push]\nmodel = \"opus\"\n",
		"count_round on a gate":     gateAgent + "count_round = true\n",
		"pass_env on a gate":        gateAgent + "permissions = { pass_env = [\"TOKEN\"] }\n",
		"deny_read on the worker":   "[agents.worker.permissions]\npush = true\ndeny_read = []\n",
	} {
		if err := engine.Runnable(parseProcess(t, text)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRunnableRefusesWhatTheEngineCannotRun(t *testing.T) {
	cases := map[string]struct{ text, key string }{
		"a start list that begins with a shell agent": {"start = \"first\"\n" + gateAgent + "[lists.first]\nagents = [\"gate\", \"worker\"]\n", "start"},
		"a lead agent that holds a slot":              {"[agents.integrate]\nslot = true\n", "agents.integrate.slot"},
		"feedback on a shell agent":                   {gateAgent + "feedback = \"reopen\"\n", "agents.gate.feedback"},
		"every result for a shell agent":              {gateAgent + "receives = \"all\"\n", "agents.gate.receives"},
		"feedback to a list without a worker first":   {"[agents.reviewer]\nfeedback = \"land\"\n", "agents.reviewer.feedback"},
		"a verdict in the worker's session":           {"[agents.selfcheck]\nlike = \"worker\"\nends = \"verdict\"\n", "agents.selfcheck.like"},
		"push rules for a reviewer":                   {"[agents.reviewer.permissions]\nedit = false\npush = true\n", "agents.reviewer.permissions.push"},
	}
	for name, c := range cases {
		if err := engine.Runnable(parseProcess(t, c.text)); !hasKey(err, c.key) {
			t.Errorf("%s: Runnable = %v, want an error that names %s", name, err, c.key)
		}
	}
}

// hasKey reports whether err or one of the errors that it joins is a KeyError of key.
func hasKey(err error, key string) bool {
	var keyErr *process.KeyError
	if errors.As(err, &keyErr) && keyErr.Key == key {
		return true
	}
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		return false
	}
	for _, e := range joined.Unwrap() {
		if errors.As(e, &keyErr) && keyErr.Key == key {
			return true
		}
	}
	return false
}
