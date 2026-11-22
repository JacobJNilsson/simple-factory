package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// gateProcess adds a gate agent before the default check, as a config.d file of a factory would.
const gateProcess = `
[agents.gate]
harness = "shell"
run = ["sh gate.sh {task_file}"]
show = "{from} (gate runs)"

[lists.build]
agents = ["worker", "gate", "check", "reviewer"]

[lists.build.then]
fail.gate = { list = "build", round = true }
`

// seedGate seeds a task with a check and an engine that runs gateProcess with a fake checker.
func (v *env) seedGate(t *testing.T, text string) *fakeChecker {
	t.Helper()
	fc := v.seedChecked(t, task.Working, 0)
	p, err := process.Parse("claude", text)
	if err != nil {
		t.Fatal(err)
	}
	v.e.SetProcess(p)
	return fc
}

func TestAGateRunsBeforeTheCheckAndTheReviewer(t *testing.T) {
	v := newEnv(t)
	fc := v.seedGate(t, gateProcess)
	mustOK(t, v.run("worker-t1", "t1", "report done", "All done."))

	if len(fc.commands) != 1 || !strings.HasPrefix(fc.commands[0], "sh gate.sh '") {
		t.Fatalf("checker calls: %v, want the gate first", fc.commands)
	}
	v.statusShows(t, "t1", "working (gate runs)")
	fc.dones[0](engine.CheckResult{})
	if len(fc.commands) != 2 || fc.commands[1] != checkCommand {
		t.Fatalf("checker calls: %v, want the check after the gate", fc.commands)
	}
	if len(v.reviewerPrompts()) != 0 {
		t.Fatal("a reviewer started before the check ended")
	}
	fc.dones[1](engine.CheckResult{})
	if tk := v.state(t, "t1"); tk.State != task.Review || tk.Round != 1 {
		t.Fatalf("got %s round %d, want review round 1", tk.State, tk.Round)
	}
}

func TestAFailedGateCountsARoundAndStartsNoCheck(t *testing.T) {
	v := newEnv(t)
	fc := v.seedGate(t, gateProcess)
	mustOK(t, v.run("worker-t1", "t1", "report done", "All done."))

	fc.dones[0](engine.CheckResult{Err: errors.New("exit status 1"), Output: "gap\n"})

	if len(fc.commands) != 1 {
		t.Fatalf("checker calls: %v, want no check after a failed gate", fc.commands)
	}
	if tk := v.state(t, "t1"); tk.State != task.Feedback || tk.Round != 1 {
		t.Fatalf("got %s round %d, want feedback round 1", tk.State, tk.Round)
	}
	if !strings.Contains(workerBody(t, v), "gap") {
		t.Fatal("the worker did not get the gate's output")
	}
}

func TestResultsHoldEachAgentsResultInOrder(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report done", "All done."))
	fc.dones[0](engine.CheckResult{Output: "ok\n"})

	results, err := v.st.Results("t1")
	if err != nil {
		t.Fatal(err)
	}
	var agents []string
	for _, r := range results {
		agents = append(agents, r.Agent)
	}
	if strings.Join(agents, " ") != "worker check" || results[0].Body != "All done." || results[1].Body != "ok\n" {
		t.Fatalf("results = %+v, want the worker's report and the check's output", results)
	}
}

const dropProcess = `
[agents.check]
run = ["{task_check} > {drop}/note.md"]
`

func TestAShellAgentsDropReachesTheLead(t *testing.T) {
	v := newEnv(t)
	fc := v.seedGate(t, dropProcess)
	mustOK(t, v.run("worker-t1", "t1", "report done", "All done."))
	if !strings.Contains(fc.commands[0], "/tasks/t1/drop/2-check'") {
		t.Fatalf("command %q names no fresh drop folder", fc.commands[0])
	}

	fc.dones[0](engine.CheckResult{Drops: [][]byte{[]byte("To: lead\n\nThe check passed with a gap.\n")}})

	var from []string
	for _, m := range v.inbox(t, "lead") {
		if strings.Contains(m.Body, "with a gap") {
			from = append(from, m.From)
		}
	}
	if len(from) != 1 || from[0] != "check@t1" {
		t.Fatalf("lead messages from %v, want one from check@t1", from)
	}
}
