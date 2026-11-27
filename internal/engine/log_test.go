package engine_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// logLines sends the engine's log to a buffer and returns a function that reads its lines.
func (v env) logLines() func() []string {
	var b bytes.Buffer
	v.e.SetLog(&b)
	return func() []string {
		s := strings.TrimSpace(b.String())
		if s == "" {
			return nil
		}
		return strings.Split(s, "\n")
	}
}

func TestABlockedReportInFeedbackReachesTheLeadAndKeepsTheSlot(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Feedback, 2)

	mustOK(t, v.run("worker-t1", "t1", "report blocked", "Which branch?"))

	got := v.inbox(t, "lead")
	if len(got) != 1 || !strings.Contains(got[0].Body, "Task t1 is blocked") {
		t.Fatalf("the lead got %v", got)
	}
	if s := v.state(t, "t1").State; s != task.Feedback {
		t.Fatalf("got %s", s)
	}
}

func TestAnAcceptedReportIsLogged(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Feedback, 2)
	lines := v.logLines()

	mustOK(t, v.run("worker-t1", "t1", "report blocked", "Which branch?"))

	got := lines()
	if len(got) != 1 || !strings.Contains(got[0], `accepted "report blocked" from worker-t1 on task t1: "Which branch?"`) {
		t.Fatalf("log %q", got)
	}
}

func TestAnAcceptedVerdictIsLogged(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)
	lines := v.logLines()

	mustOK(t, v.run("review-t1-1", "t1", "verdict fail", "Not yet."))

	var found bool
	for _, l := range lines() {
		found = found || strings.Contains(l, `accepted "verdict fail" from review-t1-1 on task t1: "Not yet."`)
	}
	if !found {
		t.Fatalf("log %q", lines())
	}
}

func TestARefusedReportIsLogged(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Feedback, 2)
	lines := v.logLines()

	mustFail(t, v.run("worker-t1", "t2", "report blocked", "Stuck."), "")
	mustFail(t, v.run("worker-t9", "t1", "report blocked", "Stuck."), "")

	got := lines()
	if len(got) != 2 ||
		!strings.Contains(got[0], `refused "report blocked" from worker-t1 on task t2`) ||
		!strings.Contains(got[1], `refused "report blocked" from worker-t9 on task t1`) {
		t.Fatalf("log %q", got)
	}
	if got := v.inbox(t, "lead"); len(got) != 0 {
		t.Fatalf("the lead got %v", got)
	}
}

func TestARefusedTokenIsLoggedWithoutTheToken(t *testing.T) {
	v := authEnv(t)
	lines := v.logLines()

	ans := v.e.Handle(protocol.Request{Caller: protocol.Caller{Agent: "worker-t1", Task: "t1", Token: "secret-token"}, Command: "report blocked", Body: "Stuck."})

	if ans.OK {
		t.Fatalf("got %+v", ans)
	}
	got := strings.Join(lines(), "\n")
	if !strings.Contains(got, "refused") || strings.Contains(got, "secret-token") {
		t.Fatalf("log %q", got)
	}
}

func TestTheLogClipsALongReport(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Feedback, 2)
	lines := v.logLines()
	body := strings.Repeat("a", 200) + "x" + strings.Repeat("c", 99)

	mustOK(t, v.run("worker-t1", "t1", "report blocked", body))

	got := strings.Join(lines(), "\n")
	if !strings.Contains(got, strings.Repeat("a", 200)+`..."`) || strings.Contains(got, "x") {
		t.Fatalf("log %q", got)
	}
}

func TestAnAcceptedCommandThatIsNotAReportIsNotLogged(t *testing.T) {
	v := newEnv(t)
	lines := v.logLines()

	mustOK(t, v.run("lead", "", "task new", taskFile))

	if got := lines(); len(got) != 0 {
		t.Fatalf("log %q", got)
	}
}
