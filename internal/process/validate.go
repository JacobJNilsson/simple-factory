package process

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// validName limits agent and list names, so a name fits a session name and a file name.
var validName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Harnesses lists the four harness kinds.
var Harnesses = []string{Claude, Shell, Lead, Operator}

// harnessKeys lists the agent keys that apply to each harness, besides the keys that apply to all.
var harnessKeys = map[string][]string{
	Claude:   {"prompt", "repo_prompt", "model", "session", "session_name", "like", "message", "permissions"},
	Shell:    {"run", "permissions"},
	Lead:     {"prompt", "message"},
	Operator: {},
}

// commonKeys apply to every harness.
var commonKeys = []string{"harness", "ends", "count_round", "feedback", "slot", "show", "receives"}

// harnessPermissions lists the permission keys that apply to each harness.
var harnessPermissions = map[string][]string{
	Claude: {"sandbox", "edit", "push", "allow", "excluded_commands", "allow_write", "deny_read", "pass_env"},
	Shell:  {"sandbox", "allow_write", "deny_read", "pass_env"},
}

// harnessEnds lists the ends values of each harness.
var harnessEnds = map[string][]string{
	Claude:   {EndsDone, EndsPushed, EndsVerdict},
	Shell:    {EndsDone},
	Lead:     {EndsIntegrate},
	Operator: {EndsRelease},
}

// Validate checks the process and the positions of the open tasks, keyed by task id. It returns every problem it finds, joined.
func (p Process) Validate(open map[string]Position) error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf("process: "+format, args...)) }
	for _, name := range slices.Sorted(maps.Keys(p.Agents)) {
		if _, both := p.Lists[name]; both {
			add("%s names both an agent and a list", name)
		}
		if !validName.MatchString(name) {
			add("the agent name %q must match [a-z][a-z0-9-]*", name)
		}
		errs = append(errs, p.validateAgent(p.Agents[name])...)
	}
	for _, name := range slices.Sorted(maps.Keys(p.Lists)) {
		if !validName.MatchString(name) || name == Done || name == Escalated {
			add("the list name %q must match [a-z][a-z0-9-]* and may not be done or escalated", name)
		}
		if _, err := p.Flatten(name); err != nil {
			errs = append(errs, err)
		}
		errs = append(errs, p.validateThen(p.Lists[name])...)
	}
	if _, ok := p.Lists[p.Start]; !ok {
		add("start names %q, which is no list", p.Start)
	}
	if loop := p.idleLoop(); loop != "" {
		add("the loop %s counts no round and has no agent that waits, so it would never end", loop)
	}
	for _, id := range slices.Sorted(maps.Keys(open)) {
		at := open[id]
		agents, err := p.Flatten(at.List)
		if err != nil || !slices.Contains(agents, at.Agent) {
			errs = append(errs, &GoneError{Task: id, At: at})
		}
	}
	return errors.Join(errs...)
}

// GoneError reports an open task whose position the process no longer has.
type GoneError struct {
	Task string
	At   Position
}

func (e *GoneError) Error() string {
	return fmt.Sprintf("process: task %s is at %s, which this process no longer has", e.Task, e.At)
}

func (p Process) validateAgent(a Agent) []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("process: agents.%s: "+format, append([]any{a.Name}, args...)...))
	}
	allowed, known := harnessKeys[a.Harness]
	if !known {
		add("unknown harness %q. A harness is %s", a.Harness, strings.Join(Harnesses, ", "))
		return errs
	}
	for _, k := range a.Keys {
		if !slices.Contains(commonKeys, k) && !slices.Contains(allowed, k) {
			add("the key %s does not apply to the %s harness", k, a.Harness)
		}
	}
	for _, k := range a.Permissions.Keys {
		if !slices.Contains(harnessPermissions[a.Harness], k) {
			add("the permission %s does not apply to the %s harness", k, a.Harness)
		}
	}
	if !slices.Contains(harnessEnds[a.Harness], a.Ends) {
		add("ends %q does not apply to the %s harness, which ends with %s", a.Ends, a.Harness, strings.Join(harnessEnds[a.Harness], " or "))
	}
	if a.Like != "" && p.Agents[a.Like].Harness != Claude {
		add("like names %q, which is no claude agent", a.Like)
	}
	if a.Harness == Claude && a.Session != SessionTask && a.Session != SessionRound {
		add("session %q must be task or round", a.Session)
	}
	if a.Receives != ReceivesPrevious && a.Receives != ReceivesAll {
		add("receives %q must be previous or all", a.Receives)
	}
	if a.Feedback != FeedbackMessage && a.Feedback != FeedbackReopen {
		if _, ok := p.Lists[a.Feedback]; !ok {
			add("feedback %q must be message, reopen or a list name", a.Feedback)
		}
	}
	if a.Harness == Shell && len(a.Run) == 0 {
		add("a shell agent needs a run list")
	}
	return errs
}

