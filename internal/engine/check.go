package engine

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/drop"
	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// CheckResult is the result of one run of a shell agent's command, such as the task's check.
type CheckResult struct {
	// Output is the kept tail of the command's stdout and stderr.
	Output string
	// Cut is the number of bytes that sfy dropped from the start of the output.
	Cut int
	// Err is nil when the command exited with 0. Otherwise it holds the exit error, the start error or the timeout.
	Err error
	// TimedOut is true when the timeout stopped the command.
	TimedOut bool
	Took     time.Duration
	// Drops holds the files that the command wrote to its drop folder, in name order.
	Drops [][]byte
}

// Checker runs the check command of a task.
type Checker interface {
	// Start runs command in dir on another goroutine and calls done once with the result.
	// It returns at once and never calls done before it returns.
	Start(dir, command string, done func(CheckResult))
}

// ShellSpec is one command of a shell agent, with the agent's own permissions.
type ShellSpec struct {
	Dir, Command string
	// Sandbox is false for an agent that runs outside the sandbox.
	Sandbox bool
	// AllowWrite and DenyRead replace the factory's list of that name when they are not nil.
	AllowWrite, DenyRead *[]string
	// Drop is the folder that the command may write drops to. The runner creates it, and it is empty when the agent names no {drop}.
	Drop string
	// PassEnv, when it is not nil, limits the command's environment to a few base variables and the named ones.
	PassEnv *[]string
}

// ShellChecker is a Checker that runs a command with a shell agent's permissions and reads its drop folder.
// The engine uses Start of a Checker that lacks it.
type ShellChecker interface {
	Checker
	// StartShell runs spec on another goroutine and calls done once with the result, as Start does.
	StartShell(spec ShellSpec, done func(CheckResult))
}

// checkRun is a shell agent that runs for a task.
type checkRun struct {
	seq int
	// command is the task's check command at the start, so sfy notices an edit during the run.
	command string
	at      process.Position
	// from is the position of the task when the agent started. A task that moved since then drops the result.
	from process.Position
	// branch is the shell branch that the agent runs for, and last is the last agent of the list that passed. Both are empty for an agent of a list.
	branch *process.ShellBranch
	last   process.Position
	// cmds are the agent's commands, and next is the index of the one that runs.
	cmds []string
	next int
	drop string
	h    handoff
}

// SetChecker sets how the engine runs checks. Without it, a task with a check goes to review at once.
func (e *Engine) SetChecker(c Checker) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ck = c
}

// shownTasks returns the tasks with Checking set on those whose check runs.
func (e *Engine) shownTasks() ([]task.Task, error) {
	tasks, err := e.st.Tasks()
	for i := range tasks {
		_, tasks[i].Checking = e.checking[tasks[i].ID]
	}
	return tasks, err
}

// label returns the state that a list or a table shows. A running shell agent shows its own label, with the state as {from}.
// An agent with a show key shows that label while the task is at it, and every other agent shows the state.
func (e *Engine) label(t task.Task, all []task.Task) string {
	if run, ok := e.checking[t.ID]; ok {
		return e.proc.Agents[run.at.Agent].Label(run.at.List, string(t.State))
	}
	at := t.Position()
	if a := e.proc.Agents[at.Agent]; a.Show != "" && t.State != task.Escalated && at != (process.Position{}) {
		return a.Label(at.List, string(t.State))
	}
	return t.Label(all)
}

// renderStatus returns the text of status.md, with the label of each running shell agent.
func (e *Engine) renderStatus(tasks []task.Task) string {
	return store.RenderStatusLabeled(tasks, func(t task.Task) string { return e.label(t, tasks) })
}

// shellRun returns the commands of a shell agent for a task and the drop folder of the run.
// Without a checker, no command runs. The folder is tasks/<id>/drop/<n>-<agent>/, where n counts the task's results, so each run gets a fresh one.
func (e *Engine) shellRun(t task.Task, a process.Agent) ([]string, string, error) {
	if e.ck == nil {
		return nil, "", nil
	}
	file, err := e.st.TaskFile(t.ID)
	if err != nil {
		return nil, "", err
	}
	results, err := e.st.Results(t.ID)
	if err != nil {
		return nil, "", err
	}
	v := process.Values{TaskCheck: t.Check, Factory: factoryOf(file), TaskFile: file}
	if n := len(results); n > 0 {
		v.Result = results[n-1].Path
	}
	if a.UsesDrop() {
		v.Drop = filepath.Join(filepath.Dir(file), "drop", fmt.Sprintf("%d-%s", len(results)+1, a.Name))
	}
	return a.Commands(v), v.Drop, nil
}

