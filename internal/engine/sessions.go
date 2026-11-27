package engine

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/process"
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

// aliveErrTicks is how many ticks in a row an alive check may fail before sfy tells the lead.
const aliveErrTicks = 3

// probeState is what the counted alive checks of one session know. Only sessions.probe and sessions.watch touch it.
type probeState struct {
	// goneTold is true after sfy told the lead that the session no longer runs.
	goneTold bool
	// errs counts the alive checks in a row that failed. A check that answers resets it.
	errs int
	// lastErr is the error of the latest failed alive check.
	lastErr error
	// errTold is true after sfy told the lead that it cannot tell whether the session runs.
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
	// blockedAt is when the worker reported blocked. It is zero when the worker did not, or when it reported again or got an answer since.
	blockedAt time.Time
}

// sessionEvent is what a counted alive check tells that the lead has not heard yet.
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
	proc     process.Process
	taskFile func(id string) (string, error)
	branchOf func(task.Task) string
	logf     func(format string, args ...any)
	// forgetNotes drops the delivery state of a session.
	forgetNotes func(name string)
	agents      map[string]*agentState
	// ended holds the tasks whose sessions all ended while the task is approved. It lives in memory only.
	ended map[string]bool
	// gone holds the sessions that a stop or an alive check found gone since they last ran. It lives in memory only.
	gone map[string]bool
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

