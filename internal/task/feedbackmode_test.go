package task_test

import (
	"reflect"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// defaultMode is the feedback mode of the agent of the default process that each state is at.
var defaultMode = map[task.State]task.FeedbackMode{
	task.Working: task.ModeMessage, task.Feedback: task.ModeMessage, task.Review: task.ModeStop,
	task.Held: task.ModeReopen, task.Approved: task.ModeReopen,
}

func TestTakeFeedbackAsTheDefaultAgentsAskMatchesTheStateTable(t *testing.T) {
	for _, before := range []task.Task{
		{State: task.New}, {State: task.Queued, Pending: []string{"old"}}, {State: task.Working}, {State: task.Feedback},
		{State: task.Review, Pushing: true}, {State: task.Review, Pushing: true, Released: true},
		{State: task.Escalated, Before: task.Review}, {State: task.Held}, {State: task.Approved, Released: true},
		{State: task.Done, Archived: true}, {State: task.Cancelled, Pushing: true, Released: true},
	} {
		want, got := before, before
		wantEffect := want.TakeFeedback("fb")
		if effect := got.TakeFeedbackAs("fb", defaultMode[before.State]); effect != wantEffect || !reflect.DeepEqual(got, want) {
			t.Errorf("%+v: got %v %+v, want %v %+v", before, effect, got, wantEffect, want)
		}
	}
}

func TestTakeFeedbackAsReopenEndsTheSessionsOfAnActiveTask(t *testing.T) {
	got := task.Task{State: task.Working}

	if effect := got.TakeFeedbackAs("fb", task.ModeReopen); effect != task.FeedbackEndsSessions || got.State != task.Queued || !got.Reopened {
		t.Fatalf("got %v %+v, want queued with the sessions ended", effect, got)
	}
}
