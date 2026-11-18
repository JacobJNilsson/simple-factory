package task_test

import (
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestParseReadsTheModelSections(t *testing.T) {
	base := "# A\n\n## Description\n\nd\n\n## Goal\n\ng\n"

	got, err := task.Parse(base + "\n## Model\n\nopus\n\n## Reviewer model\n\nsonnet[1m]\n")

	if err != nil || got.Model != "opus" || got.ReviewerModel != "sonnet[1m]" {
		t.Fatalf("got %+v, %v", got, err)
	}
	back, err := task.Parse(got.Markdown())
	if err != nil || back.Model != "opus" || back.ReviewerModel != "sonnet[1m]" {
		t.Fatalf("round trip %+v, %v", back, err)
	}
	if none, err := task.Parse(base + "\n## Model\n\n"); err != nil || none.Model != "" {
		t.Fatalf("empty section: %+v, %v", none, err)
	}
}

func TestParseRefusesAnInvalidModelName(t *testing.T) {
	base := "# A\n\n## Description\n\nd\n\n## Goal\n\ng\n\n"
	for _, name := range []string{"--dangerously-skip-permissions", "-x", "a b", "a;b", "a/b", "$(x)"} {
		_, err := task.Parse(base + "## Model\n\n" + name + "\n")
		assertFormat(t, err, "invalid section", "Model")
		_, err = task.Parse(base + "## Reviewer model\n\n" + name + "\n")
		assertFormat(t, err, "invalid section", "Reviewer model")
	}
	_, err := task.Parse(base + "## Model\n\nopus\nsonnet\n")
	assertFormat(t, err, "more than one line in", "Model")
}

func TestModelsOverrideTheConfiguredOnesOnlyWhenSet(t *testing.T) {
	set := task.Task{Model: "opus", ReviewerModel: "haiku"}
	if got := set.WorkerModel("sonnet"); got != "opus" {
		t.Fatalf("worker = %q", got)
	}
	if got := set.ReviewerModelOr("sonnet"); got != "haiku" {
		t.Fatalf("reviewer = %q", got)
	}
	var none task.Task
	if none.WorkerModel("sonnet") != "sonnet" || none.ReviewerModelOr("sonnet") != "sonnet" {
		t.Fatal("config model not kept")
	}
}
