package task_test

import (
	"reflect"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestTransitionsSetTheirFields(t *testing.T) {
	tests := []struct {
		name   string
		before task.Task
		call   func(*task.Task)
		after  task.Task
	}{
		{"queue", task.Task{State: task.New}, (*task.Task).Queue, task.Task{State: task.Queued}},
		{"start work", task.Task{State: task.Queued, Pending: []string{"a", "b"}}, func(t *task.Task) { t.StartWork("/w") },
			task.Task{State: task.Working, Dir: "/w"}},
		{"requeue and forget the dir", task.Task{State: task.Working, Dir: "/w", Branch: "b", Lease: "l"},
			func(t *task.Task) { t.RequeueAfterFailedStart([]string{"a"}, true) },
			task.Task{State: task.Queued, Pending: []string{"a"}, Branch: "b", Lease: "l"}},
		{"requeue and keep the dir", task.Task{State: task.Working, Dir: "/w"},
			func(t *task.Task) { t.RequeueAfterFailedStart(nil, false) },
			task.Task{State: task.Queued, Dir: "/w"}},
		{"start review from working", task.Task{State: task.Working, Round: 1}, (*task.Task).StartReview, task.Task{State: task.Review, Round: 2}},
		{"start review from feedback", task.Task{State: task.Feedback}, (*task.Task).StartReview, task.Task{State: task.Review, Round: 1}},
		{"fail check", task.Task{State: task.Working}, (*task.Task).FailCheck, task.Task{State: task.Feedback, Round: 1}},
		{"fail review", task.Task{State: task.Review, Round: 1}, (*task.Task).FailReview, task.Task{State: task.Feedback, Round: 1}},
		{"await push", task.Task{State: task.Review}, (*task.Task).AwaitPush, task.Task{State: task.Review, Pushing: true}},
		{"hold", task.Task{State: task.Review, Pushing: true}, (*task.Task).PlaceOnHold, task.Task{State: task.Held}},
		{"release push", task.Task{State: task.Held}, (*task.Task).ReleasePush, task.Task{State: task.Review, Released: true, Pushing: true}},
		{"approve", task.Task{State: task.Review, Pushing: true, Released: true}, (*task.Task).Approve, task.Task{State: task.Approved, Released: true}},
		{"complete", task.Task{State: task.Approved}, (*task.Task).Complete, task.Task{State: task.Done}},
		{"archive", task.Task{State: task.Done}, (*task.Task).Archive, task.Task{State: task.Done, Archived: true}},
		{"cancel an escalated task", task.Task{State: task.Escalated, Before: task.Working}, (*task.Task).Cancel, task.Task{State: task.Cancelled}},
	}
	for _, from := range []task.State{task.New, task.Queued, task.Working, task.Review, task.Feedback} {
		tests = append(tests, struct {
			name   string
			before task.Task
			call   func(*task.Task)
			after  task.Task
		}{"escalate from " + string(from), task.Task{State: from}, (*task.Task).Escalate, task.Task{State: task.Escalated, Before: from}})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.before
			tt.call(&got)
			if !reflect.DeepEqual(got, tt.after) {
				t.Errorf("got %+v, want %+v", got, tt.after)
			}
		})
	}
}

func TestTakeFeedbackFollowsTheStateTable(t *testing.T) {
	reopened := task.Task{State: task.Queued, Reopened: true, Pending: []string{"fb"}}
	tests := []struct {
		name   string
		before task.Task
		effect task.FeedbackEffect
		after  task.Task
	}{
		{"new", task.Task{State: task.New}, task.FeedbackKept, task.Task{State: task.New, Pending: []string{"fb"}}},
		{"queued", task.Task{State: task.Queued, Pending: []string{"old"}}, task.FeedbackKept, task.Task{State: task.Queued, Pending: []string{"old", "fb"}}},
		{"working", task.Task{State: task.Working}, task.FeedbackToWorker, task.Task{State: task.Working}},
		{"feedback", task.Task{State: task.Feedback}, task.FeedbackToWorker, task.Task{State: task.Feedback}},
		{"review", task.Task{State: task.Review, Pushing: true}, task.FeedbackStopsReviewer, task.Task{State: task.Feedback}},
		{"review after release", task.Task{State: task.Review, Pushing: true, Released: true}, task.FeedbackEndsSessions, reopened},
		{"escalated", task.Task{State: task.Escalated, Before: task.Review}, task.FeedbackKept,
			task.Task{State: task.Escalated, Before: task.Review, Pending: []string{"fb"}}},
		{"held", task.Task{State: task.Held}, task.FeedbackKept, reopened},
		{"approved", task.Task{State: task.Approved, Released: true}, task.FeedbackKept, reopened},
		{"done", task.Task{State: task.Done, Archived: true}, task.FeedbackKept, reopened},
		{"cancelled", task.Task{State: task.Cancelled, Pushing: true, Released: true}, task.FeedbackKept, reopened},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.before
			if effect := got.TakeFeedback("fb"); effect != tt.effect {
				t.Errorf("effect %v, want %v", effect, tt.effect)
			}
			if !reflect.DeepEqual(got, tt.after) {
				t.Errorf("got %+v, want %+v", got, tt.after)
			}
		})
	}
}

