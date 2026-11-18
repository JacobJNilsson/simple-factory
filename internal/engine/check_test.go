package engine_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// fakeChecker records each Start call and never calls done itself. A test calls the recorded done once Handle has returned.
type fakeChecker struct {
	dirs, commands []string
	dones          []func(engine.CheckResult)
}

func (f *fakeChecker) Start(dir, command string, done func(engine.CheckResult)) {
	f.dirs, f.commands, f.dones = append(f.dirs, dir), append(f.commands, command), append(f.dones, done)
}

const checkCommand = "make check"

// seedChecked seeds a task with a check and an engine that has a fake checker.
func (v *env) seedChecked(t *testing.T, state task.State, round int) *fakeChecker {
	t.Helper()
	cfg := config.Config{ReviewRounds: 3, MaxWorkers: 2, CheckTimeout: 20 * time.Minute}
	v.e = engine.New(v.st, v.l, v.ws, cfg, v.clock)
	fc := &fakeChecker{}
	v.e.SetChecker(fc)
	v.seed(t, state, round)
	tk := v.state(t, "t1")
	tk.Check = checkCommand
	if err := v.st.Save(tk); err != nil {
		t.Fatal(err)
	}
	return fc
}

func (v env) reviewerPrompts() []string {
	var out []string
	for _, s := range v.l.started {
		if s.Kind == "reviewer" {
			out = append(out, s.Prompt)
		}
	}
	return out
}

func workerBody(t *testing.T, v env) string {
	t.Helper()
	var all []string
	for _, m := range v.inbox(t, "worker-t1") {
		all = append(all, m.Body)
	}
	return strings.Join(all, "\n---\n")
}

func TestReportDoneOnATaskWithACheckRunsTheCheckAndStartsNoReviewer(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)

	a := v.run("worker-t1", "t1", "report done", "All done.")

	mustOK(t, a)
	if !strings.Contains(a.Text, checkCommand) {
		t.Errorf("answer %q does not name the command", a.Text)
	}
	if tk := v.state(t, "t1"); tk.State != task.Working || tk.Round != 0 {
		t.Errorf("got %s round %d", tk.State, tk.Round)
	}
	if len(v.reviewerPrompts()) != 0 {
		t.Error("a reviewer started before the check ended")
	}
	if len(fc.commands) != 1 || fc.commands[0] != checkCommand || fc.dirs[0] != "/work/t1" {
		t.Errorf("checker calls: %v in %v", fc.commands, fc.dirs)
	}
}

func TestStatusShowsThatACheckRunsUntilItEnds(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report done", "All done."))

	v.statusShows(t, "t1", "working (check runs)")
	fc.dones[0](engine.CheckResult{})

	v.statusShows(t, "t1", "review")
}

func TestAPassedCheckStartsTheReviewerAtRoundOne(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report done", "All done."))

	fc.dones[0](engine.CheckResult{})

	if tk := v.state(t, "t1"); tk.State != task.Review || tk.Round != 1 {
		t.Fatalf("got %s round %d", tk.State, tk.Round)
	}
	prompts := v.reviewerPrompts()
	if len(prompts) != 1 || !strings.Contains(prompts[0], "it passed") || !strings.Contains(prompts[0], "All done.") {
		t.Fatalf("reviewer prompts: %q", prompts)
	}
	if v.l.started[len(v.l.started)-1].Name != "review-t1-1" {
		t.Errorf("started %+v", v.l.started)
	}
}

func TestAFailedCheckSendsTheOutputToTheWorkerAndStartsNoReviewer(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report done", "All done."))

	fc.dones[0](engine.CheckResult{Err: errors.New("exit status 2"), Output: "FAIL: TestX\n"})

	if len(v.reviewerPrompts()) != 0 {
		t.Error("a reviewer started after a failed check")
	}
	if tk := v.state(t, "t1"); tk.State != task.Feedback || tk.Round != 1 {
		t.Errorf("got %s round %d", tk.State, tk.Round)
	}
	body := workerBody(t, v)
	for _, want := range []string{"no review started", checkCommand, "exit status 2", "FAIL: TestX"} {
		if !strings.Contains(body, want) {
			t.Errorf("message lacks %q:\n%s", want, body)
		}
	}
}

