package engine_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/factory/internal/config"
	"github.com/JacobJNilsson/factory/internal/engine"
	"github.com/JacobJNilsson/factory/internal/protocol"
	"github.com/JacobJNilsson/factory/internal/store"
	"github.com/JacobJNilsson/factory/internal/task"
)

type fakeLauncher struct {
	startErr error
	stopErr  error
	started  []engine.SessionSpec
	stopped  []string
	notified []string
}

func (f *fakeLauncher) Start(s engine.SessionSpec) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, s)
	return nil
}
func (f *fakeLauncher) Send(string, string) error  { return nil }
func (f *fakeLauncher) Alive(string) (bool, error) { return true, nil }
func (f *fakeLauncher) Stop(name string) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stopped = append(f.stopped, name)
	return nil
}
func (f *fakeLauncher) Notify(name, text string) error {
	f.notified = append(f.notified, name+": "+text)
	return nil
}

type fakeWorkspaces struct{ removed []string }

func (f *fakeWorkspaces) Create(t task.Task) (string, error) { return "/work/" + t.ID, nil }
func (f *fakeWorkspaces) Remove(t task.Task) error           { f.removed = append(f.removed, t.ID); return nil }

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type env struct {
	home string
	st   store.Store
	l    *fakeLauncher
	ws   *fakeWorkspaces
	e    *engine.Engine
}

func newEnv(t *testing.T) env {
	t.Helper()
	home := t.TempDir()
	st := store.Files(home)
	l, ws := &fakeLauncher{}, &fakeWorkspaces{}
	cfg := config.Config{ReviewRounds: 3, Models: config.Models{Reviewer: "rev-model"}}
	clock := &fakeClock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	return env{home, st, l, ws, engine.New(st, l, ws, cfg, clock)}
}

const taskFile = "# Title\n\n## Description\n\nDo it.\n\n## Goal\n\nDone.\n"

func (v env) seed(t *testing.T, state task.State, round int) {
	t.Helper()
	tk := task.Task{ID: "t1", Title: "Title", Description: "Do it.", Goal: "Done.", State: state, Round: round, Source: "main"}
	if state != task.New && state != task.Queued {
		tk.Dir = "/work/t1"
	}
	if err := v.st.Create(tk); err != nil {
		t.Fatal(err)
	}
}

func (v env) run(agent, taskID, command, body string, args ...string) protocol.Answer {
	return v.e.Handle(protocol.Request{Caller: protocol.Caller{Agent: agent, Task: taskID}, Command: command, Args: args, Body: body})
}

