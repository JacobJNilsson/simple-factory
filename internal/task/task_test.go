package task_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/factory/internal/task"
)

func TestParseFailsNamingGoalWhenGoalIsMissing(t *testing.T) {
	md := "# Fix it\n\n## Description\n\nBroken.\n"

	_, err := task.Parse(md)

	assertMissing(t, err, "Goal")
}

func TestParseFailsNamingTitleWhenTitleIsMissing(t *testing.T) {
	md := "\n"

	_, err := task.Parse(md)

	assertMissing(t, err, "Title")
}

func TestParseFailsNamingDescriptionWhenDescriptionIsEmpty(t *testing.T) {
	md := "# Fix it\n\n## Description\n\n## Goal\n\nFixed.\n"

	_, err := task.Parse(md)

	assertMissing(t, err, "Description")
}

func TestParseReadsOptionalSections(t *testing.T) {
	md := "# Fix it\n\n## Description\n\nBroken.\n\n## Goal\n\nFixed.\n\n## Repo\n\ngit@example.com:x/y.git\n\n## Check\n\nmake check\n\n## Protected\n\n- a/b\n- c.go\n"

	got, err := task.Parse(md)

	if err != nil {
		t.Fatal(err)
	}
	want := task.Task{Title: "Fix it", Description: "Broken.", Goal: "Fixed.", Repo: "git@example.com:x/y.git", Check: "make check", Protected: []string{"a/b", "c.go"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMarkdownRoundTripKeepsEveryField(t *testing.T) {
	in := task.Task{
		Title:       "Fix it",
		Description: "Broken.\n\nSecond paragraph.",
		Goal:        "Fixed.",
		Repo:        "git@example.com:x/y.git",
		Check:       "make check",
		Protected:   []string{"a/b", "c.go"},
		Extra:       "## Notes\n\nSome notes.\n\n## Links\n\n- one\n- two",
	}

	got, err := task.Parse(in.Markdown())

	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("got %+v, want %+v", got, in)
	}
}

func assertMissing(t *testing.T, err error, section string) {
	t.Helper()
	var missing *task.MissingError
	if !errors.As(err, &missing) || missing.Section != section {
		t.Fatalf("want a MissingError for %s, got %v", section, err)
	}
}

func assertFormat(t *testing.T, err error, problem, section string) {
	t.Helper()
	var bad *task.FormatError
	if !errors.As(err, &bad) || bad.Problem != problem || bad.Section != section {
		t.Fatalf("want a FormatError %s %s, got %v", problem, section, err)
	}
}

func TestParseFailsOnADuplicateSection(t *testing.T) {
	for _, name := range []string{"Description", "Goal", "Repo", "Check", "Protected"} {
		md := "# T\n\n## Description\n\nD.\n\n## Goal\n\nG.\n\n## " + name + "\n\nx\n\n## " + name + "\n\ny\n"

		_, err := task.Parse(md)

		assertFormat(t, err, "duplicate section", name)
	}
}

func TestParseFailsOnTextBeforeTheTitle(t *testing.T) {
	md := "Stray line.\n# T\n\n## Description\n\nD.\n\n## Goal\n\nG.\n"

	_, err := task.Parse(md)

	assertFormat(t, err, "text before the title", "")
}

func TestParseFailsOnASectionHeadingBeforeTheTitle(t *testing.T) {
	md := "## Description\n\nD.\n\n# T\n\n## Goal\n\nG.\n"

	_, err := task.Parse(md)

	assertFormat(t, err, "text before the title", "")
}

func TestParseKeepsATitleLineInsideDescriptionAsText(t *testing.T) {
	in := task.Task{Title: "T", Description: "Do this.\n\n# Steps\n\n1. One.", Goal: "G."}

	got, err := task.Parse(in.Markdown())

	if err != nil || !reflect.DeepEqual(got, in) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestParseReadsACRLFFileLikeAnLFFile(t *testing.T) {
	in := task.Task{Title: "T", Description: "Line one.\nLine two.", Goal: "G.", Protected: []string{"a"}, Extra: "## Notes\n\nN."}
	crlf := strings.ReplaceAll(in.Markdown(), "\n", "\r\n")

	got, err := task.Parse(crlf)

	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("got %+v, want %+v", got, in)
	}
}

func TestParseKeepsAGoalHeadingInsideATildeFenceInDescription(t *testing.T) {
	md := "# T\n\n## Description\n\nSee:\n\n~~~md\n## Goal\n~~~\n\n## Goal\n\nG.\n"

	got, err := task.Parse(md)

	if err != nil {
		t.Fatal(err)
	}
	if got.Goal != "G." || !strings.Contains(got.Description, "~~~md\n## Goal\n~~~") {
		t.Fatalf("got %+v", got)
	}
}

func TestParseTreatsAnIndentedFenceAsAFence(t *testing.T) {
	md := "# T\n\n## Description\n\n   ```\n## Goal\n   ```\n\n## Goal\n\nG.\n"

	got, err := task.Parse(md)

	if err != nil || got.Goal != "G." {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestParseDoesNotCloseAFenceWithALineThatHasAnInfoString(t *testing.T) {
	md := "# T\n\n## Description\n\n```\n```go\n## Goal\n```\n\n## Goal\n\nG.\n"

	got, err := task.Parse(md)

	if err != nil || got.Goal != "G." {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestParseGivesANilSliceForAnEmptyProtectedSection(t *testing.T) {
	md := "# T\n\n## Description\n\nD.\n\n## Goal\n\nG.\n\n## Protected\n"

	got, err := task.Parse(md)

	if err != nil || got.Protected != nil {
		t.Fatalf("got %#v, %v", got.Protected, err)
	}
}
