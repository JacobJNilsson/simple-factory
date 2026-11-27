package engine_test

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

const wakeNote = "sfy woke this session, because a message arrived for it. Run: sfy inbox"

// newWakeEnv is newEnv with the given wake settings.
func newWakeEnv(t *testing.T, wake config.Wake) env {
	t.Helper()
	home := t.TempDir()
	st := store.Files(home)
	l, ch, ws := &fakeLauncher{}, &fakeChannel{}, &fakeWorkspaces{}
	cfg := config.Config{
		ReviewRounds: 3, MaxWorkers: 2, ResendMinutes: 5, NoteFallbackAfter: 4 * time.Second,
		Models: config.Models{Worker: "work-model", Reviewer: "rev-model"}, Wake: wake,
	}
	clock := &fakeClock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	return env{home, st, l, ch, ws, clock, engine.New(st, l, ch, ws, cfg, clock)}
}

// wakeAll turns waking on for workers and reviewers, with the default cap.
var wakeAll = config.Wake{Worker: true, Reviewer: true, MaxPerHour: 3}

// permissions holds the spec fields that decide what a session may do.
type permissions struct {
	ReadOnly, Unsandboxed, NoPush bool
	AllowWrite, DenyRead, PassEnv *[]string
	Protected                     []string
	Dir, Model, Branch, TaskFile  string
}

func permissionsOf(s engine.SessionSpec) permissions {
	return permissions{
		ReadOnly: s.ReadOnly, Unsandboxed: s.Unsandboxed, NoPush: s.NoPush,
		AllowWrite: s.AllowWrite, DenyRead: s.DenyRead, PassEnv: s.PassEnv, Protected: s.Protected,
		Dir: s.Dir, Model: s.Model, Branch: s.Branch, TaskFile: s.TaskFile,
	}
}

// resumes returns the specs that resumed a session.
func (v env) resumes() []engine.SessionSpec {
	var out []engine.SessionSpec
	for _, s := range v.l.started {
		if s.Resume {
			out = append(out, s)
		}
	}
	return out
}

// startedWorker queues task t1 with a repo, starts its worker, and ends the worker's session.
func (v env) startedWorker(t *testing.T, hold bool) engine.SessionSpec {
	t.Helper()
	tk := task.Task{ID: "t1", Title: "Title", Description: "Do it.", Goal: "Done.", Repo: "some/repo", Hold: hold, State: task.Queued, Source: "lead"}
	if err := v.st.Create(tk); err != nil {
		t.Fatal(err)
	}
	v.tick(t)
	if len(v.l.started) != 1 {
		t.Fatalf("started %+v", v.l.started)
	}
	v.l.dead = map[string]bool{"worker-t1": true}
	return v.l.started[0]
}

// leadNotes returns the bodies of the lead's messages that contain part.
func (v env) leadNotes(t *testing.T, part string) []string {
	t.Helper()
	var out []string
	for _, m := range v.inbox(t, "lead") {
		if strings.Contains(m.Body, part) {
			out = append(out, m.Body)
		}
	}
	return out
}

// moveTask sets the state and the round of task t1 without any command, as if the task moved on since a message.
func (v env) moveTask(t *testing.T, state task.State, round int) {
	t.Helper()
	tk := v.state(t, "t1")
	tk.State, tk.Round = state, round
	if err := v.st.Save(tk); err != nil {
		t.Fatal(err)
	}
}

func TestAMessageWakesTheGoneWorkerWithTheSettingsOfItsFirstStart(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	first := v.startedWorker(t, false)

	mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))
	if got := v.resumes(); len(got) != 0 {
		t.Fatalf("the send woke the worker before the tick: %+v", got)
	}
	v.tick(t)

	got := v.resumes()
	if len(got) != 1 || got[0].Name != "worker-t1" || got[0].ResumePrompt != wakeNote {
		t.Fatalf("resumes %+v", got)
	}
	if a, b := permissionsOf(first), permissionsOf(got[0]); !reflect.DeepEqual(a, b) {
		t.Errorf("first start %+v, wake %+v", a, b)
	}
	if msgs := v.inbox(t, "worker-t1"); len(msgs) != 1 || msgs[0].Body != "hello" {
		t.Errorf("worker inbox %+v, want the message unread", msgs)
	}
}

func TestAWakeStopsWhatRemainsOfTheSessionFirst(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	v.startedWorker(t, false)
	stops := len(v.l.stopped)

	mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))
	v.tick(t)

	if got := v.l.stopped[stops:]; !reflect.DeepEqual(got, []string{"worker-t1"}) {
		t.Errorf("stopped %v", got)
	}
}

