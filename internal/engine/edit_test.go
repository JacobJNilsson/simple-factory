package engine_test

import (
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestEditKeepsTheStateThatResumeReturnsTo(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 0)
	mustOK(t, v.run("lead", "", "escalate", "", "t1", "stuck"))

	mustOK(t, v.run("lead", "", "task edit", taskFile, "t1"))
	mustOK(t, v.run("", "", "task resume", "", "t1"))

	if tk := v.state(t, "t1"); tk.State != task.Working || tk.Before != "" {
		t.Fatalf("got %+v", tk)
	}
}
