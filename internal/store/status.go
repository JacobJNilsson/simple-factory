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
	b.WriteString("# sfy status\n\n| Id | Title | State | Round | Agents | Last change |\n|---|---|---|---|---|---|\n")
	for _, t := range sorted {
		changed := "-"
		if !t.Updated.IsZero() {
			changed = t.Updated.UTC().Format(time.RFC3339)
		}
		title := strings.ReplaceAll(t.Title, "|", `\|`)
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %s | %s |\n", t.ID, title, t.Label(tasks), t.Round, agents(t), changed)
	}
	return b.String()
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