func (v env) state(t *testing.T, id string) task.Task {
	t.Helper()
	tk, err := v.st.Task(id)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func (v env) status(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v.home, "status.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (v env) statusShows(t *testing.T, id string, state string) {
	t.Helper()
	for _, line := range strings.Split(v.status(t), "\n") {
		if strings.HasPrefix(line, "| "+id+" |") && strings.Contains(line, "| "+state+" |") {
			return
		}
	}
	t.Fatalf("status.md does not show %s as %s:\n%s", id, state, v.status(t))
}

func mustOK(t *testing.T, a protocol.Answer) {
	t.Helper()
	if !a.OK {
		t.Fatalf("answer failed: %s", a.Error)
	}
}

func mustFail(t *testing.T, a protocol.Answer, mention string) {
	t.Helper()
	if a.OK || !strings.Contains(a.Error, mention) {
		t.Fatalf("want an error that names %q, got %+v", mention, a)
	}
}

func (v env) inbox(t *testing.T, agent string) []store.Message {
	t.Helper()
	m, err := v.st.Unread(agent)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTaskNewFromTheOperatorStartsNewAndMessagesMain(t *testing.T) {
	v := newEnv(t)

	a := v.run("", "", "task new", taskFile)

	mustOK(t, a)
	tk := v.state(t, a.Text)
	if tk.State != task.New || tk.Source != "operator" {
		t.Fatalf("got %+v", tk)
	}
	msgs := v.inbox(t, "main")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, a.Text) {
		t.Fatalf("main inbox: %+v", msgs)
	}
	v.statusShows(t, a.Text, "new")
}

func TestTaskNewFromMainStartsQueued(t *testing.T) {
	v := newEnv(t)

	a := v.run("main", "", "task new", taskFile)

	mustOK(t, a)
	tk := v.state(t, a.Text)
	if tk.State != task.Queued || tk.Source != "main" {
		t.Fatalf("got %+v", tk)
	}
	v.statusShows(t, a.Text, "queued")
}

func TestTaskNewWithoutAGoalFailsAndStoresNothing(t *testing.T) {
	v := newEnv(t)

	a := v.run("main", "", "task new", "# Title\n\n## Description\n\nDo it.\n")

	mustFail(t, a, "Goal")
	tasks, _ := v.st.Tasks()
	if len(tasks) != 0 {
		t.Fatalf("stored %+v", tasks)
	}
}

func TestTaskNewFromAWorkerFailsAndNamesReport(t *testing.T) {
	v := newEnv(t)

	a := v.run("worker-t1", "t1", "task new", taskFile)

	mustFail(t, a, "report")
}

func TestTaskEditReplacesTheGoalOfANewTask(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.New, 0)

	a := v.run("main", "", "task edit", strings.Replace(taskFile, "Done.", "Better.", 1), "t1")

	mustOK(t, a)
	if g := v.state(t, "t1").Goal; g != "Better." {
		t.Fatalf("goal %q", g)
	}
}

func TestTaskEditFailsOnAQueuedTask(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)

	a := v.run("main", "", "task edit", taskFile, "t1")

	mustFail(t, a, "queued")
}

func TestTaskQueueMovesANewTaskToQueued(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.New, 0)

	mustOK(t, v.run("main", "", "task queue", "", "t1"))

	v.statusShows(t, "t1", "queued")
}

func TestTaskCancelStopsSessionsAndRemovesTheWorkspace(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	mustOK(t, v.run("", "", "task cancel", "", "t1"))

	v.statusShows(t, "t1", "cancelled")
	if !slices.Equal(v.l.stopped, []string{"worker-t1", "review-t1-1"}) || !slices.Equal(v.ws.removed, []string{"t1"}) {
		t.Fatalf("stopped %v removed %v", v.l.stopped, v.ws.removed)
	}
}

func TestSendStoresAMessageForTheAgent(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	mustOK(t, v.run("main", "", "send", "", "worker-t1", "use", "Go"))

	msgs := v.inbox(t, "worker-t1")
	if len(msgs) != 1 || msgs[0].Body != "use Go" || msgs[0].From != "main" {
		t.Fatalf("got %+v", msgs)
	}
}

func TestReportDoneMovesTheTaskToReviewAndStartsAReviewer(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	mustOK(t, v.run("worker-t1", "t1", "report done", "", "all", "done"))

	tk := v.state(t, "t1")
	if tk.State != task.Review || tk.Round != 1 {
		t.Fatalf("got %+v", tk)
	}
	if len(v.l.started) != 1 {
		t.Fatalf("started %+v", v.l.started)
	}
	s := v.l.started[0]
	if s.Name != "review-t1-1" || s.Kind != "reviewer" || s.Model != "rev-model" || s.Dir != "/work/t1" || s.Task != "t1" {
		t.Fatalf("spec %+v", s)
	}
	if !strings.Contains(s.Prompt, "Do it.") || !strings.Contains(s.Prompt, "all done") {
		t.Fatalf("prompt %q", s.Prompt)
	}
	v.statusShows(t, "t1", "review")
}

func TestReportDoneFromAnotherWorkersTaskFails(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	a := v.run("worker-t2", "t1", "report done", "", "done")

	mustFail(t, a, "own task")
	if v.state(t, "t1").State != task.Working {
		t.Fatal("state changed")
	}
}

