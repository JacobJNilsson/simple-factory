package engine_test

import (
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

func TestRunnableRefusesAShellAgentWhereTheEngineCannotRunIt(t *testing.T) {
	for name, lists := range map[string]string{
		"after the reviewer":    `[lists.build]` + "\n" + `agents = ["worker", "check", "reviewer", "gate"]`,
		"after the push":        `[lists.ship]` + "\n" + `agents = ["push", "gate", "land"]`,
		"before integrate":      `[lists.land]` + "\n" + `agents = ["gate", "integrate"]`,
		"first in held":         `[lists.held]` + "\n" + `agents = ["gate", "release", "ship"]`,
		"a list after the push": `[lists.land]` + "\n" + `agents = ["integrate", "push"]`,
	} {
		if err := engine.Runnable(parseProcess(t, gateAgent+lists)); err == nil {
			t.Errorf("a gate %s loaded", name)
		}
	}
}

func TestRunnableRefusesABranchThatTheEngineCannotFollow(t *testing.T) {
	for name, text := range map[string]string{
		"a fail to a list after the review": gateAgent + `
[lists.build]
agents = ["worker", "gate", "check", "reviewer"]
[lists.build.then]
fail.gate = "land"`,
		"a pass back to the worker": `
[lists.build.then]
pass = "build"`,
		"a pass from land to another list": `
[lists.land.then]
pass = "ship"`,
	} {
		if err := engine.Runnable(parseProcess(t, text)); err == nil {
			t.Errorf("%s loaded", name)
		}
	}
}

func TestRunnableRefusesAKeyThatTheEngineWouldIgnore(t *testing.T) {
	for name, text := range map[string]string{
		"edit on the reviewer":      "[agents.reviewer.permissions]\nedit = true\n",
		"another worker prompt":     "[agents.worker]\nprompt = \"loader\"\n",
		"receives on the reviewer":  "[agents.reviewer]\nreceives = \"previous\"\n",
		"feedback on the push":      "[agents.push]\nfeedback = \"reopen\"\n",
		"a model on the push":       "[agents.push]\nmodel = \"opus\"\n",
		"count_round on a gate":     gateAgent + "count_round = true\n",
		"pass_env on a gate":        gateAgent + "permissions = { pass_env = [\"TOKEN\"] }\n",
		"deny_read on the worker":   "[agents.worker.permissions]\npush = true\ndeny_read = []\n",
		"a claude agent of its own": "[agents.drafter]\nharness = \"claude\"\n",
	} {
		if err := engine.Runnable(parseProcess(t, text)); err == nil {
			t.Errorf("%s loaded", name)
		}
	}
}
