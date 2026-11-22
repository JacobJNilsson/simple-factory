package task

import "github.com/JacobJNilsson/simple-factory/internal/process"

// Position returns the place of the task in the default process, read from its state fields.
// state.json keeps today's fields, so a file from an older sfy loads and an older sfy reads every task.
// A shell agent never shows here, because a restart stops it and the task stays at the agent before it.
// A task in new, queued, done or cancelled is in no list, and gets the zero position.
func (t Task) Position() process.Position {
	state := t.State
	if state == Escalated {
		state = t.Before
	}
	switch state {
	case Working, Feedback:
		return process.Position{List: "build", Agent: "worker"}
	case Review:
		switch {
		case t.Pushing && t.Released:
			return process.Position{List: "held", Agent: "push"}
		case t.Pushing:
			return process.Position{List: "ship", Agent: "push"}
		}
		return process.Position{List: "build", Agent: "reviewer"}
	case Held:
		return process.Position{List: "held", Agent: "release"}
	case Approved:
		return process.Position{List: "land", Agent: "integrate"}
	}
	return process.Position{}
}

// Open reports whether the task is in a list, so the process must keep its position.
func (t Task) Open() bool { return t.Position() != process.Position{} }