// factoryOf returns the factory folder of a task file, which is tasks/<id>/task.md in it.
func factoryOf(taskFile string) string { return filepath.Dir(filepath.Dir(filepath.Dir(taskFile))) }

// startShell starts the first command of a shell agent and answers the worker at once. checkDone takes each result.
func (e *Engine) startShell(t task.Task, at process.Position, a process.Agent, cmds []string, dropDir string, h handoff) (string, error) {
	return e.runAgent(t, checkRun{at: at, cmds: cmds, drop: dropDir, h: h}, a)
}

// startBranch runs the shell agent of a list's pass branch after the agent at last passed. Its output picks the target.
func (e *Engine) startBranch(t task.Task, last process.Position, branch process.ShellBranch, h handoff) (string, error) {
	a := e.proc.Agents[branch.Agent]
	cmds, dropDir, err := e.shellRun(t, a)
	if err != nil {
		return "", err
	}
	if err := e.endRound(t, h.from); err != nil {
		return "", err
	}
	run := checkRun{at: process.Position{List: last.List, Agent: branch.Agent}, branch: &branch, last: last, cmds: cmds, drop: dropDir, h: h}
	if len(cmds) == 0 {
		// An agent with nothing to run prints nothing, so its branch follows fail.
		return "", e.applyBranch(t, run, CheckResult{})
	}
	return e.runAgent(t, run, a)
}

// runAgent records the run of a shell agent and starts its first command.
// A shell agent with count_round counts a round when it starts.
func (e *Engine) runAgent(t task.Task, run checkRun, a process.Agent) (string, error) {
	if a.CountRound {
		t.CountRound()
		if err := e.save(t); err != nil {
			return "", err
		}
	}
	e.checkSeq++
	run.seq, run.command, run.from = e.checkSeq, t.Check, t.Position()
	if slices.Contains(a.Run, "{task_check}") {
		run.h.checked = true
	}
	at, cmds := run.at, run.cmds
	e.checking[t.ID] = run
	e.runShell(t.ID, t.Dir, run, a)
	if err := e.refreshStatus(); err != nil {
		e.logf("status of task %s not written: %v", t.ID, err)
	}
	return fmt.Sprintf("sfy runs the %s now: %s. Its result comes as a message. When it passes, the review starts.", at.Agent, strings.Join(cmds, "; ")), nil
}

// runShell starts the command of run that comes next.
func (e *Engine) runShell(id, dir string, run checkRun, a process.Agent) {
	done := func(r CheckResult) { e.checkDone(id, run.seq, r) }
	command := run.cmds[run.next]
	if sc, ok := e.ck.(ShellChecker); ok {
		p := a.Permissions
		sc.StartShell(ShellSpec{Dir: dir, Command: command, Sandbox: p.Sandbox, AllowWrite: p.AllowWrite, DenyRead: p.DenyRead, Drop: run.drop, PassEnv: p.PassEnv}, done)
		return
	}
	e.ck.Start(dir, command, done)
}

// checkDone takes the result of a command. It ignores a run that endSessions or startWorker dropped.
func (e *Engine) checkDone(id string, seq int, r CheckResult) {
	e.mu.Lock()
	defer e.mu.Unlock()
	run, ok := e.checking[id]
	if !ok || run.seq != seq {
		return
	}
	delete(e.checking, id)
	verdict := "passed"
	if r.Err != nil {
		verdict = "failed"
	}
	e.logf("%s of task %s %s after %s", run.at.Agent, id, verdict, r.Took.Round(time.Millisecond))
	if err := e.applyCheck(id, run, r); err != nil {
		e.logf("%s of task %s: %v", run.at.Agent, id, err)
	}
	if err := e.refreshStatus(); err != nil {
		e.logf("status of task %s not written: %v", id, err)
	}
}