func TestTheEndOfATaskStopsAWokenWorker(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	v.seed(t, task.Working, 1)
	v.l.dead = map[string]bool{"worker-t1": true}
	// The failed alive check after the wake neither sets nor clears the gone mark, so only the wake can clear it.
	v.l.onStart = func(engine.SessionSpec) {
		v.l.aliveErrOf = map[string]error{"worker-t1": errors.New("no answer")}
	}
	mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))
	v.tick(t)
	if len(v.resumes()) != 1 {
		t.Fatalf("resumes %+v", v.resumes())
	}
	stops := len(v.l.stopped)

	mustOK(t, v.run("lead", "", "task cancel", "", "t1"))

	if got := v.l.stopped[stops:]; !slices.Contains(got, "worker-t1") {
		t.Errorf("stopped after the wake %v, want worker-t1", got)
	}
}

func TestAWokenWorkerOfAHeldTaskGetsNoPush(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	v.startedWorker(t, true)

	mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))
	v.tick(t)

	if got := v.resumes(); len(got) != 1 || !got[0].NoPush {
		t.Fatalf("resumes %+v, want NoPush", got)
	}
}

func TestAWorkerOfATaskThatNoLongerNeedsItGetsNoWake(t *testing.T) {
	for _, state := range []task.State{task.Done, task.Cancelled, task.Escalated, task.Approved, task.Queued} {
		t.Run(string(state), func(t *testing.T) {
			v := newWakeEnv(t, wakeAll)
			v.seed(t, task.Working, 1)
			v.l.dead = map[string]bool{"worker-t1": true}
			mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))

			v.moveTask(t, state, 1)
			v.tick(t)

			if got := v.resumes(); len(got) != 0 {
				t.Fatalf("resumes %+v", got)
			}
		})
	}
}

func TestACancelBetweenTheMessageAndTheTickStopsTheWake(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	v.seed(t, task.Working, 1)
	v.l.dead = map[string]bool{"worker-t1": true}
	mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))

	mustOK(t, v.run("lead", "", "task cancel", "", "t1"))
	v.tick(t)

	if got := v.resumes(); len(got) != 0 {
		t.Fatalf("resumes %+v", got)
	}
}

func TestTheGoneReviewerOfTheCurrentRoundWakes(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	v.firstReview(t)
	v.l.dead = map[string]bool{"review-t1-1": true}

	mustOK(t, v.run("lead", "", "send", "", "review-t1-1", "look again"))
	v.tick(t)

	got := v.resumes()
	if len(got) != 1 || got[0].Name != "review-t1-1" || got[0].Kind != "reviewer" || got[0].ResumePrompt != wakeNote {
		t.Fatalf("resumes %+v", got)
	}
}

func TestTheReviewerOfAnEarlierRoundGetsNoWake(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	v.firstReview(t)
	v.l.dead = map[string]bool{"review-t1-1": true}
	mustOK(t, v.run("lead", "", "send", "", "review-t1-1", "look again"))

	v.moveTask(t, task.Review, 2)
	v.tick(t)

	if got := v.resumes(); len(got) != 0 {
		t.Fatalf("resumes %+v", got)
	}
}

func TestAReviewerThatGaveItsVerdictGetsNoWake(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	v.firstReview(t)
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Looks right."))
	v.l.dead = map[string]bool{"review-t1-1": true}

	mustOK(t, v.run("lead", "", "send", "", "review-t1-1", "one more thing"))
	v.tick(t)

	if got := v.resumes(); len(got) != 0 {
		t.Fatalf("resumes %+v", got)
	}
}

func TestAWorkerWhoseAliveCheckFailsGetsNoWake(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	v.seed(t, task.Working, 1)
	v.l.aliveErr = errors.New("exit status 2")

	mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))
	_ = v.e.Tick()

	if len(v.l.started) != 0 {
		t.Fatalf("started %+v", v.l.started)
	}
}

func TestARunningWorkerGetsNoWake(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	v.seed(t, task.Working, 1)

	mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))
	v.tick(t)

	if len(v.l.started) != 0 {
		t.Fatalf("started %+v", v.l.started)
	}
}

