package engine

import (
	"errors"
	"fmt"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// liveness is the answer of one alive check.
type liveness int

const (
	sessionRuns liveness = iota
	sessionGone
	// sessionUnknown means that the check failed. sfy never reads it as gone.
	sessionUnknown
)

// errAliveUnknown marks a failure that follows from an alive check that could not tell whether a session runs.
var errAliveUnknown = errors.New("cannot tell whether the session runs")

// aliveErrTicks is how many ticks in a row an alive check may fail before sfy tells main.
const aliveErrTicks = 3

// probeState is what the counted alive checks of one session know. Only sessions.probe and sessions.watch touch it.
type probeState struct {
	// goneTold is true after sfy told main that the session no longer runs.
	goneTold bool
	// errs counts the alive checks in a row that failed. A check that answers resets it.
	errs int
	// lastErr is the error of the latest failed alive check.
	lastErr error
	// errTold is true after sfy told main that it cannot tell whether the session runs.
	errTold bool
}

// agentState tracks the report rule and the liveness of a session. The delivery module holds the note state.
type agentState struct {
	// reported is true when the agent ran report or verdict in its current turn.
	reported bool
	// reminders counts the turns in a row that ended without a report.
	reminders int
	// probe holds what the counted alive checks know.
	probe probeState
}

// sessionEvent is what a counted alive check tells that main has not heard yet.
type sessionEvent int

const (
	nothingNew sessionEvent = iota
	becameGone
	cannotTell
)

// sessions starts, stops and checks agent sessions, and keeps what sfy knows of each one in memory.
// It is the only code that calls the launcher to start, stop or check a session.
// It takes no lock, because the engine serializes every call.
type sessions struct {
	l        Launcher
	cfg      config.Config
	taskFile func(id string) (string, error)
	branchOf func(task.Task) string
	logf     func(format string, args ...any)
	// forgetNotes drops the delivery state of a session.
	forgetNotes func(name string)
	agents      map[string]*agentState
}

func (s *sessions) agent(name string) *agentState {
	if s.agents[name] == nil {
		s.agents[name] = &agentState{}
	}
	return s.agents[name]
}

// forget drops what sfy knows of a session that ended or starts again, and its delivery state.
func (s *sessions) forget(name string) {
	delete(s.agents, name)
	s.forgetNotes(name)
}

// alive runs one alive check. The error is set only for sessionUnknown.
func (s *sessions) alive(name string) (liveness, error) {
	alive, err := s.l.Alive(name)
	switch {
	case err != nil:
		return sessionUnknown, err
	case alive:
		return sessionRuns, nil
	}
	return sessionGone, nil
}

// isGone reports whether the check answered gone. A check that failed is never gone.
func (s *sessions) isGone(name string) bool {
	l, _ := s.alive(name)
	return l == sessionGone
}

// probe runs the alive check of a session and counts its failures. It logs the first failure of a run.
func (s *sessions) probe(name string) liveness {
	st := &s.agent(name).probe
	l, err := s.alive(name)
	if l != sessionUnknown {
		st.errs, st.lastErr, st.errTold = 0, nil, false
		return l
	}
	st.errs++
	st.lastErr = err
	if st.errs == 1 {
		s.logf("alive check of %s failed: %v", name, err)
	}
	return l
}

// probeGone is probe for a caller that needs to know only whether the session is gone.
func (s *sessions) probeGone(name string) bool { return s.probe(name) == sessionGone }

// watch probes a session and reports a change that main has not heard of.
// A session that is gone is told once, until it runs again. A failed check is told once after aliveErrTicks in a row, and then err holds its error.
func (s *sessions) watch(name string) (ev sessionEvent, err error) {
	l := s.probe(name)
	st := &s.agent(name).probe
	switch l {
	case sessionUnknown:
		if st.errs >= aliveErrTicks && !st.errTold {
			st.errTold = true
			return cannotTell, st.lastErr
		}
	case sessionRuns:
		st.goneTold = false
	case sessionGone:
		if !st.goneTold {
			st.goneTold = true
			return becameGone, nil
		}
	}
	return nothingNew, nil
}

// workerOf returns the session name of the worker of a task.
func workerOf(t task.Task) string { return "worker-" + t.ID }

func (s *sessions) workerSpec(t task.Task, prompt string) SessionSpec {
	return SessionSpec{
		Name: workerOf(t), Kind: "worker", Task: t.ID, Dir: t.Dir,
		Model: t.WorkerModel(s.cfg.Models.Worker), Prompt: prompt, Protected: t.Protected, HasRepo: t.Repo != "",
		Branch: s.branchOf(t), Lease: t.Lease, NoPush: t.HoldsPush(),
	}
}

func (s *sessions) reviewerSpec(t task.Task, prompt string) SessionSpec {
	return SessionSpec{
		Name: reviewerOf(t), Kind: "reviewer", Task: t.ID, Dir: t.Dir,
		Model: t.ReviewerModelOr(s.cfg.Models.Reviewer), Prompt: prompt, Protected: t.Protected,
	}
}

// start starts the worker or the reviewer of a task and resets what sfy knows of the session.
// It changes no state when it fails, so the caller keeps its own rollback.
func (s *sessions) start(t task.Task, k kind, prompt string) error {
	var spec SessionSpec
	switch k {
	case worker:
		spec = s.workerSpec(t, prompt)
	case reviewer:
		spec = s.reviewerSpec(t, prompt)
	default:
		return fmt.Errorf("cannot start a session of kind %q", k)
	}
	file, err := s.taskFile(t.ID)
	if err != nil {
		return err
	}
	spec.TaskFile = file
	if err := s.l.Start(spec); err != nil {
		return err
	}
	s.forget(spec.Name)
	return nil
}

// stop ends a session. A failure counts only while the session still runs, because a session may already be gone.
func (s *sessions) stop(name string) error {
	err := s.l.Stop(name)
	if err == nil {
		return nil
	}
	l, aerr := s.alive(name)
	switch l {
	case sessionUnknown:
		return errors.Join(err, fmt.Errorf("%w of %s: %w", errAliveUnknown, name, aerr))
	case sessionGone:
		return nil
	}
	return err
}

// end stops a session and forgets its state when the stop worked. It is the one place that forgets after a stop.
func (s *sessions) end(name string) error {
	if err := s.stop(name); err != nil {
		return err
	}
	s.forget(name)
	return nil
}

// checkWorkers tells main once when a worker session is gone, and again only after the worker runs again.
// sfy does not restart a worker that died. The main agent decides what to do.
func (e *Engine) checkWorkers() error {
	tasks, err := e.st.Tasks()
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tasks {
		if !t.State.Active() {
			continue
		}
		name := workerOf(t)
		ev, werr := e.sess.watch(name)
		switch ev {
		case cannotTell:
			errs = append(errs, e.message("lead", "sfy", fmt.Sprintf("sfy cannot tell whether %s runs: %v", name, werr)))
		case becameGone:
			errs = append(errs, e.message("lead", "sfy", fmt.Sprintf("The worker of task %s is gone. Its session no longer runs.", t.ID)))
		}
	}
	return errors.Join(errs...)
}
