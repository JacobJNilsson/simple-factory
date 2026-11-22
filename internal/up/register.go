package up

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/prompts"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

// ErrNoSession reports that sfy register ran outside an agent session.
var ErrNoSession = errors.New("sfy cannot tell which agent session runs this command. Run sfy register inside the agent session that becomes the lead")

// ErrLeadRuns reports that sfy register found a lead.
var ErrLeadRuns = errors.New("a lead already runs. sfy register works only when no lead runs")

// ErrNoWhoami reports a template set that cannot name the terminal of the agent.
var ErrNoWhoami = errors.New("the template set needs a whoami template that prints the address of this terminal, so sfy can reach the agent. Add one to the set")

// registerIntro is the first line of the prompt that sfy register prints.
const registerIntro = "You are now the sfy lead. Follow the prompt below as your role from now on."

// Register makes the agent session that runs it the lead. The agent reads the printed prompt as its role.
func Register(environ []string, env func(string) string, stdout, stderr io.Writer) error {
	o, err := newOptions(environ, env, stdout, stderr)
	if err != nil {
		return err
	}
	return register(o)
}

// register refuses when a lead runs or the set cannot name this terminal.
// Otherwise it starts the sfy process when it does not run, records the address and the session, and prints the prompt.
func register(o options) error {
	id := o.session()
	if id == "" {
		return ErrNoSession
	}
	if err := o.moveToLead(); err != nil {
		return err
	}
	alive, err := o.launcher.Alive(leadSession)
	if err != nil {
		return err
	}
	if alive {
		return ErrLeadRuns
	}
	address, err := o.whoamiAddress()
	if err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.home, 0o700); err != nil {
		return err
	}
	if err := ensureNotes(filepath.Join(o.home, "notes.md")); err != nil {
		return err
	}
	if !o.answers() {
		if err := o.startServe(); err != nil {
			return err
		}
	}
	if _, err := recordLeadPrompt(o.home, o.layers); err != nil {
		return err
	}
	if _, err := (identity.Folder{Home: o.home}).Issue(leadSession); err != nil {
		return err
	}
	if err := o.record(address, registration.Record{ID: id, Dir: dir}); err != nil {
		return err
	}
	text, err := prompts.For(protocol.Lead, o.layers)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(o.out, "%s\n\n%s\n\n%s\n", registerIntro, text, leadPrompt(o.home))
	return err
}

// whoamiAddress returns the address of this terminal. It fails when the set cannot name it.
func (o options) whoamiAddress() (string, error) {
	if o.whoami == nil {
		return "", ErrNoWhoami
	}
	address, err := o.whoami()
	if err != nil {
		return "", fmt.Errorf("whoami failed: %w", err)
	}
	if address == "" {
		return "", ErrNoWhoami
	}
	return address, nil
}

// record writes the address and the registration. A failed write leaves neither file.
func (o options) record(address string, r registration.Record) error {
	if err := os.MkdirAll(filepath.Dir(leadAddressPath(o.home)), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(leadAddressPath(o.home), []byte(address+"\n"), 0o600); err != nil {
		return err
	}
	if err := registration.Write(o.home, r); err != nil {
		return errors.Join(err, registration.Folder{Home: o.home}.Clear())
	}
	return nil
}
