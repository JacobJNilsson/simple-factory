// Package mux drives terminal multiplexers through command templates.
package mux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Templates holds one shell command line for each multiplexer action.
type Templates struct {
	Start, Send, Alive, Stop, Notify string
}

// Mux controls named sessions in a terminal multiplexer.
type Mux interface {
	Start(name, dir string, command []string) error
	Send(name, text string) error
	Alive(name string) (bool, error)
	Stop(name string) error
	Notify(name, text string) error
}

// FromMap builds a template set from a config table and rejects unknown keys.
func FromMap(m map[string]string) (Templates, error) {
	var t Templates
	fields := map[string]*string{
		"start": &t.Start, "send": &t.Send, "alive": &t.Alive, "stop": &t.Stop, "notify": &t.Notify,
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
	switch name {
	case "tmux":
		return tmuxSet, true
	case "herdr":
		return herdrSet, true
	}
	return Templates{}, false
}

// The "=" prefix makes tmux match the session name exactly, and the trailing colon selects its pane.
var tmuxSet = Templates{
	Start: "tmux new-session -d -s {name} -c {dir} -- {command}",
	Send:  "tmux send-keys -t ={name}: -l -- {text} && tmux send-keys -t ={name}: Enter",
	Alive: "tmux has-session -t ={name}",
	Stop:  "tmux kill-session -t ={name}",
}

// A finished terminal stays listed and hc get exits 0 for it, so Alive checks the state.
// Exit 1 means missing or finished. Any other hc failure exits 2, so Alive reports an error and not "gone".
var herdrSet = Templates{
	Start: "hc run --bg --name {name} --cwd {dir} --no-parent --no-notify -- {command}",
	Send:  "hc send {name} -- {text}",
	Alive: `out=$(hc get {name} --json 2>&1); rc=$?
case $out in *'"kind":"not_found"'*) exit 1;; esac
if [ $rc -ne 0 ]; then printf '%s\n' "$out" >&2; exit 2; fi
case $out in *'"state":"live"'*) exit 0;; esac
exit 1`,
	Stop: "hc kill {name}",
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
func Runner(t Templates) Mux { return runner{t: t, timeout: commandTimeout} }

type runner struct {
	t       Templates
	timeout time.Duration
}

func (r runner) Start(name, dir string, command []string) error {
	quoted := make([]string, len(command))
	for i, arg := range command {
		quoted[i] = shellQuote(arg)
	}
	_, err := run(r.timeout, r.t.Start, "start", name, dir, strings.Join(quoted, " "), "")
	return err
}

func (r runner) Send(name, text string) error {
	_, err := run(r.timeout, r.t.Send, "send", name, "", "", text)
	return err
}

func (r runner) Stop(name string) error {
	_, err := run(r.timeout, r.t.Stop, "stop", name, "", "", "")
	return err
}

func (r runner) Notify(name, text string) error {
	if r.t.Notify == "" {
		return nil
	}
	_, err := run(r.timeout, r.t.Notify, "notify", name, "", "", text)
	return err
}

// Alive reads exit 0 as running and exit 1 as gone. Any other failure is an error.
func (r runner) Alive(name string) (bool, error) {
	code, err := run(r.timeout, r.t.Alive, "alive", name, "", "", "")
	if err == nil {
		return true, nil
	}
	if code == 1 {
		return false, nil
	}
	return false, err
}

// run returns the exit code, or -1 when the shell did not start or exit normally.
func run(timeout time.Duration, template, which, name, dir, command, text string) (int, error) {
	// One pass over the template keeps a value that holds "{text}" from being filled again.
	line := strings.NewReplacer(
		"{name}", shellQuote(name),
		"{dir}", shellQuote(dir),
		"{command}", command,
		"{text}", shellQuote(text),
	).Replace(template)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", line)
	// A child that keeps the stderr pipe open must not block Wait after the kill.
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	code := -1
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	}
	return code, &commandError{which: which, stderr: strings.TrimSpace(stderr.String()), err: err}
}

// shellQuote wraps s in single quotes and escapes each single quote inside it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