func TestWithWakeOffForWorkersSendStillRefusesAGoneWorker(t *testing.T) {
	v := newWakeEnv(t, config.Wake{Reviewer: true, MaxPerHour: 3})
	v.seed(t, task.Working, 1)
	v.l.dead = map[string]bool{"worker-t1": true}

	mustFail(t, v.run("lead", "", "send", "", "worker-t1", "hello"), "sfy task feedback t1")
	v.tick(t)

	if len(v.l.started) != 0 {
		t.Fatalf("started %+v", v.l.started)
	}
}

func TestTheCapStopsTheFourthWakeInAnHour(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	v.seed(t, task.Working, 1)
	v.l.dead = map[string]bool{"worker-t1": true}

	for range 5 {
		mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))
		v.tick(t)
		v.clock.now = v.clock.now.Add(time.Minute)
	}

	if got := len(v.resumes()); got != 3 {
		t.Fatalf("%d wakes, want 3", got)
	}
	want := "sfy woke worker-t1 3 times in the last hour, so it wakes it no more before 13:00. Its messages wait in its inbox."
	if got := v.leadNotes(t, "times in the last hour"); len(got) != 1 || got[0] != want {
		t.Fatalf("the lead got %q", got)
	}

	v.clock.now = v.clock.now.Add(time.Hour)
	mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))
	v.tick(t)

	if got := len(v.resumes()); got != 4 {
		t.Fatalf("%d wakes after the hour, want 4", got)
	}
}

func TestAFailedWakeTellsTheLeadAndCountsTowardTheCap(t *testing.T) {
	v := newWakeEnv(t, config.Wake{Worker: true, MaxPerHour: 1})
	v.seed(t, task.Working, 1)
	v.l.dead = map[string]bool{"worker-t1": true}
	v.l.startErr = errors.New("no pane")
	var log bytes.Buffer
	v.e.SetLog(&log)

	mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))
	v.tick(t)

	want := "sfy could not wake worker-t1: no pane. Its messages wait in its inbox. Run sfy task feedback t1 <text> to start a new worker, or sfy task cancel t1."
	if got := v.leadNotes(t, "could not wake"); len(got) != 1 || got[0] != want {
		t.Fatalf("the lead got %q", got)
	}
	if msgs := v.inbox(t, "worker-t1"); len(msgs) != 1 {
		t.Errorf("worker inbox %+v, want the message unread", msgs)
	}
	if tk := v.state(t, "t1"); tk.State != task.Working {
		t.Errorf("task moved to %s", tk.State)
	}
	if !strings.Contains(log.String(), "could not wake worker-t1: no pane") {
		t.Errorf("log %q", log.String())
	}

	mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))
	v.tick(t)

	if got := v.leadNotes(t, "times in the last hour"); len(got) != 1 {
		t.Fatalf("the lead got %q, want the cap note", got)
	}
}

func TestResumeWakesTheGoneWorkerOfAnEscalatedTask(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	v.escalateWithAWaitingWorker(t)
	v.l.dead = map[string]bool{"worker-t1": true}

	mustOK(t, v.run("lead", "", "task resume", "", "t1"))
	v.tick(t)

	if got := v.resumes(); len(got) != 1 || got[0].Name != "worker-t1" {
		t.Fatalf("resumes %+v", got)
	}
	msgs := v.inbox(t, "worker-t1")
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Body, "Task t1 is resumed.") {
		t.Errorf("worker inbox %+v, want the resume note unread", msgs)
	}
}

func TestAfterDownAMessageWakesNothing(t *testing.T) {
	v := newWakeEnv(t, wakeAll)
	v.seed(t, task.Working, 1)
	v.l.dead = map[string]bool{"worker-t1": true}
	mustOK(t, v.run("lead", "", "send", "", "worker-t1", "hello"))

	mustOK(t, v.run("", "", "down", ""))
	v.tick(t)

	if got := v.resumes(); len(got) != 0 {
		t.Fatalf("resumes %+v", got)
	}
}

func TestTheGoneNoteSaysThatAMessageWakesTheWorker(t *testing.T) {
	for name, c := range map[string]struct {
		wake config.Wake
		want string
	}{
		"on":  {wakeAll, "The worker of task t1 is gone. Its session no longer runs. sfy wakes it when a message arrives for it."},
		"off": {config.Wake{}, "The worker of task t1 is gone. Its session no longer runs."},
	} {
		t.Run(name, func(t *testing.T) {
			v := newWakeEnv(t, c.wake)
			v.seed(t, task.Working, 1)
			v.l.dead = map[string]bool{"worker-t1": true}

			v.tick(t)

			if got := v.goneNotes(t); len(got) != 1 || got[0] != c.want {
				t.Fatalf("the lead got %q", got)
			}
		})
	}
}
