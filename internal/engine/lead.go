package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/JacobJNilsson/simple-factory/internal/engine/delivery"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
)

// cancelCommands is the argument of sfy lead command that drops the held commands.
const cancelCommands = "--cancel"

// deniedCommands lists the slash commands that end or replace the lead's conversation, which sfy tracks by its id.
// /reset and /new are other names of /clear.
var deniedCommands = []string{"/clear", "/reset", "/new", "/exit", "/quit", "/logout"}

// leadCommand types the operator's slash command into the lead when it waits, and holds it otherwise.
// The argument --cancel drops the held commands instead.
func (e *Engine) leadCommand(req protocol.Request) (string, error) {
	line := strings.TrimSpace(text(req, req.Args))
	if line == cancelCommands {
		return fmt.Sprintf("Dropped %d held commands.", e.notes.CancelCommands(protocol.Lead)), nil
	}
	if err := checkLeadCommand(line); err != nil {
		return "", err
	}
	outcome, err := e.notes.Command(protocol.Lead, line)
	switch outcome {
	case delivery.Held:
		return fmt.Sprintf("The lead does not wait for input now. sfy types %s when it waits.", line), err
	case delivery.Skipped:
		return "", errors.New("no lead runs, so sfy typed nothing")
	}
	return fmt.Sprintf("Typed %s.", line), err
}

// checkLeadCommand fails unless line is one line of text that starts with "/" and names no denied command.
func checkLeadCommand(line string) error {
	if !strings.HasPrefix(line, "/") || strings.ContainsFunc(line, unicode.IsControl) {
		return errors.New("sfy lead command needs one line of text that starts with /, such as /effort high")
	}
	name := strings.ToLower(strings.Fields(line)[0])
	if slices.Contains(deniedCommands, name) {
		return fmt.Errorf("sfy lead command refuses %s, because it ends or replaces the conversation that sfy tracks", name)
	}
	return nil
}