func TestReportBlockedMessagesMainAndKeepsTheState(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	mustOK(t, v.run("worker-t1", "t1", "report blocked", "", "which", "db?"))

	msgs := v.inbox(t, "main")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "t1") || !strings.Contains(msgs[0].Body, "which db?") {
		t.Fatalf("got %+v", msgs)
	}
	if v.state(t, "t1").State != task.Working {
		t.Fatal("state changed")
	}
}

func TestReportProgressRecordsTheText(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half", "way"))

	if p := v.state(t, "t1").Progress; p != "half way" {
		t.Fatalf("progress %q", p)
	}
}

func TestVerdictPassMovesToDoneAndCleansUp(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Looks right."))

	v.statusShows(t, "t1", "done")
	if len(v.inbox(t, "main")) != 1 {
		t.Fatal("main got no message")
	}
	if !slices.Equal(v.l.stopped, []string{"worker-t1", "review-t1-1"}) || !slices.Equal(v.ws.removed, []string{"t1"}) {
		t.Fatalf("stopped %v removed %v", v.l.stopped, v.ws.removed)
	}
	if _, err := os.Stat(filepath.Join(v.home, "tasks", "t1", "verdicts", "1-pass.md")); err != nil {
		t.Fatal(err)
	}
}

func TestVerdictFailBelowTheLimitMovesToFeedbackAndMessagesTheWorker(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	mustOK(t, v.run("review-t1-1", "t1", "verdict fail", "Tests fail."))

	v.statusShows(t, "t1", "feedback")
	msgs := v.inbox(t, "worker-t1")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "Tests fail.") {
		t.Fatalf("got %+v", msgs)
	}
	if !slices.Equal(v.l.stopped, []string{"review-t1-1"}) {
		t.Fatalf("stopped %v", v.l.stopped)
	}
}

func TestVerdictFailInTheLastRoundEscalatesAndNotifiesMain(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 3)

	mustOK(t, v.run("review-t1-3", "t1", "verdict fail", "Still wrong."))

	v.statusShows(t, "t1", "escalated")
	if len(v.l.notified) != 1 || !strings.HasPrefix(v.l.notified[0], "main: ") {
		t.Fatalf("notified %v", v.l.notified)
	}
	if len(v.inbox(t, "main")) != 1 {
		t.Fatal("main got no message")
	}
}

func TestEscalateMovesTheTaskAndNotifiesMain(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	mustOK(t, v.run("main", "", "escalate", "", "t1", "need", "a", "decision"))

	v.statusShows(t, "t1", "escalated")
	if len(v.l.notified) != 1 || !strings.Contains(v.l.notified[0], "need a decision") {
		t.Fatalf("notified %v", v.l.notified)
	}
}

func TestInboxReturnsMessagesOldestFirstAndThenNone(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "first"))
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "second"))

	a := v.run("worker-t1", "t1", "inbox", "")
	b := v.run("worker-t1", "t1", "inbox", "")

	mustOK(t, a)
	if i, j := strings.Index(a.Text, "first"), strings.Index(a.Text, "second"); i < 0 || j < i {
		t.Fatalf("order: %q", a.Text)
	}
	if b.Text != "" {
		t.Fatalf("second inbox: %q", b.Text)
	}
}

func TestStatusAndTaskListShowTheTasks(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)

	s := v.run("", "", "status", "")
	l := v.run("", "", "task list", "")
	sh := v.run("", "", "task show", "", "t1")

	if !strings.Contains(s.Text, "t1") || !strings.Contains(l.Text, "t1") || !strings.Contains(sh.Text, "Do it.") {
		t.Fatalf("%+v %+v %+v", s, l, sh)
	}
}

func TestTaskCancelOfATaskWithoutAWorkspaceStopsNothing(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)

	mustOK(t, v.run("main", "", "task cancel", "", "t1"))

	if len(v.ws.removed) != 0 || len(v.l.stopped) != 0 {
		t.Fatalf("removed %v stopped %v", v.ws.removed, v.l.stopped)
	}
}

