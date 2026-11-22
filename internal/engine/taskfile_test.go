package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func (v *env) taskFile() string { return filepath.Join(v.home, "tasks", "t1", "task.md") }

func TestWorkerAndReviewerStartWithTheirTaskFile(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)

	v.tick(t)
	mustOK(t, v.run("worker-t1", "t1", "report done", "", "all", "done"))

	if len(v.l.started) != 2 {
		t.Fatalf("started %+v", v.l.started)
	}
	for _, s := range v.l.started {
		if s.TaskFile != v.taskFile() {
			t.Errorf("%s TaskFile = %q, want %q", s.Name, s.TaskFile, v.taskFile())
		}
	}
}

func TestThePushWorkerStartsWithTheTaskFile(t *testing.T) {
	v := newEnv(t)
	v.ws.remoteSHA = "23da1c9"
	v.seedExisting(t, true)
	v.tick(t)
	mustOK(t, v.run("worker-t1", "t1", "report done", "Rebased."))
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))

	mustOK(t, v.run("lead", "", "task push", "", "t1"))

	last := v.l.started[len(v.l.started)-1]
	if last.Kind != "worker" || last.TaskFile != v.taskFile() {
		t.Fatalf("started %+v", last)
	}
}