// alive runs one alive check and records whether the session is gone. The error is set only for sessionUnknown.
func (s *sessions) alive(name string) (liveness, error) {
	alive, err := s.l.Alive(name)
	switch {
	case err != nil:
		return sessionUnknown, err
	case alive:
		delete(s.gone, name)
		return sessionRuns, nil
	}
	s.gone[name] = true
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

// watch probes a session and reports a change that the lead has not heard of.
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

// startList returns the name of the list that a task starts in: its List section, or the start list of the process.
func (s *sessions) startList(t task.Task) string { return cmp.Or(t.List, s.proc.Start) }

// nameOf returns the session name of a claude agent for a task.
// A task session is <prefix>-<id>, and a round session is <prefix>-<id>-<round>. An agent with like has the session of the agent it copies.
func (s *sessions) nameOf(t task.Task, agent string) string {
	o := s.proc.Owner(agent)
	if o.Session == process.SessionRound {
		return roundSession(o.SessionName, t.ID, t.Round)
	}
	return o.SessionName + "-" + t.ID
}

// kindOfOwner returns the kind of the session that the agent owner runs: reviewer when an agent in that session ends at a verdict.
func (s *sessions) kindOfOwner(owner string) kind {
	for _, a := range s.proc.Agents {
		if a.Harness == process.Claude && s.proc.Owner(a.Name).Name == owner && a.Ends == process.EndsVerdict {
			return reviewer
		}
	}
	return worker
}

// sessionAgent returns the claude agent at the task's position whose kind matches, else the nearest such agent before it in its list,
// else the first such agent of the task's start list. verdict picks the agents that end at a verdict, and otherwise the agents that end at done or pushed.
func (s *sessions) sessionAgent(t task.Task, verdict bool) string {
	matches := func(name string) bool {
		a := s.proc.Agents[name]
		return a.Harness == process.Claude && (a.Ends == process.EndsVerdict) == verdict
	}
	at := t.Position()
	if agents, err := s.proc.Flatten(at.List); err == nil {
		for i := slices.Index(agents, at.Agent); i >= 0; i-- {
			if matches(agents[i]) {
				return agents[i]
			}
		}
	}
	agents, _ := s.proc.Flatten(s.startList(t))
	for _, name := range agents {
		if matches(name) {
			return name
		}
	}
	return ""
}

// workerOf returns the session name of the worker of a task: the session of its nearest claude agent that ends at done or pushed.
func (s *sessions) workerOf(t task.Task) string { return s.nameOf(t, s.sessionAgent(t, false)) }

// reviewerOf returns the session name of the reviewer of the task's round: the session of its nearest claude agent that ends at a verdict.
func (s *sessions) reviewerOf(t task.Task) string { return s.nameOf(t, s.sessionAgent(t, true)) }

// isStartAgent reports whether the agent owner runs the first claude agent that ends at done in the task's start list.
// The Model section of a task sets the model of that agent.
func (s *sessions) isStartAgent(t task.Task, owner string) bool {
	agents, _ := s.proc.Flatten(s.startList(t))
	for _, name := range agents {
		if a := s.proc.Agents[name]; a.Harness == process.Claude && a.Ends == process.EndsDone {
			return s.proc.Owner(name).Name == owner
		}
	}
	return false
}

// specFor returns the spec of the session of a claude agent for a task. An agent with like gets the spec of the agent it copies.
func (s *sessions) specFor(t task.Task, agent, prompt string) SessionSpec {
	o := s.proc.Owner(agent)
	perm := o.Permissions
	spec := SessionSpec{
		Name: s.nameOf(t, agent), Kind: string(s.kindOfOwner(o.Name)), Harness: o.Harness, Task: t.ID, Dir: t.Dir,
		Prompt: prompt, Protected: t.Protected, Agent: o.Name, Role: o.Prompt, RepoRole: o.RepoPrompt,
		ReadOnly: !perm.Edit, Unsandboxed: !perm.Sandbox, AllowWrite: perm.AllowWrite, DenyRead: perm.DenyRead, PassEnv: perm.PassEnv,
	}
	if spec.Kind == string(reviewer) {
		spec.Model = t.ReviewerModelOr(cmp.Or(o.Model, s.cfg.ModelOf(o.Name, true)))
		spec.HasRepo = t.Repo != "" && o.RepoPrompt != ""
		return spec
	}
	spec.Model = cmp.Or(o.Model, s.cfg.ModelOf(o.Name, false))
	if s.isStartAgent(t, o.Name) {
		spec.Model = t.WorkerModel(spec.Model)
	}
	spec.HasRepo, spec.Branch, spec.Lease, spec.NoPush = t.Repo != "", s.branchOf(t), t.Lease, t.HoldsPush() || !perm.Push
	return spec
}

// start starts the session of a claude agent for a task and resets what sfy knows of the session.
// It changes no state when it fails, except that it forgets that the task's sessions ended and that the session was gone.
func (s *sessions) start(t task.Task, agent, prompt string) error {
	if a := s.proc.Agents[agent]; a.Harness != process.Claude {
		return fmt.Errorf("cannot start a session for the %s agent %s", a.Harness, agent)
	}
	return s.launch(t, s.specFor(t, agent, prompt))
}

// resume continues the recorded conversation of the session of a claude agent for a task, with note as its first message.
// It stops what remains of the session first, because the start template fails on a name that a terminal session still holds.
func (s *sessions) resume(t task.Task, agent, note string) error {
	if a := s.proc.Agents[agent]; a.Harness != process.Claude {
		return fmt.Errorf("cannot resume a session for the %s agent %s", a.Harness, agent)
	}
	spec := s.specFor(t, agent, note)
	spec.Resume, spec.ResumePrompt = true, note
	if err := s.stop(spec.Name); err != nil {
		return err
	}
	return s.launch(t, spec)
}

// launch starts a session from its spec and the task's task file, and resets what sfy knows of the session.
func (s *sessions) launch(t task.Task, spec SessionSpec) error {
	file, err := s.taskFile(t.ID)
	if err != nil {
		return err
	}
	spec.TaskFile = file
	// A start that fails may still leave a session, so the clear comes first.
	delete(s.ended, t.ID)
	delete(s.gone, spec.Name)
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

// end stops a session and forgets its state when the stop worked. A session that sfy found gone needs no stop.
func (s *sessions) end(name string) error {
	if s.gone[name] {
		s.forget(name)
		return nil
	}
	if err := s.stop(name); err != nil {
		return err
	}
	s.forget(name)
	return nil
}

// checkWorkers tells the lead once when a worker or a reviewer session is gone, and again only after it runs again.
// sfy does not start a new session for one that died. The lead decides what to do, and a message may wake the session.
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
		errs = append(errs, e.watchSession(t, e.workerOf(t), "worker"))
		if e.waitsForVerdict(t) {
			errs = append(errs, e.watchSession(t, e.reviewerOf(t), "reviewer"))
		}
	}
	return errors.Join(errs...)
}

// waitsForVerdict reports whether a task in review waits for the verdict of a running reviewer session.
// A task that waits for a push, runs a check, or sits at a shell agent has no such session.
func (e *Engine) waitsForVerdict(t task.Task) bool {
	if t.State != task.Review || t.Pushing {
		return false
	}
	if _, running := e.checking[t.ID]; running {
		return false
	}
	a := e.proc.Agents[t.Position().Agent]
	return a.Harness == process.Claude && a.Ends == process.EndsVerdict
}

// watchSession runs the alive check of a session and tells the lead what is new. The role is "worker" or "reviewer".
func (e *Engine) watchSession(t task.Task, name, role string) error {
	ev, err := e.sess.watch(name)
	switch ev {
	case cannotTell:
		return e.message("lead", "sfy", fmt.Sprintf("sfy cannot tell whether %s runs: %v", name, err))
	case becameGone:
		note := fmt.Sprintf("The %s of task %s is gone. Its session no longer runs.", role, t.ID)
		if e.wakes(name) {
			note += " sfy wakes it when a message arrives for it."
		}
		return e.message("lead", "sfy", note)
	}
	return nil
}
