package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func withAfter(after string) string { return taskFile + "\n## After\n\n" + after + "\n" }

func (v env) setState(t *testing.T, id string, state task.State) {
	t.Helper()
	tk := v.state(t, id)
	tk.State = state
	if err := v.st.Save(tk); err != nil {
		t.Fatal(err)
	}
}

func (v env) startedNames() []string {
	var names []string
	for _, s := range v.l.started {
		names = append(names, s.Name)
	}
	return names
}

func TestTickKeepsATaskWaitingUntilItsDependencyIsDone(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 1)
	mustOK(t, v.run("main", "", "task new", withAfter("t1")))
	v.setState(t, "t1", task.Review)

	v.tick(t)
	if got := v.startedNames(); len(got) != 0 {
		t.Fatalf("started %v while t1 is in review", got)
	}

	v.setState(t, "t1", task.Done)
	v.tick(t)
	if got := v.startedNames(); !slices.Equal(got, []string{"worker-t2"}) {
		t.Fatalf("started %v, want worker-t2", got)
	}
}

func TestTickStartsAnotherQueuedTaskAheadOfAWaitingOne(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("main", "", "task new", taskFile))
	mustOK(t, v.run("main", "", "task new", withAfter("t1")))
	mustOK(t, v.run("main", "", "task new", taskFile))
	v.setState(t, "t1", task.Escalated)

	v.tick(t)

	if got := v.startedNames(); !slices.Equal(got, []string{"worker-t3"}) {
		t.Fatalf("started %v, want worker-t3 only", got)
	}
}

func TestTickCountsAnArchivedDoneDependencyAsDone(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 1)
	v.setState(t, "t1", task.Done)
	mustOK(t, v.run("main", "", "task archive", "", "t1"))
	mustOK(t, v.run("main", "", "task new", withAfter("t1")))

	v.tick(t)

	if got := v.startedNames(); !slices.Equal(got, []string{"worker-t2"}) {
		t.Fatalf("started %v", got)
	}
}

func TestTaskNewRejectsAnAfterIdThatNamesNoTask(t *testing.T) {
	v := newEnv(t)

	mustFail(t, v.run("main", "", "task new", withAfter("t7")), "t7")

	if tasks, _ := v.st.Tasks(); len(tasks) != 0 {
		t.Fatalf("stored %+v", tasks)
	}
}

func TestTaskNewRejectsTheTasksOwnId(t *testing.T) {
	v := newEnv(t)

	mustFail(t, v.run("main", "", "task new", withAfter("t1")), "t1")
}

func TestTaskEditRejectsOwnUnknownAndNewerAfterIds(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 3)

	mustFail(t, v.run("main", "", "task edit", withAfter("t1"), "t1"), "itself")
	mustFail(t, v.run("main", "", "task edit", withAfter("t9"), "t1"), "no task")
	mustFail(t, v.run("main", "", "task edit", withAfter("t3"), "t1"), "newer")
	if after := v.state(t, "t1").After; len(after) != 0 {
		t.Fatalf("a rejected edit stored %v", after)
	}
}

func TestTaskEditAcceptsAnOlderAfterIdInSeveralForms(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 3)

	mustOK(t, v.run("main", "", "task edit", withAfter("t1, t2"), "t3"))
	if got := v.state(t, "t3").After; !slices.Equal(got, []string{"t1", "t2"}) {
		t.Fatalf("got %v", got)
	}
	mustOK(t, v.run("main", "", "task edit", withAfter("- t1\n- t2"), "t3"))
	mustOK(t, v.run("main", "", "task edit", withAfter("t1 t2"), "t3"))
	if got := v.state(t, "t3").After; !slices.Equal(got, []string{"t1", "t2"}) {
		t.Fatalf("got %v", got)
	}
}

func TestTickTellsMainOnceThatADependencyIsCancelled(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 1)
	mustOK(t, v.run("main", "", "task new", withAfter("t1")))
	mustOK(t, v.run("main", "", "task cancel", "", "t1"))

	v.tick(t)
	v.tick(t)

	var notes []string
	for _, m := range v.inbox(t, "main") {
		if strings.Contains(m.Body, "Task t2 waits for t1, which is cancelled") {
			notes = append(notes, m.Body)
		}
	}
	if len(notes) != 1 {
		t.Fatalf("got %d notes: %v", len(notes), notes)
	}
	if tk := v.state(t, "t2"); tk.State != task.Queued {
		t.Fatalf("t2 is %s, want queued", tk.State)
	}
	if got := v.startedNames(); len(got) != 0 {
		t.Fatalf("started %v", got)
	}
}

func TestTaskListShowAndStatusShowWhatAQueuedTaskWaitsFor(t *testing.T) {
	v := newEnv(t)
	v.queue(t, 2)
	mustOK(t, v.run("main", "", "task new", withAfter("t1, t2")))
	v.setState(t, "t2", task.Done)
	v.tick(t)

	list := v.run("main", "", "task list", "")
	show := v.run("main", "", "task show", "", "t3")

	mustOK(t, list)
	mustOK(t, show)
	if !strings.Contains(list.Text, "t3\tqueued (after t1)\t") {
		t.Errorf("list:\n%s", list.Text)
	}
	if !strings.Contains(show.Text, "State: queued (after t1)") {
		t.Errorf("show:\n%s", show.Text)
	}
	v.statusShows(t, "t3", "queued (after t1)")
}
