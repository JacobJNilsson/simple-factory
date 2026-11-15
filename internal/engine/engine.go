// Package engine runs sfy's commands and moves tasks between states.
// It does no I/O of its own and reaches the world through the interfaces below.
package engine

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// SessionSpec describes an agent session to start. Kind is "main", "worker" or "reviewer".
type SessionSpec struct {
	Name, Kind, Task, Dir, Model, Prompt string
	// Protected lists the paths that a worker or reviewer must not edit. It is empty for the main agent.
	Protected []string
}

// Launcher controls agent sessions in terminal panes.
type Launcher interface {
	Start(SessionSpec) error
	Send(name, text string) error
	Alive(name string) (bool, error)
	Stop(name string) error
	Notify(name, text string) error
	// TrustWarning returns a warning when the provider would stop an agent at a trust prompt for dir.
	TrustWarning(dir string) string
}

// Workspaces creates and removes the folder of a task.
type Workspaces interface {
	Create(task.Task) (dir string, err error)
	// Restore makes the folder of a reopened task again, on the task branch with its earlier work.
	Restore(task.Task) (dir string, err error)
	Remove(task.Task) error
	// HoldsWork reports whether the task's workspace holds work that Remove would delete.
	HoldsWork(task.Task) (bool, error)
	// RepoFolder returns the folder that a task on repo belongs to, which exists after the first clone.
	RepoFolder(repo string) string
}

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// Engine handles commands. The mutex keeps a command's reads and writes together.
// It is held across Start and Stop, which can be slow, so one command runs at a time.
type Engine struct {
	st  store.Store
	l   Launcher
	w   Workspaces
	cfg config.Config
	c   Clock

	log     io.Writer
	mu      sync.Mutex
	counter int
	// agents holds what sfy knows of each session, in memory only.
	agents map[string]*agentState
	// startFails counts the failed starts in a row of each task.
	startFails map[string]int
}

// agentState tracks when a session may take a note.
type agentState struct {
	// waiting is true after a turn ended and until sfy types into the pane.
	waiting bool
	// reported is true when the agent ran report or verdict in its current turn.
	reported bool
	// hookSince is true when a hook event came after the last note.
	hookSince bool
	// reminders counts the turns in a row that ended without a report.
	reminders int
	lastNote  time.Time
	// notedID is the id of the newest unread message that the last note covered.
	notedID string
	// gone is true after sfy told main that the session no longer runs.
	gone bool
	// skipLogged is true after the log named this agent as not alive, until it lives again.
	skipLogged bool
	// aliveFailLogged is true after the log named a failed alive check of this agent, until a check succeeds.
	aliveFailLogged bool
}

func (e *Engine) agent(name string) *agentState {
	if e.agents[name] == nil {
		e.agents[name] = &agentState{}
	}
	return e.agents[name]
}

// SetLog makes the engine write one line to w for each note it types and for each unread agent it skips as not alive.
func (e *Engine) SetLog(w io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.log = w
}

// logf writes one timestamped line to the log, when one is set.
func (e *Engine) logf(format string, args ...any) {
	if e.log != nil {
		_, _ = fmt.Fprintf(e.log, "%s "+format+"\n", append([]any{e.c.Now().Format(time.RFC3339)}, args...)...)
	}
}

// New returns an Engine that works through the given store and interfaces.
func New(st store.Store, l Launcher, w Workspaces, cfg config.Config, c Clock) *Engine {
	return &Engine{st: st, l: l, w: w, cfg: cfg, c: c, agents: map[string]*agentState{}, startFails: map[string]int{}}
}

type kind string

const (
	operator kind = ""
	main     kind = "main"
	worker   kind = "worker"
	reviewer kind = "reviewer"
)

func (k kind) label() string {
	if k == operator {
		return "the operator"
	}
	return "a " + string(k)
}

var (
	workerName   = regexp.MustCompile(`^worker-t[0-9]+$`)
	reviewerName = regexp.MustCompile(`^review-t[0-9]+-[0-9]+$`)
)