func (p Process) validateThen(l List) []error {
	var errs []error
	where := "lists." + l.Name + ".then"
	check := func(key string, t Target) {
		if !t.End() {
			if _, ok := p.Lists[t.List]; !ok {
				errs = append(errs, fmt.Errorf("process: %s.%s names %q, which is no list, done or escalated", where, key, t.List))
			}
		}
	}
	b := l.Then.Pass
	switch {
	case b.Target != nil:
		check("pass", *b.Target)
	case b.Shell != nil:
		if a, ok := p.Agents[b.Shell.Agent]; !ok || a.Harness != Shell {
			errs = append(errs, fmt.Errorf("process: %s.pass.agent names %q, which is no shell agent", where, b.Shell.Agent))
		}
		for _, to := range b.Shell.To {
			check("pass.to", Target{List: to})
		}
	default:
		for i, c := range b.Cases {
			check(fmt.Sprintf("pass entry %d", i+1), c.Target)
			for _, f := range c.If {
				if !slices.Contains(factNames, f) {
					errs = append(errs, fmt.Errorf("process: %s.pass entry %d names the unknown fact %q. A fact is %s", where, i+1, f, strings.Join(factNames, ", ")))
				}
			}
			if len(c.If) == 0 && i != len(b.Cases)-1 {
				errs = append(errs, fmt.Errorf("process: %s.pass entry %d has no if, so only the last entry may lack one", where, i+1))
			}
		}
		if n := len(b.Cases); n == 0 || len(b.Cases[n-1].If) > 0 {
			errs = append(errs, fmt.Errorf("process: %s.pass needs a last entry with no if", where))
		}
	}
	check("fail", l.Then.Fail)
	for _, agent := range slices.Sorted(maps.Keys(l.Then.FailBy)) {
		if _, ok := p.Agents[agent]; !ok {
			errs = append(errs, fmt.Errorf("process: %s.fail.%s names no agent", where, agent))
		}
		check("fail."+agent, l.Then.FailBy[agent])
	}
	return errs
}

// edge is one branch from a list to a list.
type edge struct {
	to    string
	round bool
}

// targets returns every list that a list's branches can reach.
func (l List) targets() []edge {
	var out []edge
	add := func(t Target) {
		if !t.End() {
			out = append(out, edge{to: t.List, round: t.Round})
		}
	}
	b := l.Then.Pass
	switch {
	case b.Target != nil:
		add(*b.Target)
	case b.Shell != nil:
		for _, to := range b.Shell.To {
			add(Target{List: to})
		}
	}
	for _, c := range b.Cases {
		add(c.Target)
	}
	add(l.Then.Fail)
	for _, t := range l.Then.FailBy {
		add(t)
	}
	return out
}

// idleLoop returns a loop of lists in which no branch counts a round and no list holds an agent that waits or counts a round.
// It returns an empty string when there is none.
func (p Process) idleLoop() string {
	idle := map[string]bool{}
	for name := range p.Lists {
		agents, err := p.Flatten(name)
		if err != nil {
			continue
		}
		idle[name] = !slices.ContainsFunc(agents, func(a string) bool {
			agent := p.Agents[a]
			return agent.CountRound || agent.Harness != Shell
		})
	}
	// A depth-first search over the idle edges finds a cycle.
	const (
		unseen = iota
		open
		closed
	)
	mark := map[string]int{}
	var stack []string
	var found string
	var visit func(string)
	visit = func(name string) {
		mark[name] = open
		stack = append(stack, name)
		for _, e := range p.Lists[name].targets() {
			if found != "" || e.round || !idle[name] {
				continue
			}
			switch mark[e.to] {
			case open:
				i := slices.Index(stack, e.to)
				found = strings.Join(append(slices.Clone(stack[i:]), e.to), " > ")
			case unseen:
				visit(e.to)
			}
		}
		stack = stack[:len(stack)-1]
		mark[name] = closed
	}
	for _, name := range slices.Sorted(maps.Keys(p.Lists)) {
		if mark[name] == unseen && found == "" {
			visit(name)
		}
	}
	return found
}