// applyCheck moves the task on after a command of a shell agent ended.
func (e *Engine) applyCheck(id string, run checkRun, r CheckResult) error {
	t, err := e.st.Task(id)
	if err != nil {
		return err
	}
	if t.State == task.Escalated || t.Position() != run.from {
		e.logf("check of task %s dropped, because the task is %s", id, t.State)
		return nil
	}
	e.takeShellDrops(id, run.at.Agent, r.Drops)
	worker := e.workerOf(t)
	if t.Check != run.command {
		return e.message(worker, "sfy", fmt.Sprintf("The check of task %s changed while it ran. Run sfy report done again.", id))
	}
	command := run.cmds[run.next]
	if r.Err == nil && run.next+1 < len(run.cmds) {
		run.next++
		e.checking[id] = run
		e.runShell(id, t.Dir, run, e.proc.Agents[run.at.Agent])
		return nil
	}
	e.record(id, run.at.Agent, r.Output)
	if run.branch != nil {
		return e.applyBranch(t, run, r)
	}
	if r.Err == nil {
		if _, err := e.passFrom(t, run.at, run.h); err != nil {
			return errors.Join(err, e.message(worker, "sfy", fmt.Sprintf("The check passed, but the reviewer did not start: %v. Run sfy report done again.", err)))
		}
		return nil
	}
	// A failed check counts as a review round, so a worker that never fixes it ends up escalated.
	note := func(t task.Task) string { return e.checkFailedNote(t, run.at.List, command, r) }
	why := func(t task.Task) string { return fmt.Sprintf("Task %s failed its check in round %d.", t.ID, t.Round) }
	return e.failAt(t, run.at, failure{from: "sfy", note: note, why: why})
}

// applyBranch follows the target that the output of a shell branch names. A failed command, or a last line that names no target in to, follows fail.
func (e *Engine) applyBranch(t task.Task, run checkRun, r CheckResult) error {
	if target, ok := run.branch.Pick(r.Output); ok && r.Err == nil {
		next, err := e.proc.First(target.List)
		if err != nil {
			return err
		}
		_, err = e.passTo(t, run.last, next, target, run.h)
		return err
	}
	target := e.proc.Lists[run.last.List].Then.FailTarget(run.branch.Agent)
	note := func(t task.Task) string {
		return fmt.Sprintf("The branch of task %s after list %s failed, because %s named none of %s. This counts as round %d of %d.\n\n```\n%s\n```\n",
			t.ID, run.last.List, run.branch.Agent, strings.Join(run.branch.To, ", "), t.Round, e.proc.MaxRounds(run.last.List, e.cfg.ReviewRounds), strings.TrimRight(r.Output, "\n"))
	}
	why := func(t task.Task) string {
		return fmt.Sprintf("The branch agent %s of task %s named no target in round %d.", run.branch.Agent, t.ID, t.Round)
	}
	return e.failTo(t, run.last, target, failure{from: "sfy", note: note, why: why})
}

// takeShellDrops gives the lead each drop that a shell agent wrote. sfy made the folder for the run, so the sender is the agent and the task.
func (e *Engine) takeShellDrops(id, agent string, drops [][]byte) {
	sender := agent + "@" + id
	for _, raw := range drops {
		d, err := drop.ParseFromAgent(raw)
		if err != nil {
			e.logf("drop of %s refused: %v", sender, err)
			continue
		}
		if err := e.message(protocol.Lead, sender, d.Body); err != nil {
			e.logf("drop of %s not delivered: %v", sender, err)
		}
	}
}

// checkFailedNote tells the worker that its check failed, with the command, the error and the kept output.
func (e *Engine) checkFailedNote(t task.Task, list, command string, r CheckResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The check of task %s failed, so no review started. This counts as review round %d of %d. Fix the result, then run sfy report done.\n\n", t.ID, t.Round, e.proc.MaxRounds(list, e.cfg.ReviewRounds))
	fmt.Fprintf(&b, "Command: %s\n", command)
	if r.TimedOut {
		fmt.Fprintf(&b, "The check did not finish within %s, so sfy stopped it.\n", e.cfg.CheckTimeout)
	} else {
		fmt.Fprintf(&b, "Error: %v\n", r.Err)
	}
	if r.Cut > 0 {
		fmt.Fprintf(&b, "sfy cut the first %d bytes of the output.\n", r.Cut)
	}
	fmt.Fprintf(&b, "\n```\n%s\n```\n", strings.TrimRight(r.Output, "\n"))
	return b.String()
}
