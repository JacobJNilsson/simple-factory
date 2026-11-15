package up

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// runBrew runs brew with the host environment and returns what brew printed.
func runBrew(environ []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(context.Background(), "brew", args...)
	cmd.Env = environ
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// brewError reports a failed brew command and carries what brew printed.
type brewError struct {
	command string
	err     error
	output  string
}

func (e *brewError) Error() string {
	return e.command + " failed, sfy keeps running on the old binary: " + e.err.Error() + "\n" + e.output
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

// brewRun runs one brew command and wraps a failure in a brewError that carries brew's output.
func brewRun(o options, args ...string) (string, error) {
	output, err := o.brew(o.environ, args...)
	text := strings.TrimSpace(string(output))
	if err != nil {
		return text, &brewError{command: "brew " + strings.Join(args, " "), err: err, output: text}
	}
	return text, nil
}

// installedVersion returns the version of the sfy cask that brew lists, from "sfy 0.1.1".
func installedVersion(o options) (string, error) {
	text, err := brewRun(o, "list", "--cask", "--versions", "sfy")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return "", &brewError{command: "brew list --cask --versions sfy", err: fmt.Errorf("unexpected output %q", text), output: text}
	}
	return fields[len(fields)-1], nil
}

// upgrade updates brew's taps, runs brew upgrade sfy and reports the versions before and after.
// Brew skips its own update when the last one is recent, so an explicit brew update makes the new release visible.
// When brew is missing or fails, the error carries brew's output and sfy keeps its old binary.
// A sfy that is already the newest version makes brew exit with code 0, so that case counts as success.
func upgrade(o options) error {
	before, err := installedVersion(o)
	if err != nil {
		return err
	}
	if _, err := brewRun(o, "update"); err != nil {
		return err
	}
	if _, err := brewRun(o, "upgrade", "sfy"); err != nil {
		return err
	}
	after, err := installedVersion(o)
	if err != nil {
		return err
	}
	if after == before {
		_, err = fmt.Fprintf(o.out, "sfy %s is already the newest version.\n", after)
		return err
	}
	_, err = fmt.Fprintf(o.out, "Upgraded sfy from %s to %s.\n", before, after)
	return err
}
