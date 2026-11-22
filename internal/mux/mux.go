// Package mux drives terminal multiplexers through command templates.
package mux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Templates holds one shell command line for each multiplexer action.
type Templates struct {
	Start, Send, Alive, Stop, Notify, Show, ShowLead string
	// Whoami prints the address of the terminal it runs in, as a value that the other templates accept as {name}.
	Whoami string
}

// Mux controls named sessions in a terminal multiplexer.
type Mux interface {
	Start(name, dir string, command []string) error
	Send(name, text string) error
	Alive(name string) (bool, error)
	Stop(name string) error
	Notify(name, text string) error
	Show(name string) error
	ShowLead(name string) error
}

// FromMap builds a template set from a config table and rejects unknown keys.
func FromMap(m map[string]string) (Templates, error) {
	var t Templates
	fields := map[string]*string{
		"start": &t.Start, "send": &t.Send, "alive": &t.Alive, "stop": &t.Stop, "notify": &t.Notify, "show": &t.Show, "show_lead": &t.ShowLead, "whoami": &t.Whoami,
	}
	for key, value := range m {
		field, ok := fields[key]
		if !ok {
			return Templates{}, &templateError{template: key, problem: "is unknown"}
		}
		*field = value
	}
	for _, key := range []string{"start", "send", "alive", "stop"} {
		if *fields[key] == "" {
			return Templates{}, &templateError{template: key, problem: "is missing"}
		}
	}
	return t, nil
}

// Builtin returns a shipped template set by name.
func Builtin(name string) (Templates, bool) {
	if name == "tmux" {
		return tmuxSet, true
	}
	return Templates{}, false
}

// The "=" prefix makes tmux match the session name exactly, and the trailing colon selects its pane.
var tmuxSet = Templates{
	Start: "tmux new-session -d -s {name} -c {dir} -- {command}",
	Send:  "tmux send-keys -t ={name}: -l -- {text} && tmux send-keys -t ={name}: Enter",
	Alive: "tmux has-session -t ={name}",
	Stop:  "tmux kill-session -t ={name}",
	// The pane id selects the pane that runs the command, and the session name works as {name} in the other templates.
	Whoami: `tmux display-message -p -t "$TMUX_PANE" '#{session_name}'`,
}

// A template that runs longer than this is killed and reported as an error.
const commandTimeout = 30 * time.Second

// templateError names the template that a config table got wrong.
type templateError struct{ template, problem string }

func (e *templateError) Error() string {
	return fmt.Sprintf("multiplexer template %q %s", e.template, e.problem)
}

// commandError carries the stderr of a template that failed.
type commandError struct {
	which, stderr string
	err           error
}

func (e *commandError) Error() string {
	return fmt.Sprintf("multiplexer %s template failed: %v: %s", e.which, e.err, e.stderr)
}

func (e *commandError) Unwrap() error { return e.err }

// Runner fills the templates and runs them with sh -c.
// CONTRACT: callers hold a Mux, so the engine can use a fake in tests.
func Runner(t Templates) Mux { return RunnerFor(t, "") }

// RunnerFor works as Runner and gives each template the factory folder home as {home} and as SFY_HOME.
// An empty home sets neither.
// CONTRACT: callers hold a Mux, as for Runner, so RunnerFor returns the interface.
func RunnerFor(t Templates, home string) Mux {
	return runner{t: t, home: home, timeout: commandTimeout}
}

type runner struct {
	t       Templates
	home    string
	timeout time.Duration
}

func (r runner) Start(name, dir string, command []string) error {
	quoted := make([]string, len(command))
	for i, arg := range command {
		quoted[i] = shellQuote(arg)
	}
	_, err := r.run(r.t.Start, "start", name, dir, strings.Join(quoted, " "), "")
	return err
}

func (r runner) Send(name, text string) error {
	_, err := r.run(r.t.Send, "send", name, "", "", text)
	return err
}

func (r runner) Stop(name string) error {
	_, err := r.run(r.t.Stop, "stop", name, "", "", "")
	return err
}

func (r runner) Notify(name, text string) error {
	if r.t.Notify == "" {
		return nil
	}
	_, err := r.run(r.t.Notify, "notify", name, "", "", text)
	return err
}

// Show makes the session visible to the operator. A set without a show template does nothing.
func (r runner) Show(name string) error {
	if r.t.Show == "" {
		return nil
	}
	_, err := r.run(r.t.Show, "show", name, "", "", "")
	return err
}

// ShowLead makes the lead's session visible to the operator.
// A set without a show_lead template uses the show template.
func (r runner) ShowLead(name string) error {
	if r.t.ShowLead == "" {
		return r.Show(name)
	}
	_, err := r.run(r.t.ShowLead, "show_lead", name, "", "", "")
	return err
}

// Whoami runs the whoami template and returns the address it prints.
// A set without a whoami template returns an empty address. A template that prints more than one line is an error.
func (r runner) Whoami() (string, error) {
	if r.t.Whoami == "" {
		return "", nil
	}
	out, _, err := r.runOut(r.t.Whoami, "whoami", "", "", "", "")
	if err != nil {
		return "", err
	}
	address := strings.TrimSpace(out)
	if strings.ContainsAny(address, "\r\n") {
		return "", &commandError{which: "whoami", stderr: "the template printed more than one line", err: errors.New("bad output")}
	}
	return address, nil
}