func TestResumeReturnsTheEarlierState(t *testing.T) {
	pending := []string{"a"}
	tests := []struct {
		name        string
		before      task.Task
		wantPending []string
		after       task.Task
	}{
		{"queued", task.Task{State: task.Escalated, Before: task.Queued, Round: 2, Pending: pending}, nil,
			task.Task{State: task.Queued, Round: 2, Pending: pending}},
		{"working", task.Task{State: task.Escalated, Before: task.Working, Pending: pending}, pending, task.Task{State: task.Working}},
		{"review", task.Task{State: task.Escalated, Before: task.Review, Pushing: true, Round: 1}, nil,
			task.Task{State: task.Review, Pushing: true, Round: 1}},
		{"feedback", task.Task{State: task.Escalated, Before: task.Feedback, Round: 3, Pending: pending}, pending,
			task.Task{State: task.Feedback, Round: 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.before
			p, err := got.Resume()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(p, tt.wantPending) {
				t.Errorf("pending %v, want %v", p, tt.wantPending)
			}
			if !reflect.DeepEqual(got, tt.after) {
				t.Errorf("got %+v, want %+v", got, tt.after)
			}
		})
	}
}

func TestResumeWithoutAnEarlierStateFails(t *testing.T) {
	in := task.Task{ID: "t4", State: task.Escalated, Pending: []string{"a"}}
	got := in
	_, err := got.Resume()
	if err == nil || err.Error() != "task t4 has no recorded state to return to" {
		t.Fatalf("error %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("task changed: %+v", got)
	}
}

func TestStateActive(t *testing.T) {
	want := map[task.State]bool{task.Working: true, task.Review: true, task.Feedback: true}
	for _, s := range []task.State{task.New, task.Queued, task.Working, task.Review, task.Feedback, task.Held, task.Approved, task.Done, task.Escalated, task.Cancelled} {
		if got := s.Active(); got != want[s] {
			t.Errorf("%s: Active is %v", s, got)
		}
	}
}

// checkInvariants fails when tk breaks invariants 1 to 4 of the lifecycle methods. Invariant 4 needs the task before, so it is checked in the caller.
func checkInvariants(t *testing.T, tk task.Task) {
	t.Helper()
	if tk.Before != "" && tk.State != task.Escalated {
		t.Errorf("Before is %q in %s", tk.Before, tk.State)
	}
	if tk.Pushing && tk.State != task.Review && (tk.State != task.Escalated || tk.Before != task.Review) {
		t.Errorf("Pushing is set in %s with Before %q", tk.State, tk.Before)
	}
}

func TestTransitionsKeepTheInvariants(t *testing.T) {
	calls := map[string]func(*task.Task){
		"queue":    (*task.Task).Queue,
		"start":    func(t *task.Task) { t.StartWork("/w") },
		"requeue":  func(t *task.Task) { t.RequeueAfterFailedStart([]string{"x"}, true) },
		"review":   (*task.Task).StartReview,
		"check":    (*task.Task).FailCheck,
		"fail":     (*task.Task).FailReview,
		"await":    (*task.Task).AwaitPush,
		"hold":     (*task.Task).PlaceOnHold,
		"release":  (*task.Task).ReleasePush,
		"approve":  (*task.Task).Approve,
		"complete": (*task.Task).Complete,
		"cancel":   (*task.Task).Cancel,
		"archive":  (*task.Task).Archive,
		"escalate": (*task.Task).Escalate,
		"resume":   func(t *task.Task) { _, _ = t.Resume() },
		"feedback": func(t *task.Task) { t.TakeFeedback("fb") },
	}
	sources := map[string][]task.Task{
		"queue":    {{State: task.New}},
		"start":    {{State: task.Queued, Pending: []string{"a"}}},
		"requeue":  {{State: task.Working, Dir: "/w"}},
		"review":   {{State: task.Working}, {State: task.Feedback}},
		"check":    {{State: task.Working}, {State: task.Feedback}},
		"fail":     {{State: task.Review}},
		"await":    {{State: task.Review}},
		"hold":     {{State: task.Review, Pushing: true}},
		"release":  {{State: task.Held}},
		"approve":  {{State: task.Review, Pushing: true}},
		"complete": {{State: task.Approved}},
		"cancel":   {{State: task.New}, {State: task.Escalated, Before: task.Working}},
		"archive":  {{State: task.Done}, {State: task.Cancelled}},
		"escalate": {{State: task.New}, {State: task.Queued}, {State: task.Working}, {State: task.Review, Pushing: true}, {State: task.Feedback}},
		"resume":   {{State: task.Escalated, Before: task.Review, Pushing: true}, {State: task.Escalated, Before: task.Working, Pending: []string{"a"}}},
		"feedback": {{State: task.New}, {State: task.Queued}, {State: task.Working}, {State: task.Review, Pushing: true}, {State: task.Review, Pushing: true, Released: true, Reopened: true},
			{State: task.Feedback}, {State: task.Held, Hold: true}, {State: task.Approved}, {State: task.Done}, {State: task.Escalated, Before: task.Review, Pushing: true}, {State: task.Cancelled}},
	}
	for name, call := range calls {
		for _, from := range sources[name] {
			t.Run(name+" from "+string(from.State), func(t *testing.T) {
				got := from
				call(&got)
				checkInvariants(t, got)
				if from.Reopened && !got.Reopened {
					t.Error("Reopened was cleared")
				}
				if len(got.Pending) < len(from.Pending) && name != "start" && name != "resume" {
					t.Errorf("Pending shrank from %v to %v", from.Pending, got.Pending)
				}
			})
		}
	}
}
