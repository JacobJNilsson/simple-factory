// Package engine runs sfy's commands and moves tasks between states.
// It does no I/O of its own and reaches the world through the interfaces below.
package engine

import (
	"cmp"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/drop"
	"github.com/JacobJNilsson/simple-factory/internal/engine/delivery"
	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// SessionSpec describes an agent session to start. Kind is "lead", "worker" or "reviewer".
type SessionSpec struct {
	Name, Kind, Task, Dir, Model, Prompt string
	// Harness names the launcher's adapter for the session. An empty harness means the launcher's default.
	Harness string
	// Protected lists the paths that a worker or reviewer must not edit. It is empty for the lead.
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
	// ResumePrompt is the first message when the provider resumes the lead's last conversation.
	ResumePrompt string
	// Fresh makes the provider start a new conversation, even when it recorded one.
	Fresh bool
	// TaskFile is the absolute path of the task's task.md. It is set for workers and reviewers and empty for the lead.
	TaskFile string
	// Agent names the process agent that owns the session. Its session_rules and session_settings apply. An empty name means the Kind.
	Agent string
	// Role and RepoRole name the prompt of the session and the prompt part for a task with a repo. An empty Role means the prompt of the Kind.
	Role, RepoRole string
	// ReadOnly denies the edit tools, for an agent whose permissions set edit to false.
	ReadOnly bool
	// Unsandboxed runs the session outside the sandbox, for an agent whose permissions set sandbox to false.
	Unsandboxed bool
	// AllowWrite, DenyRead and PassEnv replace the factory's list of that name when they are not nil.
	AllowWrite, DenyRead, PassEnv *[]string
}

// Launcher controls agent sessions in terminal panes.
type Launcher interface {
	Start(SessionSpec) error
	Send(name, text string) error
	Alive(name string) (bool, error)
	Stop(name string) error
	Notify(name, text string) error
	// Escape presses the Escape key in the session of name.
	Escape(name string) error
	// TrustWarning returns a warning when the provider would stop an agent at a trust prompt for dir.
	TrustWarning(dir string) string
}

// Registration tells whether an agent session registered itself as the lead, which sfy did not start.
type Registration interface {
	Registered() bool
	// Clear forgets the registration and the recorded address.
	Clear() error
}

// Workspaces creates and removes the folder of a task.
type Workspaces interface {
	// Prepare fixes the branch and lease of a task and makes its folder when Dir is empty.
	// It returns the branch and lease it fixed even on an error, so that the engine can record them.
	Prepare(task.Task) (task.Prepared, error)
	// BranchOf returns the recorded branch, or the legacy name of a started task with a repo, or "".
	BranchOf(task.Task) string
	Remove(task.Task) error
	// HoldsWork reports whether the task's workspace holds work that Remove would delete.
	HoldsWork(task.Task) (bool, error)
	// RemoteBranch checks the task's existing branch and returns its sha on the remote.
	// It refuses a repo with no origin, a branch the remote lacks, and the default branch.
	RemoteBranch(task.Task) (sha string, err error)
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
	// repoConfig returns the config for a repo folder. It adds the per-repo files to cfg.
	repoConfig func(folder string) (config.Config, error)

	log     io.Writer
	reg     Registration
	auth    Authenticator
	mu      sync.Mutex
	counter int
	// sess starts, stops and checks sessions, and holds what sfy knows of each one.
	sess *sessions
	// notes gives notes to sessions and tracks when they may take one.
	notes *delivery.Delivery
	// startFails counts the failed starts in a row of each task.
	startFails map[string]int
	// cleanupRetries holds, for each task whose cleanup retry failed, the backoff state of the retries.
	cleanupRetries map[string]*cleanupRetry
	// depNoted holds the "task>dependency" pairs for which sfy told the lead that the dependency is cancelled.
	depNoted map[string]bool
	// slotWait holds when sfy first saw each ready queued task without a free slot, in memory only.
	slotWait map[string]time.Time
	// slotNoted holds the queued tasks for which sfy told the lead that they wait for a slot.
	slotNoted map[string]bool
	// ck runs the check commands of tasks. Without it, a task with a check goes to review at once.
	ck Checker
	// checking holds the check that runs for each task, in memory only.
	checking map[string]checkRun
	// checkSeq numbers the checks, so the result of a dropped check is never taken for a later one.
	checkSeq int
	// down is true after sfy down stopped the sessions. The server then ends the sfy process.
	down atomic.Bool
	// factories reaches the other factories on this machine. Without it, sends to other factories and relays fail.
	factories Factories
	// drops is the drop folder that programs with no session write to. Without it, the engine reads no drops.
	drops *drop.Folder
	// proc is the task process, which decides the agent that comes after each one.
	proc process.Process
}

// SetLog makes the engine write one line to w for each note it types, for each unread agent it skips as not alive,
// and for each refused command and each accepted report or verdict.
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

// SetRegistration tells the engine where to find the registration of the lead. Without it, no agent counts as registered.
func (e *Engine) SetRegistration(r Registration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reg = r
}

// Authenticator decides who calls from the session tokens that the server's factory folder holds.
// It returns the session name that the request's caller proves, or an error that refuses the request.
type Authenticator interface {
	Authenticate(protocol.Caller) (agent string, err error)
}

// SetAuth makes the engine take the caller's identity from the session token in each request.
// Without it, the engine trusts the name in the request.
func (e *Engine) SetAuth(a Authenticator) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.auth = a
}

