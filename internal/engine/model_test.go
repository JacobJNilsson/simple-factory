package engine_test

import (
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestTheWorkerStartsOnTheModelOfItsTask(t *testing.T) {
	v := newEnv(t)
	if err := v.st.Create(task.Task{ID: "t1", Title: "T", Description: "d", Goal: "g", Model: "opus", State: task.Queued, Source: "main"}); err != nil {
		t.Fatal(err)
	}

	v.tick(t)

	if got := v.l.started[0].Model; got != "opus" {
		t.Fatalf("model = %q", got)
	}
}

func TestTheWorkerStartsOnTheConfiguredModelWithoutAModelSection(t *testing.T) {
	v := newEnv(t)
	if err := v.st.Create(task.Task{ID: "t1", Title: "T", Description: "d", Goal: "g", State: task.Queued, Source: "main"}); err != nil {
		t.Fatal(err)
	}

	v.tick(t)

	if got := v.l.started[0].Model; got != "work-model" {
		t.Fatalf("model = %q", got)
	}
}

func TestTaskNewRefusesAnInvalidModel(t *testing.T) {
	v := newEnv(t)

	a := v.run("main", "", "task new", "# T\n\n## Description\n\nd\n\n## Goal\n\ng\n\n## Model\n\n--bad\n")

	mustFail(t, a, "Model")
}

func TestTaskShowNamesTheModelOfATask(t *testing.T) {
	v := newEnv(t)
	mustOK(t, v.run("main", "", "task new", "# T\n\n## Description\n\nd\n\n## Goal\n\ng\n\n## Model\n\nopus\n\n## Reviewer model\n\nhaiku\n"))

	a := v.run("main", "", "task show", "", "t1")

	mustOK(t, a)
	if !strings.Contains(a.Text, "Worker model: opus") || !strings.Contains(a.Text, "Reviewer model: haiku") {
		t.Fatalf("show:\n%s", a.Text)
	}
}

func TestEditChangesTheModelOfATaskWithAWorkspace(t *testing.T) {
	v := newEnv(t)
	if err := v.st.Create(task.Task{ID: "t1", Title: "T", Description: "d", Goal: "g", State: task.Queued, Source: "main"}); err != nil {
		t.Fatal(err)
	}
	v.tick(t)

	mustOK(t, v.run("main", "", "task edit", "# T\n\n## Description\n\nd\n\n## Goal\n\ng\n\n## Model\n\nopus\n", "t1"))

	if got := v.state(t, "t1").Model; got != "opus" {
		t.Fatalf("model = %q", got)
	}
}
