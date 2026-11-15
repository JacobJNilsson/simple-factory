// Package task defines a task, its states and its file format.
package task

import (
	"fmt"
	"strings"
	"time"
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
	Protected   []string
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
	// PushAfterReview is true after feedback on a task that did work. Its worker pushes only after a review passes.
	PushAfterReview bool
	// Pushing is true in review after a passed review of such a task, until the worker reports that it pushed.
	Pushing bool
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
	// Problem is "duplicate section" or "text before the title".
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
		case "Description", "Goal", "Repo", "Check", "Protected":
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
	t.Repo = firstLine(sections["Repo"])
	t.Check = firstLine(sections["Check"])
	for _, line := range strings.Split(sections["Protected"], "\n") {
		if p := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- ")); p != "" {
			t.Protected = append(t.Protected, p)
		}
	}
	t.Extra = strings.Join(extra, "\n\n")
	return t, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
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
	if len(t.Protected) > 0 {
		b.WriteString("\n## Protected\n\n")
		for _, p := range t.Protected {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}
	if t.Extra != "" {
		fmt.Fprintf(&b, "\n%s\n", t.Extra)
	}
	return b.String()
}
