// Package process describes a factory's task process as agents, lists and branches.
// It does no I/O. The engine asks it which agent comes next after an agent passes or fails.
package process

import (
	"cmp"
	_ "embed"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The harness kinds. Each agent names one.
const (
	Claude   = "claude"
	Shell    = "shell"
	Lead     = "lead"
	Operator = "operator"
)

// The targets that end a task's lists instead of naming another list.
const (
	Done      = "done"
	Escalated = "escalated"
)

// The ways an agent ends, in its ends key.
const (
	EndsDone      = "done"
	EndsPushed    = "pushed"
	EndsVerdict   = "verdict"
	EndsRelease   = "release"
	EndsIntegrate = "integrate"
)

// The feedback modes that are not a list name.
const (
	FeedbackMessage = "message"
	FeedbackReopen  = "reopen"
)

// The values of the receives key.
const (
	ReceivesPrevious = "previous"
	ReceivesAll      = "all"
)

// The values of the session key.
const (
	SessionTask  = "task"
	SessionRound = "round"
)

//go:embed default.toml
var defaultTOML string

// Process is a factory's task process. Start names the list that a task starts in.
type Process struct {
	Agents map[string]Agent
	Lists  map[string]List
	Start  string
}

// Agent is one step of a list. Its harness decides how sfy starts it and how it passes.
type Agent struct {
	Name    string
	Harness string
	// Prompt names the prompt file of a claude agent, or holds the note text of a lead agent.
	Prompt     string
	RepoPrompt string
	Model      string
	Session    string
	// SessionName is the prefix of the session name. An agent with like shares the session of the agent it copies.
	SessionName string
	Like        string
	Run         []string
	Ends        string
	CountRound  bool
	Feedback    string
	// Slot is true when the agent holds a worker slot.
	Slot     bool
	Show     string
	Message  string
	Receives string
	// Permissions widens or narrows what the agent may do.
	Permissions Permissions
	// Keys lists the keys that the tables set for this agent, sorted, so Validate can refuse a key that does not apply.
	Keys []string
}

// Permissions holds an agent's own permission settings. A nil list keeps the factory's list of that name.
type Permissions struct {
	Sandbox          bool
	Edit             bool
	Push             bool
	Allow            []string
	ExcludedCommands []string
	AllowWrite       *[]string
	DenyRead         *[]string
	PassEnv          *[]string
	// Keys lists the permission keys that the tables set, sorted.
	Keys []string
}

// Wide reports whether the agent runs outside the sandbox or with path lists of its own.
func (p Permissions) Wide() bool {
	return !p.Sandbox || p.AllowWrite != nil || p.DenyRead != nil || p.PassEnv != nil
}

// List is a sequence of agent and list names. A list name in Agents is a join, which Flatten replaces with that list's agents.
type List struct {
	Name      string
	Agents    []string
	MaxRounds int
	Then      Then
}

// Then holds the branches of a list.
type Then struct {
	Pass Branch
	// Fail is the target of a failed list. FailBy picks a target by the name of the agent that failed, and wins over Fail.
	Fail   Target
	FailBy map[string]Target
}

// Branch picks the target of a passed list. Exactly one of Target, Cases and Shell is set.
type Branch struct {
	Target *Target
	// Cases holds the entries of a pass array. The first entry whose facts all hold wins.
	Cases []Case
	Shell *ShellBranch
}

// Case is one entry of a pass array. An entry with no facts always holds.
type Case struct {
	If     []string
	Target Target
}

// ShellBranch runs a shell agent whose last output line names the target, which must be in To.
type ShellBranch struct {
	Agent string
	To    []string
}

// Target is a list name, Done or Escalated. Round is true when the branch counts a round.
type Target struct {
	List  string
	Round bool
}

// End reports whether the target ends the task's lists.
func (t Target) End() bool { return t.List == Done || t.List == Escalated }

// Position is the agent that a task is at, inside the list that the task entered.
type Position struct {
	List, Agent string
}

func (p Position) String() string { return p.List + "/" + p.Agent }

// Outcome is how an agent ended.
type Outcome int

// The outcomes of an agent.
const (
	Pass Outcome = iota
	Fail
)

// Facts are what sfy knows about a task, for the if keys of a pass array.
type Facts struct {
	Repo, Remote, Hold, Branch bool
}

// factNames lists the facts that an if key may name.
var factNames = []string{"repo", "remote", "hold", "branch"}

func (f Facts) holds(name string) bool {
	switch name {
	case "repo":
		return f.Repo
	case "remote":
		return f.Remote
	case "hold":
		return f.Hold
	case "branch":
		return f.Branch
	}
	return false
}

// resolve fills the defaults of each agent and copies the fields of the agent that like names.
// Agents without like come first, so an agent that copies another gets its filled fields.
func (p *Process) resolve(provider string) error {
	names := slices.Sorted(maps.Keys(p.Agents))
	for _, name := range names {
		if a := p.Agents[name]; a.Like == "" {
			p.Agents[name] = a.filled(provider)
		}
	}
	for _, name := range names {
		a := p.Agents[name]
		if a.Like == "" {
			continue
		}
		base, ok := p.Agents[a.Like]
		if !ok {
			return fmt.Errorf("process: agents.%s.like names %q, which is no agent", name, a.Like)
		}
		if base.Like != "" {
			return fmt.Errorf("process: agents.%s.like names %q, which copies another agent itself", name, a.Like)
		}
		p.Agents[name] = a.copyFrom(base).filled(provider)
	}
	return nil
}

// filled returns the agent with the default of each key that it does not set.
func (a Agent) filled(provider string) Agent {
	a.Harness = cmp.Or(a.Harness, provider)
	a.Ends = cmp.Or(a.Ends, defaultEnds(a.Harness))
	if !a.has("slot") {
		a.Slot = a.Harness == Claude || a.Harness == Shell
	}
	a.Feedback = cmp.Or(a.Feedback, FeedbackMessage)
	a.Receives = cmp.Or(a.Receives, ReceivesPrevious)
	if a.Harness == Claude {
		a.Prompt = cmp.Or(a.Prompt, a.Name)
		a.Session = cmp.Or(a.Session, SessionTask)
		a.SessionName = cmp.Or(a.SessionName, a.Name)
	}
	if a.Harness == Lead {
		a.Prompt = cmp.Or(a.Prompt, a.Name)
	}
	return a
}

func defaultEnds(harness string) string {
	switch harness {
	case Operator:
		return EndsRelease
	case Lead:
		return EndsIntegrate
	}
	return EndsDone
}

// copyFrom fills each field that a does not set from base. Both then share one session.
func (a Agent) copyFrom(base Agent) Agent {
	fill := func(key string, into *string, from string) {
		if !a.has(key) {
			*into = from
		}
	}
	fill("harness", &a.Harness, base.Harness)
	fill("prompt", &a.Prompt, base.Prompt)
	fill("repo_prompt", &a.RepoPrompt, base.RepoPrompt)
	fill("model", &a.Model, base.Model)
	fill("session", &a.Session, base.Session)
	fill("message", &a.Message, base.Message)
	fill("session_name", &a.SessionName, base.SessionName)
	if !a.has("permissions") {
		a.Permissions = base.Permissions
	}
	return a
}

func (a Agent) has(key string) bool { return slices.Contains(a.Keys, key) }

// Label returns the text that status.md shows for the agent in list. {from} in its show key stands for from.
func (a Agent) Label(list, from string) string {
	if a.Show == "" {
		return list
	}
	return strings.ReplaceAll(a.Show, "{from}", from)
}
