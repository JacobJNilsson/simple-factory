package engine

import (
	"errors"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
)

// Stopping reports that sfy down stopped the sessions, so the server ends the sfy process after it answers.
func (e *Engine) Stopping() bool { return e.down.Load() }

// downCommand stops every worker and reviewer session that runs, through the launcher. It never stops the lead.
// The lead keeps its records, so the next sfy up finds it. It changes no task.
// A failed stop keeps the sfy process running, so the operator can run sfy down again.
func (e *Engine) downCommand(protocol.Request) (string, error) {
	tasks, err := e.st.Tasks()
	if err != nil {
		return "", err
	}
	var names []string
	for _, t := range tasks {
		if t.Dir == "" {
			continue
		}
		names = append(names, e.sess.allSessions(t)...)
	}
	var stopped []string
	var errs []error
	for _, name := range names {
		l, err := e.sess.alive(name)
		if l == sessionUnknown {
			errs = append(errs, err)
			continue
		}
		if l == sessionGone {
			continue
		}
		if err := e.sess.stop(name); err != nil {
			errs = append(errs, err)
			continue
		}
		stopped = append(stopped, name)
	}
	if err := errors.Join(errs...); err != nil {
		return "", err
	}
	// A failed check of the lead only leaves out the line, because down never touches the lead.
	leadRuns, _ := e.sess.alive(protocol.Lead)
	e.down.Store(true)
	text := "Stopped the sfy process."
	switch {
	case len(stopped) > 0:
		text = "Stopped " + strings.Join(stopped, ", ") + " and the sfy process."
	case leadRuns != sessionRuns:
		text = "No agent session ran. Stopped the sfy process."
	}
	if leadRuns == sessionRuns {
		text += " Left the lead running. sfy down stops only the sfy process, the workers and the reviewers."
	}
	return text, nil
}
