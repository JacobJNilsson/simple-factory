package task_test

import (
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestPositionReadsTodaysStateFields(t *testing.T) {
	cases := []struct {
		t    task.Task
		want string
	}{
		{task.Task{State: task.Working}, "build/worker"},
		{task.Task{State: task.Feedback}, "build/worker"},
		{task.Task{State: task.Review}, "build/reviewer"},
		{task.Task{State: task.Review, Pushing: true}, "ship/push"},
		{task.Task{State: task.Review, Pushing: true, Released: true}, "held/push"},
		{task.Task{State: task.Held}, "held/release"},
		{task.Task{State: task.Approved}, "land/integrate"},
		{task.Task{State: task.Escalated, Before: task.Held}, "held/release"},
	}
	p := process.Default()
	for _, c := range cases {
		at := c.t.Position()
		if at.String() != c.want {
			t.Errorf("%+v is at %s, want %s", c.t, at, c.want)
		}
		if err := p.Validate(map[string]process.Position{"t1": at}); err != nil {
			t.Errorf("the default lacks %s: %v", at, err)
		}
	}
	for _, s := range []task.State{task.New, task.Queued, task.Done, task.Cancelled} {
		if (task.Task{State: s}).Open() {
			t.Errorf("a %s task is open", s)
		}
	}
}