// authenticate returns the request with the caller's name taken from the authenticator.
func (e *Engine) authenticate(req protocol.Request) (protocol.Request, error) {
	if e.auth == nil {
		return req, nil
	}
	name, err := e.auth.Authenticate(req.Caller)
	if err != nil {
		return req, err
	}
	req.Caller.Agent = name
	return req, nil
}

// registeredLead reports whether the lead is a session that registered itself.
func (e *Engine) registeredLead() bool { return e.reg != nil && e.reg.Registered() }

// logf writes one timestamped line to the log, when one is set.
func (e *Engine) logf(format string, args ...any) {
	if e.log != nil {
		_, _ = fmt.Fprintf(e.log, "%s "+format+"\n", append([]any{e.c.Now().Format(time.RFC3339)}, args...)...)
	}
}

// New returns an Engine that works through the given store and interfaces.
// ch gives notes to sessions without typing. A nil ch means that every note is typed.
func New(st store.Store, l Launcher, ch delivery.Channel, w Workspaces, cfg config.Config, c Clock) *Engine {
	e := &Engine{st: st, l: l, w: w, cfg: cfg, c: c, startFails: map[string]int{}, cleanupRetries: map[string]*cleanupRetry{}, depNoted: map[string]bool{}, slotWait: map[string]time.Time{}, slotNoted: map[string]bool{}, checking: map[string]checkRun{}, proc: process.Default()}
	e.sess = &sessions{
		l: l, cfg: cfg, proc: e.proc, logf: e.logf,
		taskFile:    func(id string) (string, error) { return e.st.TaskFile(id) },
		branchOf:    func(t task.Task) string { return e.w.BranchOf(t) },
		forgetNotes: func(name string) { e.notes.Forget(name) }, agents: map[string]*agentState{}, ended: map[string]bool{}, gone: map[string]bool{},
	}
	grace := cfg.NoteFallbackAfter
	// A config without the setting, as tests build it, gets the default.
	if grace <= 0 {
		grace = config.DefaultNoteFallbackAfter
	}
	e.notes = delivery.New(delivery.Deps{
		Panes: l, Channel: ch, Mailbox: st, Now: func() time.Time { return c.Now() },
		Logf:       e.logf,
		Registered: e.registeredLead, Runs: e.runs, Gone: e.sess.isGone, Probe: e.sess.probeGone,
		Resend: time.Duration(cfg.ResendMinutes) * time.Minute, Grace: grace,
	})
	return e
}

type kind string

const (
	operator kind = ""
	lead     kind = protocol.Lead
	worker   kind = "worker"
	reviewer kind = "reviewer"
)

func (k kind) label() string {
	if k == operator {
		return "the operator"
	}
	return "a " + string(k)
}

type handler func(e *Engine, req protocol.Request) (string, error)

type command struct {
	run  handler
	kind []kind
}

var anyone = []kind{operator, lead, worker, reviewer}
var agents = []kind{lead, worker, reviewer}

