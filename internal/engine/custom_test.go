package engine_test

import (
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// ingest runs the ingestion process with a fake checker and starts the loader of a queued task with a repo and a check.
func (v env) ingest(t *testing.T) *fakeChecker {
	t.Helper()
	fc := &fakeChecker{}
	v.e.SetChecker(fc)
	v.setProcess(t, ingestionText(t))
	tk := task.Task{ID: "t1", Title: "Title", Description: "Do it.", Goal: "Done.", Repo: "some/repo", Check: checkCommand, State: task.Queued, Source: "lead"}
	if err := v.st.Create(tk); err != nil {
		t.Fatal(err)
	}
	v.tick(t)
	return fc
}

// passAll passes each command that the checker started from index from on, in order, with the last one printing output.
func passAll(fc *fakeChecker, from int, output string) {
	for i := from; i < len(fc.dones); i++ {
		r := engine.CheckResult{}
		if i == len(fc.dones)-1 {
			r.Output = output
		}
		fc.dones[i](r)
	}
}

// loadAndAudit runs the loader, the profile checks and a passing audit of an ingestion task, up to the route agent.
func (v env) loadAndAudit(t *testing.T, fc *fakeChecker) {
	t.Helper()
	mustOK(t, v.run("loader-t1", "t1", "report done", "Loaded."))
	v.statusShows(t, "t1", "load (checks run)")
	fc.dones[0](engine.CheckResult{})
	fc.dones[1](engine.CheckResult{})
	if tk := v.state(t, "t1"); tk.State != task.Review || tk.Position() != (process.Position{List: "load", Agent: "auditor"}) {
		t.Fatalf("got %s at %s, want review at load/auditor", tk.State, tk.Position())
	}
	mustOK(t, v.run("auditor-t1-1", "t1", "verdict pass", "The data matches."))
	if n := len(fc.commands); n != 3 || !strings.Contains(fc.commands[2], "ingest.sh route") {
		t.Fatalf("checker calls: %v, want the route agent third", fc.commands)
	}
}

func TestTheIngestionProcessStartsTheLoaderInItsOwnSession(t *testing.T) {
	v := newEnv(t)
	v.ingest(t)

	if len(v.l.started) != 1 {
		t.Fatalf("started %v, want the loader", v.startedNames())
	}
	s := v.l.started[0]
	if s.Name != "loader-t1" || s.Kind != "worker" || s.Agent != "loader" || s.Role != "loader" || s.RepoRole != "worker-repo" || s.Model != "sonnet" {
		t.Fatalf("loader spec %+v", s)
	}
	if tk := v.state(t, "t1"); tk.Position() != (process.Position{List: "load", Agent: "loader"}) {
		t.Fatalf("position %s, want load/loader", tk.Position())
	}
}

func TestTheIngestionProcessReachesDoneThroughOther(t *testing.T) {
	v := newEnv(t)
	fc := v.ingest(t)
	v.loadAndAudit(t, fc)

	fc.dones[2](engine.CheckResult{Output: "checking the destination\nother\n"})

	if tk := v.state(t, "t1"); tk.State != task.Review || !tk.Pushing || tk.Position() != (process.Position{List: "other", Agent: "publish"}) {
		t.Fatalf("got %s at %s, want a push at other/publish", tk.State, tk.Position())
	}
	if notes := v.inbox(t, "loader-t1"); len(notes) == 0 || !strings.Contains(notes[len(notes)-1].Body, "Push the branch now") {
		t.Fatalf("loader inbox %v, want the push request", notes)
	}
	mustOK(t, v.run("loader-t1", "t1", "report pushed", "Pushed."))
	if n := len(fc.commands); n != 4 || !strings.Contains(fc.commands[3], "ingest.sh promote") {
		t.Fatalf("checker calls: %v, want promote after the push", fc.commands)
	}
	fc.dones[3](engine.CheckResult{})
	passAll(fc, 4, "")
	if tk := v.state(t, "t1"); tk.State != task.Done {
		t.Fatalf("got %s, want done after verify", tk.State)
	}
}

func TestTheIngestionProcessWaitsForTheOperatorOnTheWayToProduction(t *testing.T) {
	v := newEnv(t)
	fc := v.ingest(t)
	v.loadAndAudit(t, fc)

	fc.dones[2](engine.CheckResult{Output: "production\n"})

	if tk := v.state(t, "t1"); tk.State != task.Held || tk.Position() != (process.Position{List: "production", Agent: "release"}) {
		t.Fatalf("got %s at %s, want held at production/release", tk.State, tk.Position())
	}
	if lead := v.inbox(t, "lead"); len(lead) == 0 || !strings.Contains(lead[len(lead)-1].Body, "sfy task release t1") {
		t.Fatalf("lead inbox %v, want the release note", lead)
	}
	mustOK(t, v.run("", "", "task release", "", "t1"))
	last := v.l.started[len(v.l.started)-1]
	if last.Name != "loader-t1" || last.NoPush {
		t.Fatalf("started %+v, want the loader's session with push permission", last)
	}
	if tk := v.state(t, "t1"); tk.Position() != (process.Position{List: "production", Agent: "publish"}) {
		t.Fatalf("position %s, want production/publish", tk.Position())
	}
}

func TestARouteThatNamesNoTargetFollowsFail(t *testing.T) {
	v := newEnv(t)
	fc := v.ingest(t)
	v.loadAndAudit(t, fc)

	fc.dones[2](engine.CheckResult{Output: "staging\n"})

	if tk := v.state(t, "t1"); tk.State != task.Escalated {
		t.Fatalf("got %s, want escalated by the default fail target", tk.State)
	}
}

func TestAReviewerOfItsOwnNameMayNotReportDone(t *testing.T) {
	v := newEnv(t)
	fc := v.ingest(t)
	mustOK(t, v.run("loader-t1", "t1", "report done", "Loaded."))
	passAll(fc, 0, "")

	mustFail(t, v.run("auditor-t1-1", "t1", "report done", "Done."), "is not for a reviewer")
	mustFail(t, v.run("nobody-t1", "t1", "report done", "Done."), "unknown caller")
}

func TestADraftTaskLandsWithoutAPush(t *testing.T) {
	v := newEnv(t)
	v.setProcess(t, draftProcess)
	tk := task.Task{ID: "t1", Title: "Title", Description: "Do it.", Goal: "Done.", Repo: "some/repo", List: "draft", State: task.Queued, Source: "lead"}
	if err := v.st.Create(tk); err != nil {
		t.Fatal(err)
	}
	v.tick(t)
	if got := v.state(t, "t1").Position(); got != (process.Position{List: "draft", Agent: "worker"}) {
		t.Fatalf("position %s, want draft/worker", got)
	}
	mustOK(t, v.run("worker-t1", "t1", "report done", "Drafted."))
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))

	if got := v.state(t, "t1"); got.State != task.Approved {
		t.Fatalf("got %s, want approved", got.State)
	}
	if notes := v.pushNotes(t); len(notes) != 0 {
		t.Fatalf("the worker got a push request: %v", notes)
	}
}

func TestAProcessChangeAppliesAtTheNextAgent(t *testing.T) {
	v := newEnv(t)
	fc := v.seedChecked(t, task.Working, 0)
	v.setProcess(t, "[lists.build]\nagents = [\"worker\", \"reviewer\"]\n")

	mustOK(t, v.run("worker-t1", "t1", "report done", "Done."))

	if len(fc.commands) != 0 || len(v.reviewerPrompts()) != 1 {
		t.Fatalf("checker calls %v and reviewers %d, want the reviewer at once", fc.commands, len(v.reviewerPrompts()))
	}
}

func TestTaskPushStaysTheOldNameOfTaskRelease(t *testing.T) {
	v := newEnv(t)
	v.heldReview(t)

	mustOK(t, v.run("lead", "", "task release", "", "t1"))

	if tk := v.state(t, "t1"); tk.State != task.Review || !tk.Pushing || !tk.Released {
		t.Fatalf("got %+v, want a released push", tk)
	}
}
