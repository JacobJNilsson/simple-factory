package engine_test

import (
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestTaskArchiveKeepsTheStateAndLeavesTheWorkspaceAlone(t *testing.T) {
	for _, state := range []task.State{task.Done, task.Cancelled} {
		v := newEnv(t)
		v.seed(t, state, 1)

		mustOK(t, v.run("lead", "", "task archive", "", "t1"))

		tk := v.state(t, "t1")
		if !tk.Archived || tk.State != state || tk.Dir != "/work/t1" || len(v.ws.removed) != 0 || len(v.l.stopped) != 0 {
			t.Fatalf("got %+v removed %v stopped %v", tk, v.ws.removed, v.l.stopped)
		}
	}
}

func TestTaskArchiveIsForTheOperatorAndTheMainAgent(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Done, 1)

	mustFail(t, v.run("worker-t1", "t1", "task archive", "", "t1"), "task archive")
	mustOK(t, v.run("", "", "task archive", "", "t1"))
}

func TestTaskArchiveRefusesEveryOtherStateAndNamesIt(t *testing.T) {
	for _, state := range []task.State{task.New, task.Queued, task.Working, task.Review, task.Feedback, task.Approved, task.Escalated} {
		v := newEnv(t)
		v.seed(t, state, 0)

		mustFail(t, v.run("lead", "", "task archive", "", "t1"), string(state))

		if v.state(t, "t1").Archived {
			t.Fatalf("task in %s was archived", state)
		}
	}
}

func TestTaskArchiveOfAnArchivedTaskFails(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Done, 1)
	mustOK(t, v.run("lead", "", "task archive", "", "t1"))

	mustFail(t, v.run("lead", "", "task archive", "", "t1"), "already archived")
}

func TestStatusLeavesOutAnArchivedTask(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Cancelled, 0)

	mustOK(t, v.run("lead", "", "task archive", "", "t1"))

	if strings.Contains(v.status(t), "t1") {
		t.Fatalf("status.md still lists t1:\n%s", v.status(t))
	}
}

func TestTaskListHidesArchivedTasksUnlessAsked(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Cancelled, 0)
	mustOK(t, v.run("lead", "", "task archive", "", "t1"))

	if a := v.run("lead", "", "task list", ""); !a.OK || strings.Contains(a.Text, "t1") {
		t.Fatalf("default list: %+v", a)
	}
	if a := v.run("lead", "", "task list", "", "--archived"); !a.OK || !strings.Contains(a.Text, "t1\tcancelled") {
		t.Fatalf("archived list: %+v", a)
	}
}

func TestTaskListArchivedLeavesOutTasksThatAreNotArchived(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)

	if a := v.run("lead", "", "task list", "", "--archived"); !a.OK || a.Text != "" {
		t.Fatalf("got %+v", a)
	}
}

func TestTaskShowWorksForAnArchivedTaskAndSaysSo(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Done, 1)
	mustOK(t, v.run("lead", "", "task archive", "", "t1"))

	a := v.run("lead", "", "task show", "", "t1")

	if !a.OK || !strings.Contains(a.Text, "State: done") || !strings.Contains(a.Text, "Archived: yes") {
		t.Fatalf("got %+v", a)
	}
}

func TestTaskShowOfAnUnarchivedTaskDoesNotSayArchived(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Done, 1)

	if a := v.run("lead", "", "task show", "", "t1"); strings.Contains(a.Text, "Archived") {
		t.Fatalf("got %+v", a)
	}
}

func TestNewTasksNeverReuseTheIdOfAnArchivedTask(t *testing.T) {
	v := newEnv(t)
	first := v.run("lead", "", "task new", taskFile)
	mustOK(t, first)
	id := strings.TrimSpace(first.Text)
	mustOK(t, v.run("lead", "", "task cancel", "", id))
	mustOK(t, v.run("lead", "", "task archive", "", id))

	second := v.run("lead", "", "task new", taskFile)

	mustOK(t, second)
	if strings.TrimSpace(second.Text) == id {
		t.Fatalf("id %s was reused", id)
	}
}