func TestACommandOfAnotherKindFailsAndNamesTheCommandsThatKindMayRun(t *testing.T) {
	v := newEnv(t)

	a := v.run("review-t1-1", "t1", "send", "", "main", "hi")

	mustFail(t, a, "verdict pass")
}

func TestACallerWithAnUnknownNameFailsEveryCommand(t *testing.T) {
	for _, name := range []string{"Main", "reviewer-t1", "worker-", "review-t1"} {
		v := newEnv(t)

		a := v.run(name, "t1", "status", "")

		mustFail(t, a, name)
	}
}

func TestReportDoneRestoresTheStateWhenTheReviewerDoesNotStart(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.l.startErr = errors.New("no pane")

	a := v.run("worker-t1", "t1", "report done", "", "done")

	mustFail(t, a, "no pane")
	if tk := v.state(t, "t1"); tk.State != task.Working || tk.Round != 0 {
		t.Fatalf("got %+v", tk)
	}
	v.statusShows(t, "t1", "working")
}

func TestVerdictPassClearsTheWorkspaceOfTheTask(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Looks right."))

	if d := v.state(t, "t1").Dir; d != "" {
		t.Fatalf("dir %q", d)
	}
}

func TestTaskCancelKeepsTheDirWhenTheWorkspaceStaysBehind(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.l.stopErr = errors.New("stuck")

	a := v.run("", "", "task cancel", "", "t1")

	mustFail(t, a, "stuck")
	if tk := v.state(t, "t1"); tk.State != task.Cancelled || tk.Dir == "" {
		t.Fatalf("got %+v", tk)
	}
}

func TestEscalatedTaskWithoutAWorkspaceIsCancelledWithoutSessionCalls(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.New, 0)
	mustOK(t, v.run("main", "", "escalate", "", "t1", "unclear"))

	mustOK(t, v.run("", "", "task cancel", "", "t1"))

	if len(v.ws.removed) != 0 || len(v.l.stopped) != 0 {
		t.Fatalf("removed %v stopped %v", v.ws.removed, v.l.stopped)
	}
}

func TestVerdictFailSavesTheStateEvenWhenTheReviewerWillNotStop(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)
	v.l.stopErr = errors.New("stuck")

	mustFail(t, v.run("review-t1-1", "t1", "verdict fail", "Tests fail."), "stuck")

	v.statusShows(t, "t1", "feedback")
}

func TestVerdictWithAnEmptyBodyFails(t *testing.T) {
	for _, cmd := range []string{"verdict pass", "verdict fail"} {
		v := newEnv(t)
		v.seed(t, task.Review, 1)

		a := v.run("review-t1-1", "t1", cmd, "  \n")

		mustFail(t, a, "empty")
		if v.state(t, "t1").State != task.Review {
			t.Fatal("state changed")
		}
	}
}

func TestSendToAnAgentThatIsNotLiveFails(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)

	for _, to := range []string{"worker-t1", "review-t1-1", "nobody", "worker-t9"} {
		mustFail(t, v.run("main", "", "send", "", to, "hi"), to)
	}
}

func TestSendToTheReviewerOfATaskInReviewWorks(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	mustOK(t, v.run("main", "", "send", "", "review-t1-1", "hi"))
}

func TestTaskShowPrintsTheVerdicts(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)
	mustOK(t, v.run("review-t1-1", "t1", "verdict fail", "Tests fail."))

	a := v.run("", "", "task show", "", "t1")

	if !strings.Contains(a.Text, "Tests fail.") {
		t.Fatalf("got %q", a.Text)
	}
}

func TestTaskEditOfAQueuedTaskNamesTheStateItNeeds(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)

	a := v.run("main", "", "task edit", taskFile, "t1")

	mustFail(t, a, "needs new")
}
