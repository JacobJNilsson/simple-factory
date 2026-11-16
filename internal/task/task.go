// Package task defines a task, its states and its file format.
package task

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// State is a step in a task's life.
type State string

// The task states from the spec.
const (
	New       State = "new"
	Queued    State = "queued"
	Working   State = "working"
	Review    State = "review"
	Feedback  State = "feedback"
	Approved  State = "approved"
	Done      State = "done"
	Escalated State = "escalated"
	Cancelled State = "cancelled"
)

// Task is a unit of work. The first group of fields comes from task.md and the second from the state file.
type Task struct {
	ID          string
	Title       string
	Description string
	Goal        string
	Repo        string
	Check       string
	// Type is the optional Conventional Commits type from the Type section. It decides the branch prefix.
	Type      string
	Protected []string
	// After lists the ids of tasks that must be done before sfy starts this task's worker.
	After []string
	// Extra holds the sections that sfy does not know, verbatim, so an extension can add its own.
	Extra string

	State State
	// Before is the state that an escalated task had before it escalated, and is empty in every other state.
	Before State
	Round  int
	// Archived hides a done or cancelled task from the default views. The task keeps its state.
	Archived bool
	// Source is "main" for the main agent, otherwise the creator's name.
	Source string
	// Progress is the worker's latest progress report.
	Progress string
	// Pending holds feedback that no worker has read yet. The next worker's first message carries it.
	Pending []string
	// Reopened is true after feedback sent the task back from approved, done or cancelled. Its branch holds earlier work.
	Reopened bool
	// Pushing is true in review after a passed review of a task with a repo, until the worker reports that it pushed.
	Pushing bool
	// Branch is the git branch of a task with a repo. sfy picks it once, so it never changes. It is empty until then.
	Branch string
	// Dir is the task's workspace folder, empty until sfy creates it.
	Dir     string
	Updated time.Time
}

// MissingError reports a required part of a task file that is absent or empty.
type MissingError struct {
	// Section is "Title", "Description" or "Goal".
	Section string
}

func (e *MissingError) Error() string {
	return fmt.Sprintf("task file has no %s", e.Section)
}

// FormatError reports a task file whose structure Parse cannot read.
type FormatError struct {
	// Problem is "duplicate section", "more than one line in" or "text before the title".
	Problem string
	// Section names the duplicated section, and is empty for the other problems.
	Section string
}

func (e *FormatError) Error() string {
	if e.Section != "" {
		return fmt.Sprintf("task file: %s %s", e.Problem, e.Section)
	}
	return "task file: " + e.Problem
}

// fence tracks an open code fence, so a heading inside it stays text.
type fence struct {
	char  byte
	count int
}

// fenceStart returns the fence that a line opens, or false.
func fenceStart(line string) (fence, bool) {
	l := strings.TrimLeft(line, " ")
	if len(line)-len(l) > 3 || len(l) < 3 || (l[0] != '`' && l[0] != '~') {
		return fence{}, false
	}
	n := len(l) - len(strings.TrimLeft(l, string(l[0])))
	return fence{l[0], n}, n >= 3
}

// closes reports whether a line is a bare fence that ends f.
func (f fence) closes(line string) bool {
	g, ok := fenceStart(line)
	return ok && g.char == f.char && g.count >= f.count && strings.TrimSpace(line) == strings.Repeat(string(f.char), g.count)
}

