package engine_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/task"
	"github.com/JacobJNilsson/simple-factory/internal/workspace"
)

type fakeLauncher struct {
	sent     []string
	dead     map[string]bool
	sendErr  error
	aliveErr error
	startErr error
	stopErr  error
	started  []engine.SessionSpec
	stopped  []string
	notified []string
	// warning is what TrustWarning returns, and trustAsked lists the folders it was asked about.
	warning    string
	trustAsked []string
	// canDeliver makes Deliver take the text. deliverErr makes it fail. delivered lists the texts that it took.
	canDeliver bool
	deliverErr error
	delivered  []string
}

func (f *fakeLauncher) Deliver(name, text string) (bool, error) {
	if f.deliverErr != nil {
		return false, f.deliverErr
	}
	if !f.canDeliver {
		return false, nil
	}
	f.delivered = append(f.delivered, name+": "+text)
	return true, nil
}

func (f *fakeLauncher) Start(s engine.SessionSpec) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, s)
	return nil
}
func (f *fakeLauncher) Send(name, text string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, name+": "+text)
	return nil
}
func (f *fakeLauncher) Alive(name string) (bool, error) {
	if f.aliveErr != nil {
		return false, f.aliveErr
	}
	return !f.dead[name], nil
}
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

func (f *fakeLauncher) TrustWarning(dir string) string {
	f.trustAsked = append(f.trustAsked, dir)
	return f.warning
}

type fakeWorkspaces struct {
	removed     []string
	restored    []string
	removeErr   error
	dirty       map[string]bool
	beforeCheck func()
	createErr   error
	// localOnly makes HasRemote report a repo with no remote.
	localOnly bool
}

func (f *fakeWorkspaces) HasRemote(task.Task) (bool, error) { return !f.localOnly, nil }

func (f *fakeWorkspaces) Create(t task.Task) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	return "/work/" + t.ID, nil
}
func (f *fakeWorkspaces) Restore(t task.Task) (string, error) {
	f.restored = append(f.restored, t.ID)
	return f.Create(t)
}
func (f *fakeWorkspaces) HoldsWork(t task.Task) (bool, error) {
	if f.beforeCheck != nil {
		f.beforeCheck()
	}
	return f.dirty[t.ID], nil
}
func (f *fakeWorkspaces) Branch(id string) string       { return "sfy/" + id }
func (f *fakeWorkspaces) RepoFolder(repo string) string { return "/folder/" + repo }

func (f *fakeWorkspaces) Remove(t task.Task) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, t.ID)
	return nil
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type env struct {
	home  string
	st    store.Store
	l     *fakeLauncher
	ws    *fakeWorkspaces
	clock *fakeClock
	e     *engine.Engine
}

func newEnv(t *testing.T) env {
	t.Helper()
	home := t.TempDir()
	st := store.Files(home)
	l, ws := &fakeLauncher{}, &fakeWorkspaces{}
	cfg := config.Config{ReviewRounds: 3, MaxWorkers: 2, ResendMinutes: 5, Models: config.Models{Worker: "work-model", Reviewer: "rev-model"}}
	clock := &fakeClock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	return env{home, st, l, ws, clock, engine.New(st, l, ws, cfg, clock)}
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

func TestTaskNewWarnsInReplyAndNoteWhenRepoIsUntrusted(t *testing.T) {
	v := newEnv(t)
	v.l.warning = "untrusted"

	a := v.run("", "", "task new", taskFile+"\n## Repo\n\nhttps://example.com/x.git\n")

	mustOK(t, a)
	id, warning, _ := strings.Cut(a.Text, "\n")
	if id != "t1" || !strings.Contains(warning, "untrusted") {
		t.Errorf("reply %q", a.Text)
	}
	if want := []string{"/folder/https://example.com/x.git"}; !reflect.DeepEqual(v.l.trustAsked, want) {
		t.Errorf("asked %v, want %v", v.l.trustAsked, want)
	}
	msgs := v.inbox(t, "main")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "untrusted") {
		t.Fatalf("main inbox: %+v", msgs)
	}
}

func TestTaskNewIsQuietWhenRepoIsTrustedOrAbsent(t *testing.T) {
	v := newEnv(t)

	withRepo := v.run("", "", "task new", taskFile+"\n## Repo\n\n/some/repo\n")
	without := v.run("", "", "task new", taskFile)

	mustOK(t, withRepo)
	mustOK(t, without)
	if strings.ContainsAny(withRepo.Text, " \n") || strings.ContainsAny(without.Text, " \n") {
		t.Errorf("replies %q and %q hold more than an id", withRepo.Text, without.Text)
	}
	if len(v.l.trustAsked) != 1 {
		t.Errorf("a task without a repo must not ask: %v", v.l.trustAsked)
	}
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

const editedFile = "# Title\n\n## Description\n\nDo it.\n\n## Goal\n\nBetter.\n"

func editGoal(v env, t *testing.T) {
	t.Helper()
	mustOK(t, v.run("main", "", "task edit", editedFile, "t1"))
}

func TestTaskEditReplacesTheGoalOfAQueuedTask(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)

	editGoal(v, t)

	if g := v.state(t, "t1").Goal; g != "Better." {
		t.Fatalf("goal %q", g)
	}
}

func TestTaskEditOfAWorkingTaskMessagesTheWorker(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 2)

	editGoal(v, t)

	msgs := v.inbox(t, "worker-t1")
	if len(msgs) != 1 || msgs[0].Body != "The task file for t1 changed. Run: sfy task show t1" {
		t.Fatalf("worker inbox: %+v", msgs)
	}
}

func TestTaskEditKeepsStateAndRound(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 2)

	editGoal(v, t)

	got := v.state(t, "t1")
	if got.State != task.Working || got.Round != 2 {
		t.Fatalf("state %s, round %d", got.State, got.Round)
	}
}

