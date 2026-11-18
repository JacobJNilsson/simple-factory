// Package engine runs sfy's commands and moves tasks between states.
// It does no I/O of its own and reaches the world through the interfaces below.
package engine

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	// HasRepo reports that the task names a repo, so a worker gets the repo part of its prompt.
	HasRepo bool
	// Branch is the git branch of the task. It is empty for a task with no repo.
	Branch string
	// Lease is the sha that the remote branch of a task with an existing branch had at checkout or at the last push.
	// A worker may force-push with a lease on that sha, and on no other.
	Lease string
	// NoPush is true while the operator holds the push of the task. The session gets no push permission.
	NoPush bool
	// ResumePrompt is the first message when the provider resumes the main agent's last conversation.
	ResumePrompt string
	// Fresh makes the provider start a new conversation, even when it recorded one.
	Fresh bool
}

// Launcher controls agent sessions in terminal panes.
type Launcher interface {
	Start(SessionSpec) error
	Send(name, text string) error
	Alive(name string) (bool, error)
	Stop(name string) error
	Notify(name, text string) error
	// Deliver gives text to the agent through the provider, without typing into the pane.
	// It returns false, with nothing delivered, when the provider cannot. The caller then types the text with Send.
	// A non-empty messageID names the stored message that the text carries. It stays unread until the agent takes the text.
	Deliver(name, text, messageID string) (delivered bool, err error)
	// Stuck reports whether delivered text waits for a session that cannot take it.
	Stuck(name string) bool
	// Drain takes the stuck text that is no stored message and returns it.
	Drain(name string) (string, error)
	// TrustWarning returns a warning when the provider would stop an agent at a trust prompt for dir.
	TrustWarning(dir string) string
}

// Registration tells whether an agent session registered itself as the main agent, which sfy did not start.
type Registration interface {
	Registered() bool
	// Clear forgets the registration and the recorded address.
	Clear() error
}

// Workspaces creates and removes the folder of a task.
type Workspaces interface {
	Create(task.Task) (dir string, err error)
	// Restore makes the folder of a reopened task again, on the task branch with its earlier work.
	Restore(task.Task) (dir string, err error)
	Remove(task.Task) error
	// HoldsWork reports whether the task's workspace holds work that Remove would delete.
	HoldsWork(task.Task) (bool, error)
	// RemoteBranch checks the task's existing branch and returns its sha on the remote.
	// It refuses a repo with no origin, a branch the remote lacks, and the default branch.
	RemoteBranch(task.Task) (sha string, err error)
	// NewBranch returns a free branch name for the task, in the repo and on its remotes. It creates nothing.
	NewBranch(task.Task) (string, error)
	// LegacyBranch returns the branch name that sfy gave to tasks before they carried a branch of their own.
	LegacyBranch(id string) string
	// HasRemote reports whether the task's repo has a remote to push to. A repo that exists only locally has none.
	HasRemote(task.Task) (bool, error)
	// RepoFolder returns the folder that a task on repo belongs to, which exists after the first clone.
	RepoFolder(repo string) string
	// Integrate rebases the task branch onto the repo's default branch, fast-forwards the default branch, and pushes it when the repo has a remote.
	Integrate(task.Task) error
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
	// repoConfig returns the config for a repo folder. It adds the .sfy.toml files to cfg.
	repoConfig func(folder string) (config.Config, error)

	log     io.Writer
	reg     Registration
	auth    func(token string) (string, bool)
	mu      sync.Mutex
	counter int
	// agents holds what sfy knows of each session, in memory only.
	agents map[string]*agentState
	// startFails counts the failed starts in a row of each task.
	startFails map[string]int
	// cleanupWait holds, for each task whose cleanup could not tell whether a session runs, the number of retries to skip.
	cleanupWait map[string]int
	// depNoted holds the "task>dependency" pairs for which sfy told main that the dependency is cancelled.
	depNoted map[string]bool
	// down is true after sfy down stopped the sessions. The server then ends the sfy process.
	down atomic.Bool
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
	// hookedAt is when sfy queued unread messages in the note file for the session's hook. It is zero when no hook is due.
	hookedAt time.Time
	// notedID is the id of the newest unread message that the last note covered.
	notedID string
	// gone is true after sfy told main that the session no longer runs.
	gone bool
	// skipLogged is true after the log named this agent as not alive, until it lives again.
	skipLogged bool
	// aliveErrs counts the alive checks in a row that failed. A check that answers resets it.
	aliveErrs int
	// aliveErr is the error of the latest failed alive check.
	aliveErr error
	// aliveTold is true after sfy told main that it cannot tell whether the session runs.
	aliveTold bool
}

