package up

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/server"
)

// machine reaches the factories on this machine by their names, for the relay of messages between leads.
type machine struct {
	// home is the factory folder of this factory.
	home string
	env  func(string) string
}

// Self returns this factory's name. A factory folder outside the factories folder has no name that another factory can find.
func (m machine) Self() (string, error) {
	name, _ := config.Describe(m.home, m.env)
	found, ok := config.FactoryHome(m.env("HOME"), name)
	if !ok || filepath.Clean(found) != filepath.Clean(m.home) {
		return "", fmt.Errorf("other factories cannot find the factory folder %s by name, because it is not in %s", m.home, config.FactoriesDir(m.env("HOME")))
	}
	return name, nil
}

// Call sends req to the sfy process of the named factory.
func (m machine) Call(factory string, req protocol.Request) (protocol.Answer, error) {
	home, err := m.find(factory)
	if err != nil {
		return protocol.Answer{}, err
	}
	ans, err := server.Call(socket(home), req)
	var down *server.NotRunningError
	if errors.As(err, &down) {
		return protocol.Answer{}, fmt.Errorf("factory %s is not running", factory)
	}
	return ans, err
}

// Authenticate checks the caller's token against the session tokens of the named factory.
func (m machine) Authenticate(factory string, c protocol.Caller) (string, error) {
	home, err := m.find(factory)
	if err != nil {
		return "", err
	}
	return identity.Folder{Home: home}.Authenticate(c)
}

// noFactoryError reports a factory name that no factory folder on this machine has.
type noFactoryError struct{ name string }

func (e *noFactoryError) Error() string { return fmt.Sprintf("no factory %s on this machine", e.name) }

func (m machine) find(factory string) (string, error) {
	home, ok := config.FactoryHome(m.env("HOME"), factory)
	if !ok {
		return "", &noFactoryError{name: factory}
	}
	return home, nil
}
