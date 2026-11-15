package up

import (
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrNotOperator reports that an agent ran sfy restart. The command would end the main agent's own session.
var ErrNotOperator = errors.New("only the operator runs sfy restart, from a terminal outside the agent sessions")

// ErrNotStopped reports a sfy process that kept answering after its session ended.
var ErrNotStopped = errors.New("the sfy process still answers after it was stopped. Stop it by hand and run sfy up")

// mainSession is the multiplexer session of the main agent.
const mainSession = "main"

// restart stops the sfy process and the main agent, then starts both as sfy up does.
// The multiplexer ends the sfy session with a hangup, which the sfy process handles by removing its socket and lock file.
// Workers and reviewers keep their sessions, and their tasks stay on disk.
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
	if mainAlive {
		if err := o.launcher.Stop(mainSession); err != nil {
			return err
		}
		stopped = append(stopped, "the main agent")
	}
	if err := reportList(o.out, "Stopped", stopped); err != nil {
		return err
	}
	return run(o)
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
