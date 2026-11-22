package task_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestParseFailsForMoreThanOneLineInASingleLineSection(t *testing.T) {
	for _, section := range []string{"Check", "Repo"} {
		md := "# T\n\n## Description\n\nD.\n\n## Goal\n\nG.\n\n## " + section + "\n\nfirst\nsecond\n"

		_, err := task.Parse(md)

		var fe *task.FormatError
		if !errors.As(err, &fe) || fe.Section != section || fe.Problem != "more than one line in" {
			t.Fatalf("%s: got %v", section, err)
		}
	}
}

func TestParseAcceptsBlankLinesAroundASingleLineSection(t *testing.T) {
	md := "# T\n\n## Description\n\nD.\n\n## Goal\n\nG.\n\n## Check\n\n\nmake check\n\n"

	got, err := task.Parse(md)

	if err != nil || got.Check != "make check" {
		t.Fatalf("got %q, %v", got.Check, err)
	}
}

func TestParseTrimsBackticksFromProtectedPaths(t *testing.T) {
	md := "# T\n\n## Description\n\nD.\n\n## Goal\n\nG.\n\n## Protected\n\n- `SPEC.md`\n- `docs/`\n- plain.md\n"

	got, err := task.Parse(md)

	want := []string{"SPEC.md", "docs/", "plain.md"}
	if err != nil || !reflect.DeepEqual(got.Protected, want) {
		t.Fatalf("got %#v, %v", got.Protected, err)
	}
}

// taskFields mirrors task.Task field for field. The conversion below stops compiling when task.Task gains,
// loses or reorders a field, which forces this file to say whether an edit keeps or replaces the new field.
type taskFields struct {
	ID            string
	Title         string
	Description   string
	Goal          string
	Repo          string
	Check         string
	Type          string
	Protected     []string
	After         []string
	Hold          bool
	Existing      string
	Model         string
	ReviewerModel string
	Extra         string

	State    task.State
	Before   task.State
	Round    int
	Archived bool
	Source   string
	SourceID string
	Progress string
	Pending  []string
	Reopened bool
	Pushing  bool
	Released bool
	Branch   string
	Lease    string
	Dir      string
	Updated  time.Time
	Checking bool
}

var _ = taskFields(task.Task{})

func TestWithFileFieldsOfReplacesExactlyTheFileFields(t *testing.T) {
	stored := task.Task{
		ID: "s-ID", Title: "s-Title", Description: "s-Description", Goal: "s-Goal", Repo: "s-Repo",
		Check: "s-Check", Type: "s-Type", Protected: []string{"s-Protected"}, After: []string{"s-After"},
		Existing: "s-Existing", Model: "s-Model", ReviewerModel: "s-RModel", Extra: "s-Extra", State: "s-State", Before: "s-Before", Round: 3,
		Archived: true, Source: "s-Source", SourceID: "s-SourceID", Progress: "s-Progress", Pending: []string{"s-Pending"},
		Reopened: true, Pushing: true, Released: true, Branch: "s-Branch", Lease: "s-Lease", Dir: "s-Dir", Updated: time.Unix(1000, 0),
	}
	edited := task.Task{
		ID: "e-ID", Title: "e-Title", Description: "e-Description", Goal: "e-Goal", Repo: "e-Repo",
		Check: "e-Check", Type: "e-Type", Protected: []string{"e-Protected"}, After: []string{"e-After"}, Hold: true,
		Existing: "e-Existing", Model: "e-Model", ReviewerModel: "e-RModel", Extra: "e-Extra", State: "e-State", Before: "e-Before", Round: 9,
		Archived: false, Source: "e-Source", SourceID: "e-SourceID", Progress: "e-Progress", Pending: []string{"e-Pending"},
		Reopened: false, Pushing: false, Released: false, Branch: "e-Branch", Lease: "e-Lease", Dir: "e-Dir", Updated: time.Unix(2000, 0),
	}
	// File fields come from edited, and every other field comes from stored.
	want := task.Task{
		ID: "s-ID", Title: "e-Title", Description: "e-Description", Goal: "e-Goal", Repo: "e-Repo",
		Check: "e-Check", Type: "e-Type", Protected: []string{"e-Protected"}, After: []string{"e-After"}, Hold: true,
		Existing: "e-Existing", Model: "e-Model", ReviewerModel: "e-RModel", Extra: "e-Extra", State: "s-State", Before: "s-Before", Round: 3,
		Archived: true, Source: "s-Source", SourceID: "s-SourceID", Progress: "s-Progress", Pending: []string{"s-Pending"},
		Reopened: true, Pushing: true, Released: true, Branch: "s-Branch", Lease: "s-Lease", Dir: "s-Dir", Updated: time.Unix(1000, 0),
	}

	got := stored.WithFileFieldsOf(edited)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
