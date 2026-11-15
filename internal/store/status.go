package store

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JacobJNilsson/factory/internal/task"
)

// RenderStatus returns the text of status.md. Escalated tasks come first, then the rest by id.
func RenderStatus(tasks []task.Task) string {
	sorted := append([]task.Task(nil), tasks...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ei, ej := sorted[i].State == task.Escalated, sorted[j].State == task.Escalated
		if ei != ej {
			return ei
		}
		return number(sorted[i].ID) < number(sorted[j].ID)
	})

	var b strings.Builder
	b.WriteString("# Factory status\n\n| Id | Title | State | Round | Agents | Last change |\n|---|---|---|---|---|---|\n")
	for _, t := range sorted {
		changed := "-"
		if !t.Updated.IsZero() {
			changed = t.Updated.UTC().Format(time.RFC3339)
		}
		title := strings.ReplaceAll(t.Title, "|", `\|`)
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %s | %s |\n", t.ID, title, t.State, t.Round, agents(t), changed)
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