// Alive reads exit 0 as running and exit 1 as gone. Any other failure is an error.
func (r runner) Alive(name string) (bool, error) {
	code, err := r.run(r.t.Alive, "alive", name, "", "", "")
	if err == nil {
		return true, nil
	}
	if code == 1 {
		return false, nil
	}
	return false, err
}

// run returns the exit code, or -1 when the shell did not start or exit normally.
func (r runner) run(template, which, name, dir, command, text string) (int, error) {
	_, code, err := r.runOut(template, which, name, dir, command, text)
	return code, err
}

// runOut works as run and also returns what the command printed on stdout.
func (r runner) runOut(template, which, name, dir, command, text string) (string, int, error) {
	// One pass over the template keeps a value that holds "{text}" from being filled again.
	line := strings.NewReplacer(
		"{name}", shellQuote(name),
		"{dir}", shellQuote(dir),
		"{command}", command,
		"{text}", shellQuote(text),
		"{home}", shellQuote(r.home),
	).Replace(template)
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", line)
	// A child that keeps the stderr pipe open must not block Wait after the kill.
	cmd.WaitDelay = time.Second
	if r.home != "" {
		cmd.Env = append(os.Environ(), "SFY_HOME="+r.home)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), 0, nil
	}
	code := -1
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	}
	return "", code, &commandError{which: which, stderr: strings.TrimSpace(stderr.String()), err: err}
}

// Prefixed returns a Mux that puts prefix in front of every session name it passes on.
// An empty prefix returns m itself.
func Prefixed(m Mux, prefix string) Mux {
	if prefix == "" {
		return m
	}
	return prefixed{m: m, prefix: prefix}
}

type prefixed struct {
	m      Mux
	prefix string
}

func (p prefixed) Start(name, dir string, command []string) error {
	return p.m.Start(p.prefix+name, dir, command)
}
func (p prefixed) Send(name, text string) error    { return p.m.Send(p.prefix+name, text) }
func (p prefixed) Alive(name string) (bool, error) { return p.m.Alive(p.prefix + name) }
func (p prefixed) Stop(name string) error          { return p.m.Stop(p.prefix + name) }
func (p prefixed) Notify(name, text string) error  { return p.m.Notify(p.prefix+name, text) }
func (p prefixed) Show(name string) error          { return p.m.Show(p.prefix + name) }
func (p prefixed) ShowLead(name string) error      { return p.m.ShowLead(p.prefix + name) }

// Whoamier is a Mux that can tell the address of the terminal that sfy runs in.
type Whoamier interface {
	// Whoami returns the address, or an empty string when the set has no whoami template.
	Whoami() (string, error)
}

// Registered returns a Mux that sends every call for session name to the address that the file at path holds.
// A missing or empty file leaves the calls as they are. Other sessions are never changed.
// When Alive finds the address gone, Registered removes the file, so the next start runs as if nothing registered.
// A successful Stop removes the file too. Put Registered inside Prefixed and give it the prefixed name,
// because a recorded address is not a name that sfy chose.
// Whenever Registered removes the address file, it removes the files in also too.
// CONTRACT: callers hold a Mux, as for Prefixed, so Registered returns the interface.
func Registered(m Mux, name, path string, also ...string) Mux {
	return registered{m: m, name: name, path: path, also: also}
}

type registered struct {
	m          Mux
	name, path string
	also       []string
}

// target returns the recorded address for name, and whether it applies.
func (r registered) target(name string) (string, bool) {
	if name != r.name {
		return name, false
	}
	b, err := os.ReadFile(r.path)
	address := strings.TrimSpace(string(b))
	if err != nil || address == "" {
		return name, false
	}
	return address, true
}

func (r registered) forget() error {
	var errs []error
	for _, path := range append([]string{r.path}, r.also...) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r registered) Start(name, dir string, command []string) error {
	return r.m.Start(name, dir, command)
}
func (r registered) Send(name, text string) error {
	target, _ := r.target(name)
	return r.m.Send(target, text)
}
func (r registered) Notify(name, text string) error {
	target, _ := r.target(name)
	return r.m.Notify(target, text)
}
func (r registered) Show(name string) error {
	target, _ := r.target(name)
	return r.m.Show(target)
}
func (r registered) ShowLead(name string) error {
	target, _ := r.target(name)
	return r.m.ShowLead(target)
}
func (r registered) Alive(name string) (bool, error) {
	target, recorded := r.target(name)
	alive, err := r.m.Alive(target)
	if recorded && err == nil && !alive {
		err = r.forget()
	}
	return alive, err
}
func (r registered) Stop(name string) error {
	target, recorded := r.target(name)
	if err := r.m.Stop(target); err != nil {
		return err
	}
	if recorded {
		return r.forget()
	}
	return nil
}

// Whoami passes the question on, because a registered address does not change the terminal that sfy runs in.
func (r registered) Whoami() (string, error) {
	if w, ok := r.m.(Whoamier); ok {
		return w.Whoami()
	}
	return "", nil
}

// Whoami passes the question on without the prefix, because an address is not a session name.
func (p prefixed) Whoami() (string, error) {
	if w, ok := p.m.(Whoamier); ok {
		return w.Whoami()
	}
	return "", nil
}

// shellQuote wraps s in single quotes and escapes each single quote inside it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
