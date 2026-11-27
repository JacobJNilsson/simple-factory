package engine_test

import (
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/mux"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

const unstickNote = "The lead pressed Escape at your permission prompt, so sfy denied the tool call. " +
	"Do the work another way inside your folder, or run sfy report blocked <what and why>."

// mustUnstick runs unstick as the lead and checks the key press and the message to the agent.
func mustUnstick(t *testing.T, v env, agent string) {
	t.Helper()
	mustOK(t, v.run("lead", "", "unstick", "", agent))

	if len(v.l.escaped) != 1 || v.l.escaped[0] != agent {
		t.Fatalf("escaped %v, want [%s]", v.l.escaped, agent)
	}
	msgs := v.inbox(t, agent)
	if len(msgs) != 1 || msgs[0].Body != unstickNote || msgs[0].From != "lead" {
		t.Fatalf("inbox %+v", msgs)
	}
}

func TestUnstickPressesEscapeInTheWorkerAndTellsItToGoOn(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	mustUnstick(t, v, "worker-t1")
}

func TestUnstickPressesEscapeInTheReviewerAndTellsItToGoOn(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	mustUnstick(t, v, "review-t1-1")
}

func TestUnstickRefusesAnAgentThatIsNotARunningTaskSession(t *testing.T) {
	cases := []struct {
		state   task.State
		round   int
		agent   string
		mention string
	}{
		{task.Working, 0, "lead", "lead"},
		{task.Working, 0, "main", "main"},
		{task.Working, 0, "other/worker-t1", "this factory"},
		{task.Working, 0, "", "agent name"},
		{task.Done, 0, "worker-t1", "worker-t1"},
		{task.Working, 0, "worker-t9", "worker-t9"},
		{task.Working, 0, "nobody", "nobody"},
	}
	for _, c := range cases {
		v := newEnv(t)
		v.seed(t, c.state, c.round)

		mustFail(t, v.run("lead", "", "unstick", "", c.agent), c.mention)
		if len(v.l.escaped) != 0 {
			t.Errorf("%q: escaped %v", c.agent, v.l.escaped)
		}
	}
}

func TestOnlyTheLeadMayUnstick(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	mustFail(t, v.run("worker-t1", "t1", "unstick", "", "review-t1-1"), "not for a worker")
	mustFail(t, v.run("review-t1-1", "t1", "unstick", "", "worker-t1"), "not for a reviewer")
	mustFail(t, v.run("", "", "unstick", "", "worker-t1"), "not for the operator")
	if len(v.l.escaped) != 0 {
		t.Fatalf("escaped %v", v.l.escaped)
	}
}

func TestUnstickWithoutAnEscapeTemplateFailsAndSendsNothing(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.l.escapeErr = mux.ErrNoEscape

	mustFail(t, v.run("lead", "", "unstick", "", "worker-t1"), mux.ErrNoEscape.Error())
	if msgs := v.inbox(t, "worker-t1"); len(msgs) != 0 {
		t.Fatalf("inbox %+v", msgs)
	}
}
