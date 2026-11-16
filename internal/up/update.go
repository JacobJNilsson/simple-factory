package up

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

// updater holds the two steps of sfy restart --update, so that tests replace them.
type updater struct {
	// brew runs brew with the given arguments and returns what brew printed.
	brew func(environ []string, args ...string) ([]byte, error)
	// rerun runs sfy restart through the upgraded binary.
	rerun func(environ []string, stdout, stderr io.Writer) error
}

// systemUpdater runs the real brew and the real upgraded binary.
func systemUpdater() updater { return updater{brew: runBrew, rerun: runNewBinary} }

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

// runNewBinary runs sfy restart through the sfy binary that PATH names, which is the upgraded one.
// The new binary reads and validates the config. The error is the text that it printed.
func runNewBinary(environ []string, stdout, stderr io.Writer) error {
	path, err := exec.LookPath("sfy")
	if err != nil {
		return err
	}
	var errBuf bytes.Buffer
	cmd := exec.CommandContext(context.Background(), path, "restart")
	cmd.Env = environ
	cmd.Stdout = stdout
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		text := strings.TrimPrefix(strings.TrimSpace(errBuf.String()), "sfy: ")
		if text == "" {
			return err
		}
		return errors.New(text)
	}
	_, _ = io.Copy(stderr, &errBuf)
	return nil
}

// updateAndRestart upgrades sfy before it reads the config, because a config written for the new version can fail the old one.
// A failed upgrade stops before anything restarts. After the upgrade, the new binary restarts sfy and validates the config.
func (u updater) updateAndRestart(environ []string, stdout, stderr io.Writer) error {
	if err := upgrade(options{environ: environ, brew: u.brew, out: stdout}); err != nil {
		return err
	}
	return u.rerun(environ, stdout, stderr)
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
