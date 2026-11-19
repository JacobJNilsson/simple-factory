package task

import "fmt"

// Active reports whether a task in this state holds a worker slot.
//
// The transition methods below keep these invariants:
//  1. Before is not empty only in escalated.
//  2. Pushing holds only in review, or in escalated with Before set to review.
//  3. No transition clears Reopened.
//  4. A transition that starts or resumes a worker empties Pending. Every other transition keeps or extends it.
//  5. Round grows only in StartReview and FailCheck.
//
// The methods do no I/O and do not check the source state. The engine guards each command before it calls one.
func (s State) Active() bool {
	return s == Working || s == Review || s == Feedback
}

// Queue moves a new task to queued.
func (t *Task) Queue() { t.State = Queued }

// StartWork moves a queued task to working in the workspace dir. The new worker reads the pending feedback, so it empties.
func (t *Task) StartWork(dir string) {
	t.State, t.Dir, t.Pending = Working, dir, nil
}

// RequeueAfterFailedStart moves a task whose worker did not start back to queued, with the feedback that the worker did not read.
// It also forgets the workspace when removed is true. It keeps Branch and Lease.
func (t *Task) RequeueAfterFailedStart(pending []string, removed bool) {
	t.State, t.Pending = Queued, pending
	if removed {
		t.Dir = ""
	}
}

// StartReview moves a working or feedback task to review and counts the round.
func (t *Task) StartReview() {
	t.State = Review
	t.Round++
}

// FailCheck moves a working or feedback task to feedback after a failed check, and counts the failure as a round.
func (t *Task) FailCheck() {
	t.Round++
	t.State = Feedback
}

// FailReview moves a task in review to feedback after a failed verdict. The round stays.
func (t *Task) FailReview() { t.State = Feedback }

// AwaitPush marks a task in review as waiting for its worker to push.
func (t *Task) AwaitPush() { t.Pushing = true }

// PlaceOnHold moves a task in review to held, since the push waits for the operator.
func (t *Task) PlaceOnHold() {
	t.State, t.Pushing = Held, false
}

// ReleasePush moves a held task back to review, with the push flag and the release set.
func (t *Task) ReleasePush() {
	t.State, t.Released, t.Pushing = Review, true, true
}

// Approve moves a task in review to approved. A task that waited for a push no longer waits.
func (t *Task) Approve() {
	t.State, t.Pushing = Approved, false
}

// Complete moves an approved task to done.
func (t *Task) Complete() { t.State = Done }

// Cancel moves any task that is not done or cancelled to cancelled.
func (t *Task) Cancel() {
	t.State, t.Before, t.Pushing, t.Released = Cancelled, "", false, false
}

// Archive hides a done or cancelled task from the default views.
func (t *Task) Archive() { t.Archived = true }

// Escalate moves a task to escalated and records the state that it left.
func (t *Task) Escalate() {
	t.Before, t.State = t.State, Escalated
}

// Resume returns an escalated task to the state it had before.
// It returns the feedback that the new worker must read, and empties it, when that state is active.
// Otherwise it returns nil and keeps the feedback. A task with no recorded state fails and stays as it is.
func (t *Task) Resume() ([]string, error) {
	if t.Before == "" {
		return nil, fmt.Errorf("task %s has no recorded state to return to", t.ID)
	}
	t.State, t.Before = t.Before, ""
	if !t.State.Active() {
		return nil, nil
	}
	pending := t.Pending
	t.Pending = nil
	return pending, nil
}

// FeedbackEffect tells the engine what to do after TakeFeedback changed a task.
type FeedbackEffect int

// The effects of TakeFeedback.
const (
	// FeedbackToWorker means the running worker gets the feedback as a message. The task did not change.
	FeedbackToWorker FeedbackEffect = iota
	// FeedbackStopsReviewer means the review ends without a verdict and the task moved to feedback.
	FeedbackStopsReviewer
	// FeedbackEndsSessions means the sessions end because the worker may push. The task moved to queued.
	FeedbackEndsSessions
	// FeedbackKept means no session changes. The task keeps the feedback for its next worker.
	FeedbackKept
)

// TakeFeedback applies the feedback table of the spec for the state of the task and returns what the engine must do.
// A task in new, queued or escalated keeps the state. A task in held, approved, done or cancelled reopens as queued.
func (t *Task) TakeFeedback(body string) FeedbackEffect {
	switch t.State {
	case Working, Feedback:
		return FeedbackToWorker
	case Review:
		if t.Released {
			t.State, t.Pushing, t.Released, t.Reopened = Queued, false, false, true
			t.Pending = append(t.Pending, body)
			return FeedbackEndsSessions
		}
		t.State, t.Pushing = Feedback, false
		return FeedbackStopsReviewer
	case Held, Approved, Done, Cancelled:
		t.State, t.Archived, t.Reopened, t.Pushing, t.Released = Queued, false, true, false, false
	}
	t.Pending = append(t.Pending, body)
	return FeedbackKept
}
