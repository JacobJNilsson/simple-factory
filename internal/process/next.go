package process

import (
	"fmt"
	"slices"
	"strings"
)

// JoinError reports a list that joins itself. Path runs from the outer list to the repeated one.
type JoinError struct {
	Path []string
}

func (e *JoinError) Error() string {
	return fmt.Sprintf("process: list %s joins itself through %s", e.Path[len(e.Path)-1], strings.Join(e.Path, " > "))
}

// Flatten returns the agents of a list in order, with each joined list replaced by its agents at every depth.
// It refuses an unknown name and a join that contains itself.
func (p Process) Flatten(list string) ([]string, error) {
	return p.flatten(list, nil)
}

func (p Process) flatten(list string, stack []string) ([]string, error) {
	if slices.Contains(stack, list) {
		return nil, &JoinError{Path: append(slices.Clone(stack), list)}
	}
	l, ok := p.Lists[list]
	if !ok {
		return nil, fmt.Errorf("process: no list %q", list)
	}
	var out []string
	for _, name := range l.Agents {
		if _, isAgent := p.Agents[name]; isAgent {
			out = append(out, name)
			continue
		}
		if _, isList := p.Lists[name]; !isList {
			return nil, fmt.Errorf("process: lists.%s.agents names %q, which is no agent and no list", list, name)
		}
		inner, err := p.flatten(name, append(stack, list))
		if err != nil {
			return nil, err
		}
		out = append(out, inner...)
	}
	return out, nil
}

// First returns the position of the first agent of a list.
func (p Process) First(list string) (Position, error) {
	agents, err := p.Flatten(list)
	if err != nil {
		return Position{}, err
	}
	if len(agents) == 0 {
		return Position{}, fmt.Errorf("process: list %s has no agents", list)
	}
	return Position{List: list, Agent: agents[0]}, nil
}

// Last reports whether at is the last agent of its list, so a pass there follows the list's branch.
func (p Process) Last(at Position) bool {
	agents, err := p.Flatten(at.List)
	return err == nil && len(agents) > 0 && agents[len(agents)-1] == at.Agent
}

// MaxRounds returns the round at which a fail in the list escalates, with fallback for a list that sets none.
func (p Process) MaxRounds(list string, fallback int) int {
	if n := p.Lists[list].MaxRounds; n > 0 {
		return n
	}
	return fallback
}

// Next returns where a task goes after the agent at at ended with outcome.
// A pass before the last agent of the list gives the next agent and an empty target.
// Otherwise the list's branch picks the target. A target that names a list gives that list's first agent.
// Done and Escalated give an empty position. output is the output of the shell agent of a shell branch.
func (p Process) Next(at Position, outcome Outcome, facts Facts, output string) (Position, Target, error) {
	agents, err := p.Flatten(at.List)
	if err != nil {
		return Position{}, Target{}, err
	}
	i := slices.Index(agents, at.Agent)
	if i < 0 {
		return Position{}, Target{}, fmt.Errorf("process: list %s has no agent %s", at.List, at.Agent)
	}
	l := p.Lists[at.List]
	var target Target
	switch {
	case outcome == Pass && i+1 < len(agents):
		return Position{List: at.List, Agent: agents[i+1]}, Target{}, nil
	case outcome == Pass:
		target = l.Then.pass(facts, output)
	default:
		target = l.Then.fail(at.Agent)
	}
	if target.End() {
		return Position{}, target, nil
	}
	first, err := p.First(target.List)
	return first, target, err
}

// pass picks the target of a passed list. A shell branch whose output names no target in To follows fail.
func (t Then) pass(facts Facts, output string) Target {
	b := t.Pass
	switch {
	case b.Target != nil:
		return *b.Target
	case b.Shell != nil:
		if target, ok := b.Shell.Pick(output); ok {
			return target
		}
		return t.fail(b.Shell.Agent)
	}
	for _, c := range b.Cases {
		if c.holds(facts) {
			return c.Target
		}
	}
	return Target{List: Escalated}
}

// Pick reads the output of the branch's shell agent. It returns the target that the last line names, or false when that line names no target in To.
func (b ShellBranch) Pick(output string) (Target, bool) {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	name := strings.TrimSpace(lines[len(lines)-1])
	if slices.Contains(b.To, name) {
		return Target{List: name}, true
	}
	return Target{}, false
}

// FailTarget returns the target of a list that failed at agent.
func (t Then) FailTarget(agent string) Target { return t.fail(agent) }

// fail picks the target of a list that failed at agent.
func (t Then) fail(agent string) Target {
	if target, ok := t.FailBy[agent]; ok {
		return target
	}
	if t.Fail.List == "" {
		return Target{List: Escalated}
	}
	return t.Fail
}

func (c Case) holds(f Facts) bool {
	for _, name := range c.If {
		if !f.holds(name) {
			return false
		}
	}
	return true
}

// Values are the texts that the placeholders of a run command stand for.
type Values struct {
	// TaskCheck is the task's check command, which replaces {task_check} as it is.
	TaskCheck string
	// Factory, TaskFile, Drop and Result are paths. Each replaces its placeholder as one quoted shell word.
	Factory, TaskFile, Drop, Result string
}

// Expand replaces the placeholders of a run command. {task_check} gives the task's check command as it is.
// {factory}, {task_file}, {drop} and {result} give their path as one shell word in single quotes.
func Expand(command string, v Values) string {
	return strings.NewReplacer(
		"{task_check}", v.TaskCheck,
		"{factory}", quote(v.Factory),
		"{task_file}", quote(v.TaskFile),
		"{drop}", quote(v.Drop),
		"{result}", quote(v.Result),
	).Replace(command)
}

// quote makes s one shell word.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Commands returns the agent's run commands with the placeholders replaced.
// It skips an entry that is only {task_check} when the task has no check.
func (a Agent) Commands(v Values) []string {
	var out []string
	for _, c := range a.Run {
		if strings.TrimSpace(c) == "{task_check}" && strings.TrimSpace(v.TaskCheck) == "" {
			continue
		}
		out = append(out, Expand(c, v))
	}
	return out
}

// UsesDrop reports whether a run command of the agent names the {drop} folder.
func (a Agent) UsesDrop() bool {
	return slices.ContainsFunc(a.Run, func(c string) bool { return strings.Contains(c, "{drop}") })
}
