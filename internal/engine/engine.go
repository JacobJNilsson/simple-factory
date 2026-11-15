// Package engine runs factory's commands and moves tasks between states.
// It does no I/O of its own and reaches the world through the interfaces below.
package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JacobJNilsson/factory/internal/config"
	"github.com/JacobJNilsson/factory/internal/protocol"
	"github.com/JacobJNilsson/factory/internal/store"
	"github.com/JacobJNilsson/factory/internal/task"
)

// SessionSpec describes an agent session to start. Kind is "main", "worker" or "reviewer".
type SessionSpec struct {
	Name, Kind, Task, Dir, Model, Prompt string
}

// Launcher controls agent sessions in terminal panes.
type Launcher interface {
	Start(SessionSpec) error
	Send(name, text string) error
	Alive(name string) (bool, error)
	Stop(name string) error
	Notify(name, text string) error
}

// Workspaces creates and removes the folder of a task.
type Workspaces interface {
	Create(task.Task) (dir string, err error)
	Remove(task.Task) error
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

	mu      sync.Mutex
	counter int
}

// New returns an Engine that works through the given store and interfaces.
func New(st store.Store, l Launcher, w Workspaces, cfg config.Config, c Clock) *Engine {
	return &Engine{st: st, l: l, w: w, cfg: cfg, c: c}
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
	"send":            {(*Engine).send, []kind{main}},
	"report done":     {(*Engine).reportDone, []kind{worker}},
	"report blocked":  {(*Engine).reportBlocked, []kind{worker}},
	"report progress": {(*Engine).reportProgress, []kind{worker}},
	"verdict pass":    {(*Engine).verdictPass, []kind{reviewer}},
	"verdict fail":    {(*Engine).verdictFail, []kind{reviewer}},
	"inbox":           {(*Engine).inbox, agents},
	"escalate":        {(*Engine).escalate, []kind{main}},
	"status":          {(*Engine).status, anyone},
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
		if allowed(c.kind, k) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