func TestAFailedCheckCountsTowardTheReviewRounds(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Feedback, 2)
	mustOK(t, v.run("worker-t1", "t1", "report done", "Again."))

	fc.dones[0](engine.CheckResult{Err: errors.New("exit status 1")})

	if tk := v.state(t, "t1"); tk.State != task.Escalated || tk.Before != task.Feedback || tk.Round != 3 {
		t.Fatalf("got %s before %s round %d", tk.State, tk.Before, tk.Round)
	}
	if msgs := v.inbox(t, "main"); len(msgs) != 1 || !strings.Contains(msgs[0].Body, "failed its check") {
		t.Errorf("main inbox: %+v", msgs)
	}
	if !strings.Contains(workerBody(t, v), "no review started") {
		t.Error("the worker got no message")
	}
}

func TestATimedOutCheckSaysItDidNotFinishWithinTheTimeout(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report done", "x"))

	fc.dones[0](engine.CheckResult{Err: errors.New("signal: killed"), TimedOut: true})

	if body := workerBody(t, v); !strings.Contains(body, "did not finish within 20m0s") {
		t.Errorf("message:\n%s", body)
	}
}

func TestACheckWithCutOutputNamesTheCutBytes(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report done", "x"))

	fc.dones[0](engine.CheckResult{Err: errors.New("exit status 1"), Cut: 4242, Output: "tail\n"})

	if body := workerBody(t, v); !strings.Contains(body, "4242 bytes") {
		t.Errorf("message:\n%s", body)
	}
}

func TestASecondReportDoneDuringACheckFails(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report done", "x"))

	mustFail(t, v.run("worker-t1", "t1", "report done", "x"), "already runs the check")

	if len(fc.commands) != 1 {
		t.Errorf("checker calls: %d", len(fc.commands))
	}
}

func TestACancelledTaskIgnoresTheResultOfItsCheck(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report done", "x"))
	mustOK(t, v.run("main", "", "task cancel", "", "t1"))

	fc.dones[0](engine.CheckResult{})

	if len(v.reviewerPrompts()) != 0 || len(v.inbox(t, "worker-t1")) != 0 {
		t.Error("the result of a dropped check had an effect")
	}
	if tk := v.state(t, "t1"); tk.State != task.Cancelled {
		t.Errorf("state %s", tk.State)
	}
}

func TestAnEditThatChangesTheCheckDropsTheResultAndTellsTheWorker(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report done", "x"))
	mustOK(t, v.run("main", "", "task edit", taskFile+"\n## Check\n\nmake other\n", "t1"))

	fc.dones[0](engine.CheckResult{})

	if len(v.reviewerPrompts()) != 0 {
		t.Error("a reviewer started for a changed check")
	}
	if body := workerBody(t, v); !strings.Contains(body, "changed while it ran") {
		t.Errorf("message:\n%s", body)
	}
}

func TestAPassedCheckWhoseReviewerFailsToStartTellsTheWorker(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report done", "x"))
	v.l.startErr = errors.New("no pane")

	fc.dones[0](engine.CheckResult{})

	if tk := v.state(t, "t1"); tk.State != task.Working || tk.Round != 0 {
		t.Errorf("got %s round %d", tk.State, tk.Round)
	}
	if body := workerBody(t, v); !strings.Contains(body, "reviewer did not start") {
		t.Errorf("message:\n%s", body)
	}
}

func TestATaskWithoutACheckStartsItsReviewerAtOnce(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)
	tk := v.state(t, "t1")
	tk.Check = ""
	if err := v.st.Save(tk); err != nil {
		t.Fatal(err)
	}

	mustOK(t, v.run("worker-t1", "t1", "report done", "x"))

	if len(fc.commands) != 0 || len(v.reviewerPrompts()) != 1 {
		t.Errorf("checker calls %d, reviewers %d", len(fc.commands), len(v.reviewerPrompts()))
	}
	if strings.Contains(v.reviewerPrompts()[0], "sfy ran the check") {
		t.Error("the prompt names a check that did not run")
	}
}

func TestATaskWithACheckAndNoCheckerStartsItsReviewerAtOnce(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	tk := v.state(t, "t1")
	tk.Check = checkCommand
	if err := v.st.Save(tk); err != nil {
		t.Fatal(err)
	}

	mustOK(t, v.run("worker-t1", "t1", "report done", "x"))

	if len(v.reviewerPrompts()) != 1 {
		t.Errorf("reviewers %d", len(v.reviewerPrompts()))
	}
}
