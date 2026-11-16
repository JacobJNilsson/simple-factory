package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
)

// Stopping reports that sfy down stopped the sessions, so the server ends the sfy process after it answers.
func (e *Engine) Stopping() bool { return e.down.Load() }

// downCommand stops the main agent and every worker and reviewer session that runs, through the launcher.
// It changes no task. A failed stop keeps the sfy process running, so the operator can run sfy down again.
func (e *Engine) downCommand(protocol.Request) (string, error) {
	tasks, err := e.st.Tasks()
	if err != nil {
		return "", err
	}
	names := []string{"main"}
	for _, t := range tasks {
		if t.Dir == "" {
			continue
		}
		names = append(names, "worker-"+t.ID)
		for r := 1; r <= t.Round; r++ {
			names = append(names, fmt.Sprintf("review-%s-%d", t.ID, r))
		}
	}
	var stopped []string
	var errs []error
	for _, name := range names {
		alive, err := e.l.Alive(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !alive {
			continue
		}
		if err := e.stop(name); err != nil {
			errs = append(errs, err)
			continue
		}
		stopped = append(stopped, name)
	}
	if err := errors.Join(errs...); err != nil {
		return "", err
	}
	e.down.Store(true)
	if len(stopped) == 0 {
		return "No agent session ran. Stopped the sfy process.", nil
	}
	return "Stopped " + strings.Join(stopped, ", ") + " and the sfy process.", nil
}
