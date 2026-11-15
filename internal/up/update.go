package up

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// brewUpgrade runs brew upgrade sfy with the host environment and returns what brew printed.
func brewUpgrade(environ []string) ([]byte, error) {
	cmd := exec.CommandContext(context.Background(), "brew", "upgrade", "sfy")
	cmd.Env = environ
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// brewError reports a failed brew upgrade and carries what brew printed.
type brewError struct {
	err    error
	output string
}

func (e *brewError) Error() string {
	return "brew upgrade sfy failed, sfy keeps running on the old binary: " + e.err.Error() + "\n" + e.output
}

func (e *brewError) Unwrap() error { return e.err }

// restartCommand restarts sfy, after an upgrade when update is set. A failed upgrade stops before anything restarts.
func restartCommand(o options, update bool) error {
	if update {
		if err := upgrade(o); err != nil {
			return err
		}
	}
	return restart(o)
}

// upgrade runs brew upgrade sfy. When brew is missing or fails, the error carries brew's output and sfy keeps its old binary.
// A sfy that is already the newest version makes brew exit with code 0, so that case counts as success.
func upgrade(o options) error {
	output, err := o.brew(o.environ)
	if err != nil {
		return &brewError{err: err, output: strings.TrimSpace(string(output))}
	}
	_, err = fmt.Fprintln(o.out, "Upgraded sfy with brew.")
	return err
}
