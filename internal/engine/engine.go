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
	"sync/atomic"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine/delivery"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// SessionSpec describes an agent session to start. Kind is "lead", "worker" or "reviewer".
type SessionSpec struct {
	Name, Kind, Task, Dir, Model, Prompt string
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
	// repoConfig returns the config for a repo folder. It adds the .sfy.toml files to cfg.
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
	// cleanupWait holds, for each task whose cleanup could not tell whether a session runs, the number of retries to skip.
	cleanupWait map[string]int
	// depNoted holds the "task>dependency" pairs for which sfy told the lead that the dependency is cancelled.
	depNoted map[string]bool
	// ck runs the check commands of tasks. Without it, a task with a check goes to review at once.
	ck Checker
	// checking holds the check that runs for each task, in memory only.
	checking map[string]checkRun
	// checkSeq numbers the checks, so the result of a dropped check is never taken for a later one.
	checkSeq int
	// down is true after sfy down stopped the sessions. The server then ends the sfy process.
	down atomic.Bool
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
	e := &Engine{st: st, l: l, w: w, cfg: cfg, c: c, startFails: map[string]int{}, cleanupWait: map[string]int{}, depNoted: map[string]bool{}, checking: map[string]checkRun{}}
	e.sess = &sessions{
		l: l, cfg: cfg, logf: e.logf,
		taskFile:    func(id string) (string, error) { return e.st.TaskFile(id) },
		branchOf:    func(t task.Task) string { return e.w.BranchOf(t) },
		forgetNotes: func(name string) { e.notes.Forget(name) }, agents: map[string]*agentState{},
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

var (
	workerName   = regexp.MustCompile(`^worker-t[0-9]+$`)
	reviewerName = regexp.MustCompile(`^review-t[0-9]+-[0-9]+$`)
)

// kindOf reads the kind from a session name. Only the empty name is the operator. The old name of the lead reads as the lead.
func kindOf(agent string) (kind, bool) {
	switch {
	case agent == "":
		return operator, true
	case protocol.AgentName(agent) == protocol.Lead:
		return lead, true
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
	"task push":       {(*Engine).taskPush, []kind{operator, lead}},
	"task archive":    {(*Engine).taskArchive, []kind{operator, lead}},
	"down":            {(*Engine).downCommand, []kind{operator}},
	"send":            {(*Engine).send, []kind{operator, lead}},
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
	req.Caller.Agent = protocol.AgentName(req.Caller.Agent)
	k, known := kindOf(req.Caller.Agent)
	if !known {
		return protocol.Answer{Error: fmt.Sprintf("unknown caller %q. Agent names are lead, worker-<id> and review-<id>-<round>", req.Caller.Agent)}
	}
	if !allowed(cmd.kind, k) {
		return protocol.Answer{Error: fmt.Sprintf("%q is not for %s. You may run: %s", req.Command, k.label(), allowedBy(k))}
	}
	req, warning := oldTarget(req)
	text, err := cmd.run(e, req)
	if err != nil {
		return protocol.Answer{Error: err.Error(), Warning: warning}
	}
	e.noteCommand(req)
	return protocol.Answer{OK: true, Text: text, Warning: warning}
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

// noteCommand records what a successful command means for the caller's session.
// Only a worker or a reviewer has a report to track.
func (e *Engine) noteCommand(req protocol.Request) {
	if strings.HasPrefix(req.Command, "report ") {
		s := e.sess.agent(req.Caller.Agent)
		s.reported, s.reminders = true, 0
	}
}
