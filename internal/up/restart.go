package up

import (
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrNotAllowed reports that a worker or reviewer ran sfy restart.
var ErrNotAllowed = errors.New("only the operator and the main agent run sfy restart")

// ErrNotStopped reports a sfy process that kept answering after its session ended.
var ErrNotStopped = errors.New("the sfy process still answers after it was stopped. Stop it by hand and run sfy")

// mainSession is the multiplexer session of the main agent.
const mainSession = "main"

// mayRestart reports whether the agent may run sfy restart. The operator has no agent name.
func mayRestart(agent string) bool { return agent == "" || agent == mainSession }

// restart stops the sfy process and starts it again as a plain sfy does.
// The multiplexer ends the sfy session with a hangup, which the sfy process handles by removing its socket and lock file.
// A running main agent keeps its session and chat, and gets a note. Workers and reviewers keep their sessions, and their tasks stay on disk.
func restart(o options) error {
	var stopped []string
	serveAlive, err := o.mux.Alive(serveSession)
	if err != nil {
		return err
	}
	if serveAlive || o.answers() {
		if serveAlive {
			if err := o.mux.Stop(serveSession); err != nil {
				return err
			}
		}
		if err := o.waitStopped(); err != nil {
			return err
		}
		stopped = append(stopped, "the sfy process")
	}
	mainAlive, err := o.launcher.Alive(mainSession)
	if err != nil {
		return err
	}
	if err := reportList(o.out, "Stopped", stopped); err != nil {
		return err
	}
	started, _, err := o.ensure()
	if err != nil {
		return err
	}
	if err := report(o.out, started); err != nil {
		return err
	}
	if !mainAlive {
		return nil
	}
	return o.noteMain()
}

// noteMain tells the running main agent that sfy restarted, and where to read a changed prompt.
// The note is one line, because a multiplexer sends a new line as Enter.
func (o options) noteMain() error {
	changed, err := recordMainPrompt(o.home)
	if err != nil {
		return err
	}
	note := "sfy restarted."
	if changed {
		note += " Your prompt changed. Read the new text in " + mainPromptPath(o.home) + " with the Read tool."
	}
	if err := o.launcher.Send(mainSession, note); err != nil {
		return err
	}
	_, err = fmt.Fprintln(o.out, "Told the main agent.")
	return err
}

// waitStopped waits until the sfy session ended and the socket stops answering.
func (o options) waitStopped() error {
	deadline := time.Now().Add(o.wait)
	for {
		alive, err := o.mux.Alive(serveSession)
		if err != nil {
			return err
		}
		if !alive && !o.answers() {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrNotStopped
		}
		time.Sleep(o.poll)
	}
}

func reportList(out io.Writer, verb string, names []string) error {
	var err error
	switch len(names) {
	case 0:
	case 1:
		_, err = fmt.Fprintf(out, "%s %s.\n", verb, names[0])
	default:
		_, err = fmt.Fprintf(out, "%s %s and %s.\n", verb, names[0], names[1])
	}
	return err
}
