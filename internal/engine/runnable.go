package engine

import (
	"errors"
	"maps"
	"slices"

	"github.com/JacobJNilsson/simple-factory/internal/process"
)

// Runnable refuses a valid process that this engine cannot run. Each error is a process.KeyError that names the key.
// Every rule below says why the engine cannot follow the key, so a rule goes once the engine can.
func Runnable(p process.Process) error {
	var errs []error
	refuse := func(key, msg string) { errs = append(errs, &process.KeyError{Key: key, Msg: msg}) }
	if err := startable(p, p.Start); err != nil {
		// A queued task holds no position, and sfy makes its workspace for the first session of its start list.
		refuse("start", err.Error())
	}
	for _, name := range slices.Sorted(maps.Keys(p.Agents)) {
		a := p.Agents[name]
		// The engine counts a slot by the task's state, and the state follows the harness.
		if a.Slot != (a.Harness == process.Claude || a.Harness == process.Shell) {
			refuse("agents."+name+".slot", "this sfy counts a worker slot for each claude and shell agent and for no lead or operator agent")
		}
		if a.Harness == process.Shell {
			// sfy saves no position for a shell agent, so a task stays at the agent before it and feedback follows that agent.
			if a.Feedback != process.FeedbackMessage {
				refuse("agents."+name+".feedback", "feedback during a shell agent follows the feedback key of the agent before it")
			}
			// A command reads the results through the {result} placeholder, which names the last result only.
			if a.Receives != process.ReceivesPrevious {
				refuse("agents."+name+".receives", "a shell agent reads only the result before it, through {result}")
			}
		}
		if err := feedbackRunnable(p, a); err != nil {
			refuse("agents."+name+".feedback", err.Error())
		}
		if a.Harness == process.Claude && a.Like == "" {
			errs = append(errs, sessionRunnable(p, a)...)
		}
	}
	return errors.Join(errs...)
}

// startable refuses a list whose first agent is not a claude agent that ends at done.
// The first session of a task makes its work, and its state says working only at such an agent.
func startable(p process.Process, list string) error {
	first, err := p.First(list)
	if err != nil {
		return err
	}
	if a := p.Agents[first.Agent]; a.Harness != process.Claude || a.Ends != process.EndsDone {
		return errors.New("a task starts in list " + list + ", whose first agent " + first.Agent + " is no claude agent that ends at done")
	}
	return nil
}

// feedbackRunnable refuses a feedback list that a task cannot go back to.
// Feedback that names a list puts the task in feedback, which the engine reads as the work of a claude agent that ends at done.
func feedbackRunnable(p process.Process, a process.Agent) error {
	if a.Feedback == process.FeedbackMessage || a.Feedback == process.FeedbackReopen {
		return nil
	}
	return startable(p, a.Feedback)
}

// sessionRunnable refuses a session whose agents need two kinds of session.
// A session has one kind, and the kind decides its sfy commands and its permission rules.
func sessionRunnable(p process.Process, owner process.Agent) []error {
	var errs []error
	verdict := owner.Ends == process.EndsVerdict
	for _, name := range slices.Sorted(maps.Keys(p.Agents)) {
		a := p.Agents[name]
		if a.Like == owner.Name && a.Harness == process.Claude && (a.Ends == process.EndsVerdict) != verdict {
			errs = append(errs, &process.KeyError{Key: "agents." + name + ".like", Msg: "a session runs agents that end at a verdict or agents that do not, not both"})
		}
	}
	// A reviewer session gets the sfy commands of a verdict and no push rule.
	if verdict && owner.Permissions.Push {
		errs = append(errs, &process.KeyError{Key: "agents." + owner.Name + ".permissions.push", Msg: "a session that ends at a verdict gets no push rules"})
	}
	return errs
}
