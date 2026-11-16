package task_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestBranchBaseDerivesTypeAndSlug(t *testing.T) {
	tests := []struct {
		name string
		task task.Task
		want string
	}{
		{"the Type field wins over the title", task.Task{Title: "fix: Broken thing", Type: "docs", Repo: "r"}, "docs/broken-thing"},
		{"a type prefix in the title", task.Task{Title: "fix: Refuse packaging writes", Repo: "r"}, "fix/refuse-packaging-writes"},
		{"a type prefix with scope and bang", task.Task{Title: "perf(api)!: Cache lookups", Repo: "r"}, "perf/cache-lookups"},
		{"a keyword in the title", task.Task{Title: "Fix the login race", Repo: "r"}, "fix/fix-the-login-race"},
		{"a documentation keyword", task.Task{Title: "Document the config file", Repo: "r"}, "docs/document-the-config-file"},
		{"an unknown prefix stays in the slug", task.Task{Title: "wip: Something", Repo: "r"}, "feat/wip-something"},
		{"the fallback with a repo", task.Task{Title: "Add organisation locations", Repo: "r"}, "feat/add-organisation-locations"},
		{"the fallback with no repo", task.Task{Title: "Add organisation locations"}, "chore/add-organisation-locations"},
		{"a ticket key stays in the slug", task.Task{Title: "ROR-1090 Add locations", Repo: "r"}, "feat/ror-1090-add-locations"},
		{"letters with marks fold to ASCII", task.Task{Title: "Läs fältet från Åre", Repo: "r"}, "feat/las-faltet-fran-are"},
		{"a title with no ASCII word", task.Task{Title: "新しい機能", Repo: "r"}, "feat/task"},
		{"punctuation and spaces collapse", task.Task{Title: "  Make   it -- work!! ", Repo: "r"}, "feat/make-it-work"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.task.BranchBase(); got != tt.want {
				t.Errorf("BranchBase() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBranchBaseCutsTheSlugAtAWordBoundary(t *testing.T) {
	tk := task.Task{Title: "Name task branches by type and slug instead of the task id for every repo", Repo: "r"}

	got := tk.BranchBase()

	if want := "feat/name-task-branches-by-type-and-slug-instead-of-the"; got != want {
		t.Errorf("BranchBase() = %q, want %q", got, want)
	}
	if len(strings.TrimPrefix(got, "feat/")) > 50 {
		t.Errorf("slug %q is longer than 50 characters", got)
	}
}

func TestBranchBaseCutsOneLongWord(t *testing.T) {
	tk := task.Task{Title: strings.Repeat("a", 80), Repo: "r"}

	if got, want := tk.BranchBase(), "feat/"+strings.Repeat("a", 50); got != want {
		t.Errorf("BranchBase() = %q, want %q", got, want)
	}
}

func TestParseReadsTheTypeSection(t *testing.T) {
	md := "# Title\n\n## Description\n\nD.\n\n## Goal\n\nG.\n\n## Type\n\nFix\n"

	got, err := task.Parse(md)

	if err != nil {
		t.Fatal(err)
	}
	back, err := task.Parse(got.Markdown())
	if err != nil || !reflect.DeepEqual(back, got) || got.Type != "fix" {
		t.Errorf("parsed %+v, then %+v, %v", got, back, err)
	}
}

func TestParseRejectsAnUnknownType(t *testing.T) {
	md := "# Title\n\n## Description\n\nD.\n\n## Goal\n\nG.\n\n## Type\n\nbugfix\n"

	_, err := task.Parse(md)

	var fe *task.FormatError
	if !errors.As(err, &fe) || fe.Section != "Type" {
		t.Fatalf("err = %v, want a FormatError for Type", err)
	}
}