func TestTaskEditOfAQueuedTaskMessagesNobody(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)

	editGoal(v, t)

	if m := v.inbox(t, "worker-t1"); len(m) != 0 {
		t.Fatalf("worker inbox: %+v", m)
	}
}

func TestTaskEditOfAnEscalatedTaskWithAWorkspaceMessagesTheWorker(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Escalated, 0)

	editGoal(v, t)

	if m := v.inbox(t, "worker-t1"); len(m) != 1 {
		t.Fatalf("worker inbox: %+v", m)
	}
}

func TestTaskEditOfAnEscalatedTaskWithoutAWorkspaceMessagesNobody(t *testing.T) {
	v := newEnv(t)
	tk := task.Task{ID: "t1", Title: "Title", Description: "Do it.", Goal: "Done.", State: task.Escalated, Source: "main"}
	if err := v.st.Create(tk); err != nil {
		t.Fatal(err)
	}

	editGoal(v, t)

	if m := v.inbox(t, "worker-t1"); len(m) != 0 {
		t.Fatalf("worker inbox: %+v", m)
	}
}

// waitingWorker leaves the worker of a working task idle, so the next message types a note into its pane.
func waitingWorker(v env, t *testing.T) {
	t.Helper()
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report progress", "halfway"))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))
}

// sentTo lists what the fake launcher typed into one pane.
func sentTo(v env, name string) []string {
	var out []string
	for _, s := range v.l.sent {
		if strings.HasPrefix(s, name+": ") {
			out = append(out, s)
		}
	}
	return out
}

func TestTaskEditWithADeadWorkerSessionSucceeds(t *testing.T) {
	v := newEnv(t)
	waitingWorker(v, t)
	v.l.dead = map[string]bool{"worker-t1": true}
	v.l.sendErr = errors.New("no such pane")

	editGoal(v, t)

	if m := v.inbox(t, "worker-t1"); len(m) != 1 {
		t.Fatalf("worker inbox: %+v", m)
	}
}

func TestTaskEditFailsWhenTheNoteFailsForALiveWorker(t *testing.T) {
	v := newEnv(t)
	waitingWorker(v, t)
	v.l.sendErr = errors.New("pane refused")

	a := v.run("main", "", "task edit", editedFile, "t1")

	mustFail(t, a, "pane refused")
}

func TestTaskEditFailsOnADoneTask(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Done, 0)

	a := v.run("main", "", "task edit", taskFile, "t1")

	if a.OK {
		t.Fatalf("want an error, got %+v", a)
	}
}

func TestTaskEditFailsOnACancelledTask(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Cancelled, 0)

	a := v.run("main", "", "task edit", taskFile, "t1")

	if a.OK {
		t.Fatalf("want an error, got %+v", a)
	}
}

func TestTaskEditRefusesANewRepoOnceTheWorkspaceExists(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	a := v.run("main", "", "task edit", taskFile+"\n## Repo\n\nother\n", "t1")

	mustFail(t, a, "repo is fixed")
}

func TestTaskEditRefusesNewProtectedPathsOnceTheWorkspaceExists(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	a := v.run("main", "", "task edit", taskFile+"\n## Protected\n\n- a.go\n", "t1")

	mustFail(t, a, "protected paths are fixed")
}

func TestTaskEditAllowsANewRepoBeforeTheWorkspaceExists(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)

	mustOK(t, v.run("main", "", "task edit", taskFile+"\n## Repo\n\nother\n", "t1"))

	if r := v.state(t, "t1").Repo; r != "other" {
		t.Fatalf("repo %q", r)
	}
}

func TestTaskEditOfATaskInReviewKeepsTheRunningReviewersPrompt(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report done", "", "all", "done"))

	editGoal(v, t)

	if p := v.l.started[0].Prompt; !strings.Contains(p, "Done.") || strings.Contains(p, "Better.") {
		t.Fatalf("prompt %q", p)
	}
}

func TestTheRoundAfterAnEditReviewsTheNewFile(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report done", "", "all", "done"))
	editGoal(v, t)
	mustOK(t, v.run("review-t1-1", "t1", "verdict fail", "Tests fail."))

	mustOK(t, v.run("worker-t1", "t1", "report done", "", "again"))

	if p := v.l.started[1].Prompt; !strings.Contains(p, "Better.") {
		t.Fatalf("prompt %q", p)
	}
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

// pass records a passing verdict and has main complete the task.
func (v env) pass(t *testing.T) {
	t.Helper()
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))
	mustOK(t, v.run("main", "", "task complete", "", "t1"))
}

func TestVerdictPassMovesToApprovedAndTellsMain(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Looks right."))

	v.statusShows(t, "t1", "approved")
	msgs := v.inbox(t, "main")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "task complete t1") || !strings.Contains(msgs[0].Body, "no repo") {
		t.Fatalf("main got %+v", msgs)
	}
	if !slices.Equal(v.l.stopped, []string{"worker-t1", "review-t1-1"}) || len(v.ws.removed) != 0 {
		t.Fatalf("stopped %v removed %v", v.l.stopped, v.ws.removed)
	}
	if d := v.state(t, "t1").Dir; d != "/work/t1" {
		t.Fatalf("dir %q", d)
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

func TestCompleteMovesAnApprovedTaskToDoneAndCleansUp(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)

	mustOK(t, v.run("main", "", "task complete", "", "t1"))

	v.statusShows(t, "t1", "done")
	if !slices.Equal(v.l.stopped, []string{"worker-t1", "review-t1-1"}) || !slices.Equal(v.ws.removed, []string{"t1"}) {
		t.Fatalf("stopped %v removed %v", v.l.stopped, v.ws.removed)
	}
}

func TestCompleteOfAWorkingTaskFails(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	mustFail(t, v.run("main", "", "task complete", "", "t1"), "needs approved")
	if v.state(t, "t1").State != task.Working {
		t.Fatal("the task changed state")
	}
}

