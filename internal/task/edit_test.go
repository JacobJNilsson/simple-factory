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

// editReplaces names the fields that an edit takes from the task file. Every other field is state and survives.
var editReplaces = map[string]bool{
	"Title": true, "Description": true, "Goal": true, "Repo": true, "Check": true,
	"Protected": true, "After": true, "Type": true, "Extra": true,
}

// fill sets every field of a task to a non-zero value that carries the given tag.
func fill(t *testing.T, tag string) task.Task {
	t.Helper()
	var tk task.Task
	v := reflect.ValueOf(&tk).Elem()
	for i := range v.NumField() {
		f, name := v.Field(i), v.Type().Field(i).Name
		switch f.Kind() {
		case reflect.String:
			f.SetString(tag + name)
		case reflect.Int:
			f.SetInt(7)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Slice:
			f.Set(reflect.ValueOf([]string{tag + name}))
		case reflect.Struct:
			f.Set(reflect.ValueOf(time.Unix(1000, 0)))
		default:
			t.Fatalf("field %s has kind %s, teach fill about it", name, f.Kind())
		}
	}
	return tk
}

func TestWithFileFieldsOfReplacesExactlyTheFileFields(t *testing.T) {
	stored, edited := fill(t, "stored-"), fill(t, "edited-")

	got := stored.WithFileFieldsOf(edited)

	g, s, e := reflect.ValueOf(got), reflect.ValueOf(stored), reflect.ValueOf(edited)
	for i := range g.NumField() {
		name := g.Type().Field(i).Name
		want := s.Field(i).Interface()
		if editReplaces[name] {
			want = e.Field(i).Interface()
		}
		if !reflect.DeepEqual(g.Field(i).Interface(), want) {
			t.Errorf("field %s: got %v, want %v", name, g.Field(i).Interface(), want)
		}
	}
}
