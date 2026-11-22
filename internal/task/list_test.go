package task_test

import (
	"errors"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

const listTask = "# T\n\n## Description\n\nD.\n\n## Goal\n\nG.\n\n## List\n\ndraft\n"

func TestParseReadsTheListSection(t *testing.T) {
	got, err := task.Parse(listTask)
	if err != nil || got.List != "draft" {
		t.Fatalf("List = %q, %v, want draft", got.List, err)
	}
	again, err := task.Parse(got.Markdown())
	if err != nil || again.List != "draft" {
		t.Fatalf("after Markdown, List = %q, %v, want draft", again.List, err)
	}
}

func TestParseRefusesAListNameOutsideTheNameRule(t *testing.T) {
	_, err := task.Parse("# T\n\n## Description\n\nD.\n\n## Goal\n\nG.\n\n## List\n\nDraft list\n")
	var fe *task.FormatError
	if !errors.As(err, &fe) || fe.Section != "List" {
		t.Fatalf("Parse = %v, want an invalid List section", err)
	}
}

func TestPositionPrefersTheRecordedPositionOfAnOpenTask(t *testing.T) {
	at := process.Position{List: "load", Agent: "auditor"}
	open := task.Task{State: task.Review, At: at}
	if got := open.Position(); got != at {
		t.Errorf("Position = %v, want %v", got, at)
	}
	if got := (task.Task{State: task.Queued, At: at}).Position(); got != (process.Position{}) {
		t.Errorf("Position of a queued task = %v, want none", got)
	}
}