func TestAWorkerMayNotRunComplete(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)

	mustFail(t, v.run("worker-t1", "t1", "task complete", "", "t1"), "task complete")
	if v.state(t, "t1").State != task.Approved {
		t.Fatal("the task changed state")
	}
}

func TestCancelOfAnApprovedTaskWorks(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)

	mustOK(t, v.run("main", "", "task cancel", "", "t1"))

	v.statusShows(t, "t1", "cancelled")
	if !slices.Equal(v.ws.removed, []string{"t1"}) {
		t.Fatalf("removed %v", v.ws.removed)
	}
}

func TestTaskEditFailsOnAnApprovedTask(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)

	mustFail(t, v.run("main", "", "task edit", taskFile, "t1"), "approved")
}

func TestCompleteClearsTheWorkspaceOfTheTask(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	v.pass(t)

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

const (
	reminder = "Your turn ended without a report. If you need an answer, run sfy report blocked <question>."
	note     = "New message. Run: sfy inbox"
)

func TestTurnEndedFromAWorkerThatDidNotReportTypesTheReminder(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	if !slices.Equal(v.l.sent, []string{"worker-t1: " + reminder}) {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestTurnEndedFromAWorkerThatReportedTypesNoReminder(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))

	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	if len(v.l.sent) != 0 {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestTurnEndedTypesTheNoteForUnreadMessages(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "hi"))

	mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	if !slices.Equal(v.l.sent, []string{"worker-t1: " + note}) {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestAMessageToAnAgentThatWaitsTypesTheNoteOnce(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("main", "", "hook turn-ended", ""))

	mustOK(t, v.run("", "", "task new", taskFile))
	mustOK(t, v.run("", "", "task new", taskFile))

	if !slices.Equal(v.l.sent, []string{"main: " + note}) {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestAMessageToABusyAgentTypesNothing(t *testing.T) {
	v := newEnv(t)

	mustOK(t, v.run("", "", "task new", taskFile))

	if len(v.l.sent) != 0 {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestTheReminderStopsAfterTwoAndMainGetsOneMessage(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	for range 4 {
		mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))
	}

	if !slices.Equal(v.l.sent, []string{"worker-t1: " + reminder, "worker-t1: " + reminder}) {
		t.Fatalf("sent %v", v.l.sent)
	}
	msgs := v.inbox(t, "main")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "worker-t1 ended 2 turns without a report.") {
		t.Fatalf("got %+v", msgs)
	}
}

func TestAReportResetsTheCountOfReminders(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))
	mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	if len(v.l.sent) != 3 || len(v.inbox(t, "main")) != 0 {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestTurnEndedFromAReviewerAfterItsVerdictTypesNothing(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)
	mustOK(t, v.run("review-t1-1", "t1", "verdict fail", "Tests fail."))

	mustOK(t, v.run("review-t1-1", "t1", "hook turn-ended", ""))
	mustOK(t, v.run("review-t1-1", "t1", "hook turn-ended", ""))

	if len(v.l.sent) != 0 {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestTheErrorForAForbiddenCommandListsNoHookCommand(t *testing.T) {
	v := newEnv(t)

	a := v.run("worker-t1", "t1", "task new", taskFile)

	if strings.Contains(a.Error, "hook") {
		t.Fatalf("got %q", a.Error)
	}
}

func TestPermissionFromAWorkerWritesNothingToMainsInboxAndLogsTheCall(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	var log bytes.Buffer
	v.e.SetLog(&log)

	mustOK(t, v.run("worker-t1", "t1", "hook permission", `{"tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`))

	if msgs := v.inbox(t, "main"); len(msgs) != 0 {
		t.Fatalf("main got %+v", msgs)
	}
	if len(v.l.sent) != 0 {
		t.Fatalf("typed notes %v", v.l.sent)
	}
	for _, want := range []string{"worker-t1", "Bash", "rm -rf build"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q: %q", want, log.String())
		}
	}
}

func TestPermissionHookDeniesAWorkerAndAReviewerWithAReason(t *testing.T) {
	for _, c := range []struct {
		agent string
		state task.State
	}{{"worker-t1", task.Working}, {"review-t1-1", task.Review}} {
		v := newEnv(t)
		v.seed(t, c.state, 1)

		a := v.run(c.agent, "t1", "hook permission", `{"tool_name":"Bash","tool_input":{"command":"ls"}}`)

		mustOK(t, a)
		var got struct {
			Out struct {
				Event    string `json:"hookEventName"`
				Decision struct {
					Behavior string `json:"behavior"`
					Message  string `json:"message"`
				} `json:"decision"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(a.Text), &got); err != nil {
			t.Fatalf("%s reply %q: %v", c.agent, a.Text, err)
		}
		d := got.Out.Decision
		if got.Out.Event != "PermissionRequest" || d.Behavior != "deny" || !strings.Contains(d.Message, "sfy report blocked") {
			t.Errorf("%s reply %q", c.agent, a.Text)
		}
	}
}

func TestReviewerReportBlockedTellsMain(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)

	mustOK(t, v.run("review-t1-1", "t1", "report blocked", "", "needs", "network"))

	msgs := v.inbox(t, "main")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "needs network") {
		t.Fatalf("got %+v", msgs)
	}
	if got := v.state(t, "t1").State; got != task.Review {
		t.Fatalf("task state after the report is %s, want review", got)
	}
}

func TestPermissionDenialLeavesTheResendOfAnUnreadNoteRunning(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "hello"))
	mustOK(t, v.run("worker-t1", "t1", "hook permission", `{"tool_name":"Bash","tool_input":{"command":"ls"}}`))
	before := len(v.l.sent)

	v.clock.now = v.clock.now.Add(5 * time.Minute)
	v.tick(t)

	if len(v.l.sent) != before+1 || !strings.HasPrefix(v.l.sent[before], "worker-t1: ") {
		t.Fatalf("sent %v, before %d", v.l.sent, before)
	}
}

func TestPermissionFromAStaleWorkerIsDeniedAndNotLogged(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Cancelled, 0)
	var log bytes.Buffer
	v.e.SetLog(&log)

	a := v.run("worker-t1", "t1", "hook permission", `{"tool_name":"Bash","tool_input":{"command":"ls"}}`)

	mustOK(t, a)
	if !strings.Contains(a.Text, `"behavior":"deny"`) || log.Len() != 0 {
		t.Fatalf("reply %q, log %q", a.Text, log.String())
	}
}

func TestPermissionFromMainNotifiesTheOperator(t *testing.T) {
	v := newEnv(t)

	a := v.run("main", "", "hook permission", "Run make?")

	mustOK(t, a)
	if a.Text != "" {
		t.Fatalf("main got reply %q", a.Text)
	}

	if len(v.l.notified) != 1 || !strings.Contains(v.l.notified[0], "Run make?") {
		t.Fatalf("notified %v", v.l.notified)
	}
}

func (v env) queue(t *testing.T, n int) {
	t.Helper()
	for range n {
		mustOK(t, v.run("main", "", "task new", taskFile))
	}
}

func (v env) tick(t *testing.T) {
	t.Helper()
	if err := v.e.Tick(); err != nil {
		t.Fatal(err)
	}
}

func TestTickStartsWorkersUpToMaxWorkersOldestFirst(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 3)

	v.tick(t)

	var names []string
	for _, s := range v.l.started {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"worker-t1", "worker-t2"}) {
		t.Fatalf("started %v", names)
	}
	v.statusShows(t, "t3", "queued")
}

func TestTickMovesAStartedTaskToWorkingWithTheWorkspaceDir(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 1)

	v.tick(t)

	tk := v.state(t, "t1")
	if tk.State != task.Working || tk.Dir != "/work/t1" {
		t.Fatalf("got %+v", tk)
	}
	s := v.l.started[0]
	if s.Kind != "worker" || s.Dir != "/work/t1" || s.Model != "work-model" || s.Task != "t1" || !strings.Contains(s.Prompt, "Do it.") {
		t.Fatalf("spec %+v", s)
	}
}

func TestTickPutsATaskBackInQueuedWhenTheWorkerDoesNotStart(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 1)
	noPane := errors.New("no pane")
	v.l.startErr = noPane

	err := v.e.Tick()

	if !errors.Is(err, noPane) {
		t.Fatalf("got %v", err)
	}
	if tk := v.state(t, "t1"); tk.State != task.Queued || tk.Dir != "" {
		t.Fatalf("got %+v", tk)
	}
	if !slices.Equal(v.ws.removed, []string{"t1"}) {
		t.Fatalf("removed %v", v.ws.removed)
	}
}

func TestTickPutsATaskBackInQueuedWhenTheWorkspaceFails(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 1)
	v.ws.createErr = errors.New("disk full")

	err := v.e.Tick()

	if err == nil || v.state(t, "t1").State != task.Queued {
		t.Fatalf("got %v, %+v", err, v.state(t, "t1"))
	}
}

func TestTickResendsTheNoteAfterResendMinutesAndNotBefore(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("main", "", "hook turn-ended", ""))
	mustOK(t, v.run("", "", "task new", taskFile))

	v.clock.now = v.clock.now.Add(4 * time.Minute)
	v.tick(t)
	before := len(v.l.sent)
	v.clock.now = v.clock.now.Add(time.Minute)
	v.tick(t)

	if before != 1 || len(v.l.sent) != 2 || v.l.sent[1] != "main: "+note {
		t.Fatalf("before %d, sent %v", before, v.l.sent)
	}
}

func TestTickDoesNotResendToAnAgentThatReadItsMessages(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("main", "", "hook turn-ended", ""))
	mustOK(t, v.run("", "", "task new", taskFile))
	mustOK(t, v.run("main", "", "inbox", ""))

	v.clock.now = v.clock.now.Add(time.Hour)
	v.tick(t)

	if len(v.l.sent) != 1 {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestTickRetriesRemoveForACancelledTaskAndClearsDir(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.ws.removeErr = errors.New("busy")
	mustFail(t, v.run("", "", "task cancel", "", "t1"), "busy")
	v.ws.removeErr = nil

	v.tick(t)

	if !slices.Equal(v.ws.removed, []string{"t1"}) || v.state(t, "t1").Dir != "" {
		t.Fatalf("removed %v, dir %q", v.ws.removed, v.state(t, "t1").Dir)
	}
}

func TestTickTellsMainOnceWhenAWorkerIsGone(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.l.dead = map[string]bool{"worker-t1": true}

	v.tick(t)
	v.tick(t)

	msgs := v.inbox(t, "main")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "t1") {
		t.Fatalf("got %+v", msgs)
	}
}

func TestTickTellsMainAgainWhenAWorkerDiesAfterRunningAgain(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	v.l.dead = map[string]bool{"worker-t1": true}
	v.tick(t)
	v.l.dead = nil
	v.tick(t)
	v.l.dead = map[string]bool{"worker-t1": true}

	v.tick(t)

	if n := len(v.inbox(t, "main")); n != 2 {
		t.Fatalf("got %d messages", n)
	}
}

func TestAFreshEngineResendsAnUnreadNoteAfterResendMinutes(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("", "", "task new", taskFile))
	cfg := config.Config{ResendMinutes: 5, MaxWorkers: 2}
	fresh := engine.New(v.st, v.l, v.ws, cfg, v.clock)
	v.clock.now = v.clock.now.Add(5 * time.Minute)

	if err := fresh.Tick(); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(v.l.sent, []string{"main: " + note}) {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestTickDoesNotResendToMainWhenItsSessionIsGone(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("", "", "task new", taskFile))
	v.l.dead = map[string]bool{"main": true}
	v.clock.now = v.clock.now.Add(time.Hour)

	v.tick(t)

	if len(v.l.sent) != 0 {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestTickKeepsTheDirWhenRemoveFailsAfterAFailedStart(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 1)
	v.l.startErr = errors.New("no pane")
	v.ws.removeErr = errors.New("busy")

	if err := v.e.Tick(); err == nil {
		t.Fatal("want an error")
	}

	if tk := v.state(t, "t1"); tk.State != task.Queued || tk.Dir != "/work/t1" {
		t.Fatalf("got %+v", tk)
	}
}

func TestThreeFailedStartsEscalateTheTask(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 1)
	v.l.startErr = errors.New("no pane")

	for range 3 {
		_ = v.e.Tick()
	}

	v.statusShows(t, "t1", "escalated")
	msgs := v.inbox(t, "main")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "t1") || !strings.Contains(msgs[0].Body, "no pane") {
		t.Fatalf("got %+v", msgs)
	}
}

func TestASuccessfulStartResetsTheCountOfFailedStarts(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 1)
	v.l.startErr = errors.New("no pane")
	_ = v.e.Tick()
	_ = v.e.Tick()
	v.l.startErr = nil
	v.tick(t)
	mustOK(t, v.run("", "", "task cancel", "", "t1"))
	v.queue(t, 1)
	v.l.startErr = errors.New("no pane")

	_ = v.e.Tick()
	_ = v.e.Tick()

	v.statusShows(t, "t2", "queued")
}

const protectedTaskFile = taskFile + "\n## Protected\n\n- SPEC.md\n- internal/store\n"

func TestStartedWorkerKnowsWhetherTheTaskHasARepo(t *testing.T) {
	for name, tc := range map[string]struct {
		file string
		want bool
	}{"no repo": {taskFile, false}, "repo": {taskFile + "\n## Repo\n\nhttps://example.com/r.git\n", true}} {
		t.Run(name, func(t *testing.T) {
			v := newEnv(t)
			mustOK(t, v.run("main", "", "task new", tc.file))

			v.tick(t)

			if len(v.l.started) != 1 || v.l.started[0].HasRepo != tc.want {
				t.Fatalf("started %+v, want HasRepo %v", v.l.started, tc.want)
			}
		})
	}
}

func TestStartedSessionsCarryTheTasksProtectedPaths(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("main", "", "task new", protectedTaskFile))
	want := []string{"SPEC.md", "internal/store"}

	v.tick(t)
	mustOK(t, v.run("worker-t1", "t1", "report done", "", "finished"))

	if len(v.l.started) != 2 {
		t.Fatalf("started %+v", v.l.started)
	}
	for _, s := range v.l.started {
		if !reflect.DeepEqual(s.Protected, want) {
			t.Fatalf("%s protected %v, want %v", s.Name, s.Protected, want)
		}
	}
}

func TestResumeReturnsAWorkingTaskToWorking(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "escalate", "", "t1", "stuck"))

	mustOK(t, v.run("", "", "task resume", "", "t1"))

	if tk := v.state(t, "t1"); tk.State != task.Working || tk.Before != "" {
		t.Fatalf("got %+v", tk)
	}
}

func TestResumeTellsTheWorkerToGoOn(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "escalate", "", "t1", "stuck"))

	mustOK(t, v.run("", "", "task resume", "", "t1"))

	msgs := v.inbox(t, "worker-t1")
	if len(msgs) != 1 || msgs[0].Body != "Task t1 is resumed. Go on, and report as usual." {
		t.Fatalf("worker got %+v", msgs)
	}
}

func TestResumeKeepsTheRoundOfAFeedbackTask(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Feedback, 2)
	mustOK(t, v.run("main", "", "escalate", "", "t1", "stuck"))

	mustOK(t, v.run("main", "", "task resume", "", "t1"))

	if tk := v.state(t, "t1"); tk.State != task.Feedback || tk.Round != 2 {
		t.Fatalf("got %+v", tk)
	}
}

// failLastRound escalates task t1 through a failed verdict in the last review round.
func (v env) failLastRound(t *testing.T) {
	t.Helper()
	v.seed(t, task.Review, 3)
	mustOK(t, v.run("review-t1-3", "t1", "verdict fail", "Still wrong."))
}

func TestTheLastFailedRoundSendsTheWorkerTheVerdict(t *testing.T) {
	v := newEnv(t)
	v.failLastRound(t)

	msgs := v.inbox(t, "worker-t1")

	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "Review round 3 of task t1 failed") || !strings.Contains(msgs[0].Body, "Still wrong.") {
		t.Fatalf("worker got %+v", msgs)
	}
}

func TestResumeAfterTheLastFailedRoundGoesToFeedbackWithTheSameRound(t *testing.T) {
	v := newEnv(t)
	v.failLastRound(t)

	mustOK(t, v.run("main", "", "task resume", "", "t1"))

	if tk := v.state(t, "t1"); tk.State != task.Feedback || tk.Round != 3 {
		t.Fatalf("got %+v", tk)
	}
}

func TestReportDoneAfterThatResumeStartsTheNextRound(t *testing.T) {
	v := newEnv(t)
	v.failLastRound(t)
	mustOK(t, v.run("main", "", "task resume", "", "t1"))

	mustOK(t, v.run("worker-t1", "t1", "report done", "", "fixed"))

	if tk := v.state(t, "t1"); tk.State != task.Review || tk.Round != 4 {
		t.Fatalf("got %+v", tk)
	}
}

func TestResumeOfAQueuedEscalationGoesBackToQueuedWithoutAWorkerMessage(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)
	mustOK(t, v.run("main", "", "escalate", "", "t1", "unclear"))

	mustOK(t, v.run("main", "", "task resume", "", "t1"))

	v.statusShows(t, "t1", "queued")
	if len(v.inbox(t, "worker-t1")) != 0 {
		t.Fatal("a worker that does not run got a message")
	}
}

func TestResumeFailsForATaskThatIsNotEscalated(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	ans := v.run("main", "", "task resume", "", "t1")

	if ans.OK || !strings.Contains(ans.Error, "needs escalated") {
		t.Fatalf("got %+v", ans)
	}
	if v.state(t, "t1").State != task.Working {
		t.Fatal("the task changed state")
	}
}

func TestPassKeepsAWorkspaceThatHoldsWork(t *testing.T) {
	v := newEnv(t)
	v.ws.dirty = map[string]bool{"t1": true}
	v.seed(t, task.Review, 1)

	v.pass(t)

	if len(v.ws.removed) != 0 {
		t.Fatalf("removed %v", v.ws.removed)
	}
}

func TestPassTellsMainThatTheWorkspaceHoldsWork(t *testing.T) {
	v := newEnv(t)
	v.ws.dirty = map[string]bool{"t1": true}
	v.seed(t, task.Review, 1)

	v.pass(t)

	want := "Task t1 is done, but its workspace /work/t1 still holds work, so sfy kept it."
	var found bool
	for _, m := range v.inbox(t, "main") {
		found = found || m.Body == want
	}
	if !found {
		t.Fatalf("main got no %q", want)
	}
}

func TestCancelKeepsAWorkspaceThatHoldsWork(t *testing.T) {
	v := newEnv(t)
	v.ws.dirty = map[string]bool{"t1": true}
	v.seed(t, task.Working, 0)

	mustOK(t, v.run("", "", "task cancel", "", "t1"))

	if len(v.ws.removed) != 0 {
		t.Fatalf("removed %v", v.ws.removed)
	}
}

func TestCancelTellsMainThatTheWorkspaceHoldsWork(t *testing.T) {
	v := newEnv(t)
	v.ws.dirty = map[string]bool{"t1": true}
	v.seed(t, task.Working, 0)

	mustOK(t, v.run("", "", "task cancel", "", "t1"))

	msgs := v.inbox(t, "main")
	if len(msgs) != 1 || msgs[0].Body != "Task t1 is cancelled, but its workspace /work/t1 still holds work, so sfy kept it." {
		t.Fatalf("main got %+v", msgs)
	}
}

func TestPassKeepsAPlainFolderThatHoldsAFile(t *testing.T) {
	v := newEnv(t)
	ws := workspace.New(t.TempDir())
	v.e = engine.New(v.st, v.l, ws, config.Config{ReviewRounds: 3, MaxWorkers: 2}, v.clock)
	tk := task.Task{ID: "t1", Title: "Title", Description: "Do it.", Goal: "Done.", State: task.Review, Round: 1, Source: "main"}
	dir, err := ws.Create(tk)
	if err != nil {
		t.Fatal(err)
	}
	tk.Dir = dir
	if err := v.st.Create(tk); err != nil {
		t.Fatal(err)
	}
	answer := filepath.Join(dir, "answer.md")
	if err := os.WriteFile(answer, []byte("42"), 0o600); err != nil {
		t.Fatal(err)
	}

	v.pass(t)

	if _, err := os.Stat(answer); err != nil {
		t.Fatalf("the deliverable is gone: %v", err)
	}
}

func TestTickAfterAFailedSaveTellsMainOnceThatTheWorkspaceWasKept(t *testing.T) {
	v := newEnv(t)
	v.ws.dirty = map[string]bool{"t1": true}
	v.seed(t, task.Review, 1)
	taskDir := filepath.Join(v.home, "tasks", "t1")
	// The save in cleanup fails because its folder cannot take new files.
	v.ws.beforeCheck = func() { _ = os.Chmod(taskDir, 0o500) }

	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))
	if ans := v.run("main", "", "task complete", "", "t1"); ans.OK {
		t.Fatal("the failed save went unreported")
	}
	if err := os.Chmod(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	v.ws.beforeCheck = nil
	if err := v.e.Tick(); err != nil {
		t.Fatal(err)
	}

	var kept int
	for _, m := range v.inbox(t, "main") {
		if strings.Contains(m.Body, "still holds work") {
			kept++
		}
	}
	if kept != 1 {
		t.Fatalf("main heard %d times", kept)
	}
}

func TestTickAfterAKeptWorktreeSendsMainNothing(t *testing.T) {
	v := newEnv(t)
	v.ws.dirty = map[string]bool{"t1": true}
	v.seed(t, task.Review, 1)
	v.pass(t)
	before := len(v.inbox(t, "main"))

	if err := v.e.Tick(); err != nil {
		t.Fatal(err)
	}

	if after := len(v.inbox(t, "main")); after != before {
		t.Fatalf("main got %d messages before and %d after the tick", before, after)
	}
}

func TestResumeIntoReviewTellsTheReviewerToo(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Review, 1)
	mustOK(t, v.run("main", "", "escalate", "", "t1", "stuck"))

	mustOK(t, v.run("main", "", "task resume", "", "t1"))

	want := "Task t1 is resumed. Go on, and report as usual."
	for _, name := range []string{"worker-t1", "review-t1-1"} {
		if msgs := v.inbox(t, name); len(msgs) != 1 || msgs[0].Body != want {
			t.Fatalf("%s got %+v", name, msgs)
		}
	}
}

// escalateWithAWaitingWorker escalates task t1 while its worker waits for input, so a note to it goes to the launcher.
// The report and the turn end are the shortest public way to reach that state.
func (v env) escalateWithAWaitingWorker(t *testing.T) {
	t.Helper()
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))
	mustOK(t, v.run("main", "", "escalate", "", "t1", "stuck"))
	v.l.sendErr = errors.New("no such pane")
}

func TestResumeSucceedsWhenTheWorkerSessionIsGone(t *testing.T) {
	v := newEnv(t)
	v.escalateWithAWaitingWorker(t)
	v.l.dead = map[string]bool{"worker-t1": true}

	mustOK(t, v.run("main", "", "task resume", "", "t1"))
}

func TestResumeFailsWhenTheNoteFailsAndTheSessionRuns(t *testing.T) {
	v := newEnv(t)
	v.escalateWithAWaitingWorker(t)

	ans := v.run("main", "", "task resume", "", "t1")

	if ans.OK || !strings.Contains(ans.Error, "no such pane") {
		t.Fatalf("got %+v", ans)
	}
}

func TestResumeFailsWhenTheMessageCannotBeStoredAndTheSessionIsGone(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "escalate", "", "t1", "stuck"))
	v.l.dead = map[string]bool{"worker-t1": true}
	// A file where the inbox folder belongs makes every write to that inbox fail.
	if err := os.MkdirAll(filepath.Join(v.home, "inbox"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.home, "inbox", "worker-t1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if ans := v.run("main", "", "task resume", "", "t1"); ans.OK {
		t.Fatal("resume reported success although the message was lost")
	}
}

func TestCancelOfAnEscalatedTaskClearsTheStateBefore(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "escalate", "", "t1", "stuck"))

	mustOK(t, v.run("main", "", "task cancel", "", "t1"))

	if tk := v.state(t, "t1"); tk.State != task.Cancelled || tk.Before != "" {
		t.Fatalf("got %+v", tk)
	}
}

func TestMainEscalatingATaskGetsNoMessageAboutIt(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	mustOK(t, v.run("main", "", "escalate", "", "t1", "need", "a", "decision"))

	if msgs := v.inbox(t, "main"); len(msgs) != 0 {
		t.Fatalf("got %+v", msgs)
	}
	if len(v.l.notified) != 1 {
		t.Fatalf("notified %v", v.l.notified)
	}
}

func TestARestartedEngineTypesTheNoteForAnIdleMainAfterResendMinutes(t *testing.T) {
	v := newEnv(t)
	fresh := engine.New(v.st, v.l, v.ws, config.Config{ResendMinutes: 5, MaxWorkers: 2}, v.clock)
	answer := fresh.Handle(protocol.Request{Caller: protocol.Caller{}, Command: "task new", Body: taskFile})
	mustOK(t, answer)
	v.clock.now = v.clock.now.Add(5 * time.Minute)

	if err := fresh.Tick(); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(v.l.sent, []string{"main: " + note}) {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestPermissionLogLineNamesTheToolAndItsMainInput(t *testing.T) {
	const ids = `"session_id":"s1","transcript_path":"/x/y.jsonl"`
	cases := map[string]struct{ body, want string }{
		"bash":                 {`{` + ids + `,"tool_name":"Bash","tool_input":{"command":"ls -la","description":"d"}}`, "Tool: Bash. Command: ls -la"},
		"edit":                 {`{` + ids + `,"tool_name":"Edit","tool_input":{"file_path":"/w/a.go","old_string":"x"}}`, "Tool: Edit. File: /w/a.go"},
		"read":                 {`{` + ids + `,"tool_name":"Read","tool_input":{"file_path":"/w/c.go"}}`, "Tool: Read. File: /w/c.go"},
		"unknown tool":         {`{` + ids + `,"tool_name":"Grep","tool_input":{ "pattern": "x" }}`, `Tool: Grep. Input: {"pattern":"x"}`},
		"missing tool input":   {`{"tool_name":"Task"}`, "Tool: Task"},
		"non-string command":   {`{"tool_name":"Bash","tool_input":{"command":5}}`, `Tool: Bash. Input: {"command":5}`},
		"body that is no JSON": {"Run rm -rf build?", "Run rm -rf build?"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			v := newEnv(t)
			v.seed(t, task.Working, 0)
			var log bytes.Buffer
			v.e.SetLog(&log)

			mustOK(t, v.run("worker-t1", "t1", "hook permission", c.body))

			if !strings.Contains(log.String(), "worker-t1") || !strings.Contains(log.String(), c.want) {
				t.Fatalf("log %q lacks %q", log.String(), c.want)
			}
		})
	}
}

func TestEngineLogsEachNoteItTypesAndWhyItWasTyped(t *testing.T) {
	v := newEnv(t)
	var log bytes.Buffer
	v.e.SetLog(&log)
	mustOK(t, v.run("main", "", "hook turn-ended", ""))
	mustOK(t, v.run("", "", "task new", taskFile))
	v.clock.now = v.clock.now.Add(5 * time.Minute)
	v.tick(t)

	lines := strings.Split(strings.TrimSpace(log.String()), "\n")
	if len(lines) != 2 || lines[0] == lines[1] || strings.TrimPrefix(lines[0], strings.Fields(lines[0])[0]) == strings.TrimPrefix(lines[1], strings.Fields(lines[1])[0]) {
		t.Fatalf("log %q", log.String())
	}
}

func TestEngineLogsOnceWhenATickSkipsAnUnreadMainThatIsNotAlive(t *testing.T) {
	v := newEnv(t)
	var log bytes.Buffer
	v.e.SetLog(&log)
	mustOK(t, v.run("", "", "task new", taskFile))
	v.l.dead = map[string]bool{"main": true}
	v.clock.now = v.clock.now.Add(5 * time.Minute)

	v.tick(t)
	v.tick(t)

	if lines := strings.Count(log.String(), "\n"); lines != 1 || len(v.l.sent) != 0 {
		t.Fatalf("log %q, sent %v", log.String(), v.l.sent)
	}
}

func TestEngineLogsAgainForASecondPeriodInWhichMainIsNotAlive(t *testing.T) {
	v := newEnv(t)
	var log bytes.Buffer
	v.e.SetLog(&log)
	mustOK(t, v.run("", "", "task new", taskFile))
	v.clock.now = v.clock.now.Add(time.Minute)
	v.l.dead = map[string]bool{"main": true}
	v.tick(t)
	v.l.dead = nil
	v.tick(t)
	v.l.dead = map[string]bool{"main": true}

	v.tick(t)

	if lines := strings.Count(log.String(), "\n"); lines != 2 {
		t.Fatalf("log %q", log.String())
	}
}

func TestEngineLogsOnceWhenTheAliveCheckOfMainFails(t *testing.T) {
	v := newEnv(t)
	var log bytes.Buffer
	v.e.SetLog(&log)
	mustOK(t, v.run("", "", "task new", taskFile))
	v.clock.now = v.clock.now.Add(5 * time.Minute)
	v.l.aliveErr = errors.New("hc failed")

	v.tick(t)
	v.tick(t)

	if got := strings.Count(log.String(), "alive check"); got != 1 || len(v.l.sent) != 1 {
		t.Fatalf("log %q, sent %v", log.String(), v.l.sent)
	}
}

func TestResumeNoteReachesAWorkerWhoseTurnEndedDuringTheEscalation(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "escalate", "", "t1", "unclear"))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))
	if len(v.l.sent) != 0 {
		t.Fatalf("an escalated worker got a note: %v", v.l.sent)
	}

	mustOK(t, v.run("main", "", "task resume", "", "t1"))

	if !slices.Equal(v.l.sent, []string{"worker-t1: " + note}) {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestIdleTypesTheNoteForUnreadMail(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "hi"))

	mustOK(t, v.run("worker-t1", "t1", "hook idle", ""))

	if got := sentTo(v, "worker-t1"); !slices.Equal(got, []string{"worker-t1: " + note}) {
		t.Fatalf("sent %v", got)
	}
}

func TestIdleWithoutUnreadMailTypesNothing(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)

	mustOK(t, v.run("worker-t1", "t1", "hook idle", ""))

	if len(v.l.sent) != 0 {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestIdleOfAnEscalatedWorkerTypesNothingButRecordsTheWait(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "escalate", "", "t1", "unclear"))

	mustOK(t, v.run("worker-t1", "t1", "hook idle", ""))
	if len(v.l.sent) != 0 {
		t.Fatalf("sent %v", v.l.sent)
	}
	mustOK(t, v.run("main", "", "task resume", "", "t1"))

	if got := sentTo(v, "worker-t1"); !slices.Equal(got, []string{"worker-t1: " + note}) {
		t.Fatalf("sent %v", got)
	}
}

// An agent that cannot read its inbox ends each turn with the same unread message.
// Each turn end must not type the note again.
func TestTurnEndsWithTheSameUnreadMessageTypeOneNote(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "hi"))

	for range 5 {
		mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
		mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))
		mustOK(t, v.run("worker-t1", "t1", "hook idle", ""))
	}

	if got := v.l.sent; !slices.Equal(got, []string{"worker-t1: " + note}) {
		t.Fatalf("sent %v", got)
	}
}

func TestASecondMessageAfterTheNoteBringsOneMoreNote(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "one"))
	mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))
	mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	mustOK(t, v.run("main", "", "send", "", "worker-t1", "two"))
	for range 3 {
		mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
		mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))
	}

	if got := v.l.sent; len(got) != 2 {
		t.Fatalf("sent %v", got)
	}
}

func TestTheResendStillFiresForAMessageThatStaysUnreadAfterTurnEnds(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "hi"))
	mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))
	mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
	mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))

	v.clock.now = v.clock.now.Add(5 * time.Minute)
	v.tick(t)

	if got := v.l.sent; len(got) != 2 {
		t.Fatalf("sent %v", got)
	}
}

func TestAMessageMidTurnTypesOneNoteAtTheTurnEnd(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("main", "", "send", "", "worker-t1", "hi"))
	if len(v.l.sent) != 0 {
		t.Fatalf("sent %v", v.l.sent)
	}

	for range 3 {
		mustOK(t, v.run("worker-t1", "t1", "report progress", "", "half"))
		mustOK(t, v.run("worker-t1", "t1", "hook turn-ended", ""))
	}

	if !slices.Equal(v.l.sent, []string{"worker-t1: " + note}) {
		t.Fatalf("sent %v", v.l.sent)
	}
}

func TestAnApprovedTaskHoldsNoWorkerSlot(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 3)
	tk := v.state(t, "t1")
	tk.State, tk.Dir = task.Approved, "/work/t1"
	if err := v.st.Save(tk); err != nil {
		t.Fatal(err)
	}

	v.tick(t)

	if len(v.l.started) != 2 {
		t.Fatalf("started %d workers", len(v.l.started))
	}
}

func TestTickSendsNoNoticeAndNoResendForAnApprovedTask(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)
	v.l.dead = map[string]bool{"worker-t1": true}
	if err := v.st.Send(store.Message{ID: "1", From: "main", To: "worker-t1", Body: "hi", Created: v.clock.now}); err != nil {
		t.Fatal(err)
	}

	v.tick(t)
	v.clock.now = v.clock.now.Add(10 * time.Minute)
	v.tick(t)

	if len(v.inbox(t, "main")) != 0 || len(v.l.sent) != 0 {
		t.Fatalf("main got %+v, sent %v", v.inbox(t, "main"), v.l.sent)
	}
}

func TestTickEndsTheSessionsOfAnApprovedTaskAgain(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)

	v.tick(t)

	if !slices.Equal(v.l.stopped, []string{"worker-t1", "review-t1-1"}) {
		t.Fatalf("stopped %v", v.l.stopped)
	}
}

func TestSendToTheWorkerOfAnApprovedTaskFails(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Approved, 1)

	mustFail(t, v.run("main", "", "send", "hi", "worker-t1"), "does not run")
}