// restartGrace gives the hook a fresh grace period when the session starts to wait with queued messages.
func (s *agentState) restartGrace(now time.Time) {
	if !s.hookedAt.IsZero() {
		s.hookedAt = now
	}
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

// SetRepoConfig sets how the engine finds the config of a repo folder. Without it, every repo gets the global config.
func (e *Engine) SetRepoConfig(f func(folder string) (config.Config, error)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.repoConfig = f
}

// configFor returns the config that applies to a task's repo.
func (e *Engine) configFor(t task.Task) (config.Config, error) {
	if e.repoConfig == nil {
		return e.cfg, nil
	}
	return e.repoConfig(e.w.RepoFolder(t.Repo))
}

// SetRegistration tells the engine where to find the registration of the main agent. Without it, no agent counts as registered.
func (e *Engine) SetRegistration(r Registration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reg = r
}

// SetAuth makes the engine take the caller's identity from the session token in each request.
// auth returns the session name that owns a token. Without it, the engine trusts the name in the request.
func (e *Engine) SetAuth(auth func(token string) (string, bool)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.auth = auth
}

// authenticate returns the request with the caller's name taken from its token.
// A named caller needs the token of that session. A request with neither name nor token comes from the operator.
func (e *Engine) authenticate(req protocol.Request) (protocol.Request, error) {
	if e.auth == nil {
		return req, nil
	}
	claimed := req.Caller.Agent
	if req.Caller.Token == "" {
		if claimed != "" {
			return req, errors.New("missing session token. This session started before sessions had tokens. Restart the session, or run sfy restart, or run sfy from the session that sfy started")
		}
		return req, nil
	}
	name, ok := e.auth(req.Caller.Token)
	if !ok || (claimed != "" && claimed != name) {
		return req, errors.New("bad session token")
	}
	req.Caller.Agent = name
	return req, nil
}

// registeredMain reports whether the main agent is a session that registered itself.
func (e *Engine) registeredMain() bool { return e.reg != nil && e.reg.Registered() }

// logf writes one timestamped line to the log, when one is set.
func (e *Engine) logf(format string, args ...any) {
	if e.log != nil {
		_, _ = fmt.Fprintf(e.log, "%s "+format+"\n", append([]any{e.c.Now().Format(time.RFC3339)}, args...)...)
	}
}

// New returns an Engine that works through the given store and interfaces.
func New(st store.Store, l Launcher, w Workspaces, cfg config.Config, c Clock) *Engine {
	return &Engine{st: st, l: l, w: w, cfg: cfg, c: c, agents: map[string]*agentState{}, startFails: map[string]int{}, cleanupWait: map[string]int{}, depNoted: map[string]bool{}}
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
	"task integrate":  {(*Engine).taskIntegrate, []kind{operator, main}},
	"task push":       {(*Engine).taskPush, []kind{operator, main}},
	"task archive":    {(*Engine).taskArchive, []kind{operator, main}},
	"down":            {(*Engine).downCommand, []kind{operator}},
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
	"hook busy":       {(*Engine).hookBusy, agents},
}

// Handle runs one command and returns its answer.
func (e *Engine) Handle(req protocol.Request) protocol.Answer {
	e.mu.Lock()
	defer e.mu.Unlock()
	cmd, ok := commands[req.Command]
	if !ok {
		return protocol.Answer{Error: fmt.Sprintf("unknown command %q", req.Command)}
	}
	req, err := e.authenticate(req)
	if err != nil {
		return protocol.Answer{Error: err.Error()}
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
