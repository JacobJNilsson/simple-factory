package store

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// RenderStatus returns the text of status.md. It leaves out archived tasks. Escalated tasks come first, then the rest. Each group lists the newest change first, a zero time last, and the higher id first on a tie.
func RenderStatus(tasks []task.Task) string {
	return RenderStatusLabeled(tasks, func(t task.Task) string { return t.Label(tasks) })
}

// RenderStatusLabeled is RenderStatus with the state column from label, so a running shell agent can show its own label.
func RenderStatusLabeled(tasks []task.Task, label func(task.Task) string) string {
	var sorted []task.Task
	for _, t := range tasks {
		if !t.Archived {
			sorted = append(sorted, t)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		ei, ej := sorted[i].State == task.Escalated, sorted[j].State == task.Escalated
		if ei != ej {
			return ei
		}
		ui, uj := sorted[i].Updated, sorted[j].Updated
		if zi, zj := ui.IsZero(), uj.IsZero(); zi != zj {
			return zj
		}
		if !ui.Equal(uj) {
			return ui.After(uj)
		}
		return number(sorted[i].ID) > number(sorted[j].ID)
	})

	var b strings.Builder
	fmt.Fprintf(&b, "# sfy status\n\n| Id | Title | State | Round | Agents | Last change (%s) |\n|---|---|---|---|---|---|\n", zoneName())
	for _, t := range sorted {
		changed := "-"
		if !t.Updated.IsZero() {
			changed = t.Updated.In(time.Local).Format("2006-01-02 15:04")
		}
		title := strings.ReplaceAll(t.ShownTitle(), "|", `\|`)
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %s | %s |\n", t.ID, title, label(t), t.Round, agents(t)+models(t), changed)
	}
	return b.String()
}

// zoneName returns the abbreviation of the local time zone as it applies now.
func zoneName() string {
	name, _ := time.Now().In(time.Local).Zone()
	return name
}

// models names the models that the task sets, and is empty when it sets none.
func models(t task.Task) string {
	var parts []string
	if t.Model != "" {
		parts = append(parts, "worker model "+t.Model)
	}
	if t.ReviewerModel != "" {
		parts = append(parts, "reviewer model "+t.ReviewerModel)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// agents names the sessions that the spec's session names give a task in its state.
func agents(t task.Task) string {
	switch t.State {
	case task.Working, task.Feedback:
		return "worker-" + t.ID
	case task.Review:
		return fmt.Sprintf("worker-%s, review-%s-%d", t.ID, t.ID, t.Round)
	}
	return "-"
}