// commands lists each command with the kinds that may run it.
var commands = map[string]command{
	"task new":        {(*Engine).taskNew, []kind{operator, lead}},
	"task list":       {(*Engine).taskList, anyone},
	"task show":       {(*Engine).taskShow, anyone},
	"task edit":       {(*Engine).taskEdit, []kind{lead}},
	"task queue":      {(*Engine).taskQueue, []kind{lead}},
	"task cancel":     {(*Engine).taskCancel, []kind{operator, lead}},
	"task resume":     {(*Engine).resume, []kind{operator, lead}},
	"task feedback":   {(*Engine).taskFeedback, []kind{operator, lead}},
	"task complete":   {(*Engine).taskComplete, []kind{operator, lead}},
	"task integrate":  {(*Engine).taskIntegrate, []kind{operator, lead}},
	"task release":    {(*Engine).taskRelease, []kind{operator, lead}},
	"task push":       {(*Engine).taskRelease, []kind{operator, lead}},
	"task archive":    {(*Engine).taskArchive, []kind{operator, lead}},
	"down":            {(*Engine).downCommand, []kind{operator}},
	"send":            {(*Engine).send, []kind{operator, lead}},
	"unstick":         {(*Engine).unstick, []kind{lead}},
	"report done":     {(*Engine).reportDone, []kind{worker}},
	"report blocked":  {(*Engine).reportBlocked, []kind{worker, reviewer}},
	"report progress": {(*Engine).reportProgress, []kind{worker}},
	"report pushed":   {(*Engine).reportPushed, []kind{worker}},
	"verdict pass":    {(*Engine).verdictPass, []kind{reviewer}},
	"verdict fail":    {(*Engine).verdictFail, []kind{reviewer}},
	"inbox":           {(*Engine).inbox, agents},
	"escalate":        {(*Engine).escalate, []kind{lead}},
	"status":          {(*Engine).status, anyone},
	"hook turn-ended": {(*Engine).hookTurnEnded, agents},
	"hook permission": {(*Engine).hookPermission, agents},
	"hook idle":       {(*Engine).hookIdle, agents},
	"hook busy":       {(*Engine).hookBusy, agents},
	"restarted":       {(*Engine).restarted, []kind{operator, lead}},
	// A relay comes from another factory's sfy process. It has no kind here, because relay checks the token against that factory.
	protocol.RelayCommand:     {(*Engine).relay, nil},
	protocol.RelayTaskCommand: {(*Engine).relayTask, nil},
}

// Handle runs one command and returns its answer.
// A send to another factory leaves the lock before it calls that factory's sfy process.
func (e *Engine) Handle(req protocol.Request) protocol.Answer {
	ans, out := e.handleLocked(req)
	if out != nil {
		return relayOut(*out)
	}
	return ans
}

// handleLocked runs handle under the lock. The deferred unlock frees the lock also when a command panics.
func (e *Engine) handleLocked(req protocol.Request) (protocol.Answer, *outgoing) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.handle(req)
}

// handle runs one command under the lock and logs its answer. It returns an outgoing message instead of an answer for a send to another factory.
func (e *Engine) handle(req protocol.Request) (protocol.Answer, *outgoing) {
	ans, out := e.dispatch(req)
	if out == nil {
		e.logAnswer(req, ans)
	}
	return ans, out
}

// logClip is the most runes of a report text or an error that one log line holds.
const logClip = 200

// logAnswer writes one line for a refused command and for an accepted report or verdict. The line holds the name that the request claims.
func (e *Engine) logAnswer(req protocol.Request, ans protocol.Answer) {
	taskID := cmp.Or(req.Caller.Task, "-")
	switch {
	case !ans.OK:
		e.logf("refused %q from %s on task %s: %q", req.Command, callerName(req.Caller), taskID, clip(ans.Error))
	case strings.HasPrefix(req.Command, "report ") || strings.HasPrefix(req.Command, "verdict "):
		e.logf("accepted %q from %s on task %s: %q", req.Command, callerName(req.Caller), taskID, clip(text(req, req.Args)))
	}
}

// callerName names the caller of a request for the log. It never reads the token.
func callerName(c protocol.Caller) string {
	name := cmp.Or(c.Agent, "operator")
	if c.Factory != "" {
		name += " of factory " + c.Factory
	}
	return name
}

