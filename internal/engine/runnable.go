package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/process"
)

// deliverAgents are the session agents after the review, in the order that a list after the review may hold them.
var deliverAgents = []string{"release", "push", "integrate"}

// Runnable refuses a valid process that this engine cannot run yet. Each error names the key that the engine would ignore or cannot follow.
// A session agent keeps the keys of its default agent, because session names, prompts and permissions follow the default.
// A shell agent runs only between the worker and the reviewer of a list that starts with the worker.
// A list after the review holds release, push and integrate in that order, ends with integrate, and passes to done.
func Runnable(p process.Process) error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(p.Agents)) {
		errs = append(errs, runnableAgent(p.Agents[name])...)
	}
	for _, name := range slices.Sorted(maps.Keys(p.Lists)) {
		errs = append(errs, runnableList(p, p.Lists[name])...)
	}
	if first, err := p.First(p.Start); err == nil && first.Agent != "worker" {
		errs = append(errs, fmt.Errorf("process: start: the start list %s must begin with the worker", p.Start))
	}
	return errors.Join(errs...)
}

// keyValue is one key of an agent with its value as text, for the comparison with the default agent.
type keyValue struct {
	key, value string
}

// fixedKeys returns the keys of an agent that this engine does not read, so they must keep their default.
func fixedKeys(a process.Agent) []keyValue {
	perm := a.Permissions
	list := func(l *[]string) string {
		if l == nil {
			return "the factory's list"
		}
		return strings.Join(*l, ", ")
	}
	return []keyValue{
		{"prompt", a.Prompt}, {"repo_prompt", a.RepoPrompt}, {"session", a.Session}, {"session_name", a.SessionName},
		{"like", a.Like}, {"message", a.Message}, {"feedback", a.Feedback}, {"receives", a.Receives},
		{"slot", fmt.Sprint(a.Slot)}, {"count_round", fmt.Sprint(a.CountRound)}, {"show", a.Show},
		{"permissions.sandbox", fmt.Sprint(perm.Sandbox)}, {"permissions.edit", fmt.Sprint(perm.Edit)}, {"permissions.push", fmt.Sprint(perm.Push)},
		{"permissions.allow_write", list(perm.AllowWrite)}, {"permissions.deny_read", list(perm.DenyRead)}, {"permissions.pass_env", list(perm.PassEnv)},
	}
}

func runnableAgent(a process.Agent) []error {
	var errs []error
	refuse := func(key, why string) {
		errs = append(errs, fmt.Errorf("process: agents.%s.%s: %s", a.Name, key, why))
	}
	if a.Harness == process.Shell {
		shell := process.Agent{Name: a.Name, Feedback: process.FeedbackMessage, Receives: process.ReceivesPrevious, Slot: true}
		for _, k := range []string{"feedback", "receives", "slot", "count_round"} {
			if got, want := valueOf(fixedKeys(a), k), valueOf(fixedKeys(shell), k); got != want {
				refuse(k, fmt.Sprintf("this sfy runs a shell agent only with the default %s", want))
			}
		}
		if a.Permissions.PassEnv != nil {
			refuse("permissions.pass_env", "a shell agent gets sfy's environment without the SFY_ variables, so this sfy takes no pass_env list for it")
		}
		return errs
	}
	d, ok := process.Default().Agents[a.Name]
	if !ok || d.Harness != a.Harness || d.Ends != a.Ends {
		refuse("harness", fmt.Sprintf("this sfy runs a %s agent only under a default name with the default harness and ends. Use a shell agent, or one of the default agents", a.Harness))
		return errs
	}
	mine, def := fixedKeys(a), fixedKeys(d)
	for i, kv := range mine {
		if kv.value != def[i].value {
			refuse(kv.key, fmt.Sprintf("this sfy runs the %s agent only with the default %q", a.Name, def[i].value))
		}
	}
	// The worker and the reviewer read model, and the other session agents start no session of their own.
	if a.Model != d.Model && a.Name != "worker" && a.Name != "reviewer" {
		refuse("model", "only the worker and the reviewer start a session that takes a model")
	}
	return errs
}

func valueOf(kvs []keyValue, key string) string {
	for _, kv := range kvs {
		if kv.key == key {
			return kv.value
		}
	}
	return ""
}

func runnableList(p process.Process, l process.List) []error {
	agents, err := p.Flatten(l.Name)
	if err != nil || len(agents) == 0 {
		return nil
	}
	var errs []error
	refuse := func(key, why string) {
		errs = append(errs, fmt.Errorf("process: lists.%s.%s: %s", l.Name, key, why))
	}
	if l.Then.Pass.Shell != nil {
		refuse("then.pass", "this sfy does not run a shell branch yet")
	}
	if agents[0] == "worker" {
		if !buildShape(p, agents) {
			refuse("agents", "a list that starts with the worker holds the worker, then shell agents only, then the reviewer last")
		}
		for _, t := range passTargets(l.Then.Pass) {
			if !t.End() && !deliverList(p, t.List) {
				refuse("then.pass", fmt.Sprintf("the target %s must be done, escalated or a list of release, push and integrate", t.List))
			}
		}
		for key, t := range failTargets(l.Then) {
			if !t.End() && !startsWithWorker(p, t.List) {
				refuse(key, fmt.Sprintf("the target %s must be done, escalated or a list that starts with the worker", t.List))
			}
		}
		return errs
	}
	if !deliverList(p, l.Name) {
		refuse("agents", "a list that does not start with the worker holds release, push and integrate in that order, with no shell agent, and ends with integrate")
		return errs
	}
	if t := l.Then.Pass.Target; t == nil || t.List != process.Done {
		refuse("then.pass", "a list that ends with integrate passes to done")
	}
	return errs
}

// buildShape reports whether agents are the worker, then shell agents, then the reviewer.
func buildShape(p process.Process, agents []string) bool {
	n := len(agents)
	if n < 2 || agents[0] != "worker" || agents[n-1] != "reviewer" {
		return false
	}
	for _, a := range agents[1 : n-1] {
		if p.Agents[a].Harness != process.Shell {
			return false
		}
	}
	return true
}

func startsWithWorker(p process.Process, list string) bool {
	first, err := p.First(list)
	return err == nil && first.Agent == "worker"
}

// deliverList reports whether a list holds release, push and integrate in that order, each at most once, and ends with integrate.
func deliverList(p process.Process, list string) bool {
	agents, err := p.Flatten(list)
	if err != nil || len(agents) == 0 || agents[len(agents)-1] != "integrate" {
		return false
	}
	at := 0
	for _, a := range agents {
		i := slices.Index(deliverAgents[at:], a)
		if i < 0 {
			return false
		}
		at += i + 1
	}
	return true
}

func passTargets(b process.Branch) []process.Target {
	if b.Target != nil {
		return []process.Target{*b.Target}
	}
	var out []process.Target
	for _, c := range b.Cases {
		out = append(out, c.Target)
	}
	return out
}

// failTargets returns each fail target with its key below the list.
func failTargets(t process.Then) map[string]process.Target {
	out := map[string]process.Target{"then.fail": t.Fail}
	for agent, target := range t.FailBy {
		out["then.fail."+agent] = target
	}
	return out
}