// Parse reads a task file.
func Parse(md string) (Task, error) {
	var t Task
	var extra []string
	sections := map[string]string{}
	var name string
	var buf []string
	var open *fence
	titled := false
	var dup string

	flush := func() {
		if name == "" {
			return
		}
		body := strings.Trim(strings.Join(buf, "\n"), "\n")
		switch name {
		case "Description", "Goal", "Repo", "Check", "Type", "Protected", "After":
			if _, seen := sections[name]; seen && dup == "" {
				dup = name
			}
			sections[name] = body
		default:
			extra = append(extra, "## "+name+"\n\n"+body)
		}
	}

	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if open != nil {
			buf = append(buf, line)
			if open.closes(line) {
				open = nil
			}
			continue
		}
		if f, ok := fenceStart(line); ok {
			open = &f
			if !titled {
				return Task{}, &FormatError{Problem: "text before the title"}
			}
			buf = append(buf, line)
			continue
		}
		switch {
		case !titled && strings.HasPrefix(line, "# "):
			titled = true
			t.Title = strings.TrimSpace(line[2:])
		case !titled && strings.TrimSpace(line) != "":
			return Task{}, &FormatError{Problem: "text before the title"}
		case strings.HasPrefix(line, "## "):
			flush()
			name = strings.TrimSpace(line[3:])
			buf = nil
		default:
			buf = append(buf, line)
		}
	}
	flush()
	if dup != "" {
		return Task{}, &FormatError{Problem: "duplicate section", Section: dup}
	}

	if t.Title == "" {
		return Task{}, &MissingError{Section: "Title"}
	}
	for _, req := range []string{"Description", "Goal"} {
		if strings.TrimSpace(sections[req]) == "" {
			return Task{}, &MissingError{Section: req}
		}
	}
	t.Description = sections["Description"]
	t.Goal = sections["Goal"]
	var err error
	if t.Repo, err = oneLine("Repo", sections["Repo"]); err != nil {
		return Task{}, err
	}
	if t.Check, err = oneLine("Check", sections["Check"]); err != nil {
		return Task{}, err
	}
	typ, err := oneLine("Type", sections["Type"])
	if err != nil {
		return Task{}, err
	}
	if typ = strings.ToLower(typ); typ != "" {
		if !slices.Contains(Types, typ) {
			return Task{}, &FormatError{Problem: "invalid section", Section: "Type"}
		}
		t.Type = typ
	}
	for _, line := range strings.Split(sections["Protected"], "\n") {
		p := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if p = strings.TrimSpace(strings.Trim(p, "`")); p != "" {
			t.Protected = append(t.Protected, p)
		}
	}
	for _, id := range strings.FieldsFunc(sections["After"], func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if id != "-" {
			t.After = append(t.After, id)
		}
	}
	t.Extra = strings.Join(extra, "\n\n")
	return t, nil
}

// Number returns the number in a task id such as t12, or 0 for any other text.
func Number(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "t"))
	return n
}

// WaitingOn returns the ids in the task's After list whose task is not done, in list order.
// An id that names no task in all counts as not done. An archived done task counts as done.
func (t Task) WaitingOn(all []Task) []string {
	var waiting []string
	for _, id := range t.After {
		if !slices.ContainsFunc(all, func(o Task) bool { return o.ID == id && o.State == Done }) {
			waiting = append(waiting, id)
		}
	}
	return waiting
}

// Label returns the state for a list or a table. A queued task that waits on others shows them, as in "queued (after t6, t8)".
func (t Task) Label(all []Task) string {
	if waiting := t.WaitingOn(all); t.State == Queued && len(waiting) > 0 {
		return fmt.Sprintf("%s (after %s)", t.State, strings.Join(waiting, ", "))
	}
	return string(t.State)
}

// oneLine returns the single non-empty line of a section body, or empty for a blank body.
func oneLine(section, body string) (string, error) {
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	switch len(lines) {
	case 0:
		return "", nil
	case 1:
		return lines[0], nil
	}
	return "", &FormatError{Problem: "more than one line in", Section: section}
}

// WithFileFieldsOf returns a copy of t whose fields from the task file come from edited.
// Every state field of t stays, so a new state field survives an edit without a change here.
func (t Task) WithFileFieldsOf(edited Task) Task {
	t.Title, t.Description, t.Goal = edited.Title, edited.Description, edited.Goal
	t.Repo, t.Check, t.Protected = edited.Repo, edited.Check, edited.Protected
	t.After, t.Type, t.Extra = edited.After, edited.Type, edited.Extra
	return t
}

// Markdown writes the task file that Parse reads.
func (t Task) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n## Description\n\n%s\n\n## Goal\n\n%s\n", t.Title, t.Description, t.Goal)
	if t.Repo != "" {
		fmt.Fprintf(&b, "\n## Repo\n\n%s\n", t.Repo)
	}
	if t.Check != "" {
		fmt.Fprintf(&b, "\n## Check\n\n%s\n", t.Check)
	}
	if t.Type != "" {
		fmt.Fprintf(&b, "\n## Type\n\n%s\n", t.Type)
	}
	if len(t.Protected) > 0 {
		b.WriteString("\n## Protected\n\n")
		for _, p := range t.Protected {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}
	if len(t.After) > 0 {
		b.WriteString("\n## After\n\n")
		for _, id := range t.After {
			fmt.Fprintf(&b, "%s\n", id)
		}
	}
	if t.Extra != "" {
		fmt.Fprintf(&b, "\n%s\n", t.Extra)
	}
	return b.String()
}