// clip cuts s to logClip runes and marks a cut with "...".
func clip(s string) string {
	r := []rune(s)
	if len(r) <= logClip {
		return s
	}
	return string(r[:logClip]) + "..."
}

// dispatch runs one command under the lock. It returns an outgoing message instead of an answer for a send to another factory.
func (e *Engine) dispatch(req protocol.Request) (protocol.Answer, *outgoing) {
	cmd, ok := commands[req.Command]
	if !ok {
		return protocol.Answer{Error: fmt.Sprintf("unknown command %q", req.Command)}, nil
	}
	if cmd.kind == nil {
		// The command checks its caller itself, against the factory that the caller names.
		text, err := cmd.run(e, req)
		if err != nil {
			return protocol.Answer{Error: err.Error()}, nil
		}
		return protocol.Answer{OK: true, Text: text}, nil
	}
	if req.Caller.Factory != "" {
		return protocol.Answer{Error: fmt.Sprintf("%q takes no caller from another factory", req.Command)}, nil
	}
	return e.handleLocal(req, cmd)
}

// handleLocal runs a command of a caller of this factory. A send to another factory passes the same checks first.
func (e *Engine) handleLocal(req protocol.Request, cmd command) (protocol.Answer, *outgoing) {
	req, err := e.authenticate(req)
	if err != nil {
		return protocol.Answer{Error: err.Error()}, nil
	}
	req.Caller.Agent = protocol.AgentName(req.Caller.Agent)
	k, known := e.kindOf(req.Caller.Agent)
	if !known {
		return protocol.Answer{Error: fmt.Sprintf("unknown caller %q. Agent names are lead, worker-<id> and review-<id>-<round>", req.Caller.Agent)}, nil
	}
	if !allowed(cmd.kind, k) {
		return protocol.Answer{Error: fmt.Sprintf("%q is not for %s. You may run: %s", req.Command, k.label(), allowedBy(k))}, nil
	}
	if factory, agent, ok := relayTarget(req); ok {
		out, err := e.prepareRelay(req, factory, agent)
		if err != nil {
			return protocol.Answer{Error: err.Error()}, nil
		}
		return protocol.Answer{}, out
	}
	if factory, ok := taskRelayTarget(req); ok {
		out, err := e.prepareTaskRelay(req, factory)
		if err != nil {
			return protocol.Answer{Error: err.Error()}, nil
		}
		return protocol.Answer{}, out
	}
	req, warning := oldTarget(req)
	text, err := cmd.run(e, req)
	if err != nil {
		return protocol.Answer{Error: err.Error(), Warning: warning}, nil
	}
	e.noteCommand(req)
	return protocol.Answer{OK: true, Text: text, Warning: warning}, nil
}

// oldTarget reads the old name of the lead as the target of send, and returns the warning for it.
func oldTarget(req protocol.Request) (protocol.Request, string) {
	if req.Command != "send" || len(req.Args) == 0 || req.Args[0] != protocol.OldLead {
		return req, ""
	}
	req.Args = append([]string{protocol.Lead}, req.Args[1:]...)
	return req, protocol.OldLeadWarning
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
		// The hook commands and restarted are internal, so no error names them.
		if allowed(c.kind, k) && !strings.HasPrefix(name, "hook ") && name != "restarted" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// noteCommand records what a successful command means for the caller's session, and for a worker that the lead answers.
// Only a worker or a reviewer has a report to track.
func (e *Engine) noteCommand(req protocol.Request) {
	switch {
	case strings.HasPrefix(req.Command, "report "):
		s := e.sess.agent(req.Caller.Agent)
		s.reported, s.reminders = true, 0
		if req.Command != "report blocked" {
			s.blockedAt = time.Time{}
		}
	case req.Command == "send" && req.Caller.Agent == protocol.Lead && len(req.Args) > 0:
		e.clearBlock(req.Args[0])
	case req.Command == "task feedback" && len(req.Args) > 0:
		if t, err := e.st.Task(req.Args[0]); err == nil {
			e.clearBlock(e.workerOf(t))
		}
	}
}

// clearBlock forgets that a worker reported blocked. It keeps no state for a session that sfy does not know.
func (e *Engine) clearBlock(name string) {
	if s := e.sess.agents[name]; s != nil {
		s.blockedAt = time.Time{}
	}
}