// kindOf reads the kind from a session name. Only the empty name is the operator.
func kindOf(agent string) (kind, bool) {
	switch {
	case agent == "":
		return operator, true
	case agent == "main":
		return main, true
	case workerName.MatchString(agent):
		return worker, true
	case reviewerName.MatchString(agent):
		return reviewer, true
	}
	return operator, false
}

type handler func(e *Engine, req protocol.Request) (string, error)

type command struct {
	run  handler
	kind []kind
}

var anyone = []kind{operator, main, worker, reviewer}
var agents = []kind{main, worker, reviewer}

// commands lists each command with the kinds that may run it.
var commands = map[string]command{
	"task new":        {(*Engine).taskNew, []kind{operator, main}},
	"task list":       {(*Engine).taskList, anyone},
	"task show":       {(*Engine).taskShow, anyone},
	"task edit":       {(*Engine).taskEdit, []kind{main}},
	"task queue":      {(*Engine).taskQueue, []kind{main}},
	"task cancel":     {(*Engine).taskCancel, []kind{operator, main}},
	"task resume":     {(*Engine).resume, []kind{operator, main}},
	"task feedback":   {(*Engine).taskFeedback, []kind{operator, main}},
	"task complete":   {(*Engine).taskComplete, []kind{operator, main}},
	"task archive":    {(*Engine).taskArchive, []kind{operator, main}},
	"send":            {(*Engine).send, []kind{main}},
	"report done":     {(*Engine).reportDone, []kind{worker}},
	"report blocked":  {(*Engine).reportBlocked, []kind{worker, reviewer}},
	"report progress": {(*Engine).reportProgress, []kind{worker}},
	"report pushed":   {(*Engine).reportPushed, []kind{worker}},
	"verdict pass":    {(*Engine).verdictPass, []kind{reviewer}},
	"verdict fail":    {(*Engine).verdictFail, []kind{reviewer}},
	"inbox":           {(*Engine).inbox, agents},
	"escalate":        {(*Engine).escalate, []kind{main}},
	"status":          {(*Engine).status, anyone},
	"hook turn-ended": {(*Engine).hookTurnEnded, agents},
	"hook permission": {(*Engine).hookPermission, agents},
	"hook idle":       {(*Engine).hookIdle, agents},
}

// Handle runs one command and returns its answer.
func (e *Engine) Handle(req protocol.Request) protocol.Answer {
	e.mu.Lock()
	defer e.mu.Unlock()
	cmd, ok := commands[req.Command]
	if !ok {
		return protocol.Answer{Error: fmt.Sprintf("unknown command %q", req.Command)}
	}
	k, known := kindOf(req.Caller.Agent)
	if !known {
		return protocol.Answer{Error: fmt.Sprintf("unknown caller %q. Agent names are main, worker-<id> and review-<id>-<round>", req.Caller.Agent)}
	}
	if !allowed(cmd.kind, k) {
		return protocol.Answer{Error: fmt.Sprintf("%q is not for %s. You may run: %s", req.Command, k.label(), allowedBy(k))}
	}
	text, err := cmd.run(e, req)
	if err != nil {
		return protocol.Answer{Error: err.Error()}
	}
	e.noteCommand(req)
	return protocol.Answer{OK: true, Text: text}
}

func allowed(kinds []kind, k kind) bool {
	for _, c := range kinds {
		if c == k {
			return true
		}
	}
	return false
}

// allowedBy lists the commands that a kind may run, sorted.
func allowedBy(k kind) string {
	var names []string
	for name, c := range commands {
		// The hook commands are internal, so no error names them.
		if allowed(c.kind, k) && !strings.HasPrefix(name, "hook ") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// noteCommand records what a successful command means for the caller's session.
// A verdict ends its reviewer's session, so its state goes. Only a worker or a reviewer has a report to track.
func (e *Engine) noteCommand(req protocol.Request) {
	name := req.Caller.Agent
	switch {
	case strings.HasPrefix(req.Command, "verdict "):
		delete(e.agents, name)
	case strings.HasPrefix(req.Command, "report "):
		s := e.agent(name)
		s.reported, s.reminders = true, 0
	}
}
