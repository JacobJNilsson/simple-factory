package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// Factories reaches the other factories on this machine.
type Factories interface {
	// Self returns the name that other factories use for this factory. It fails when they cannot find this factory by that name.
	Self() (string, error)
	// Call sends a request to the sfy process of the named factory.
	Call(factory string, req protocol.Request) (protocol.Answer, error)
	// Authenticate returns the session name that the caller proves with a token from the sessions folder of the named factory.
	Authenticate(factory string, c protocol.Caller) (string, error)
}

// SetFactories lets the lead send to other factories and lets this factory take their relays. Without it, both fail.
func (e *Engine) SetFactories(f Factories) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.factories = f
}

// outgoing is a request of the lead to another factory, which Handle sends after it lets go of the lock.
type outgoing struct {
	factories Factories
	factory   string
	// command is the relay request, and args are its arguments.
	command string
	args    []string
	token   string
	body    string
	// selfHint is the command that the error names when the target is this factory.
	selfHint string
}

// relayTarget splits a send target of the form factory/agent.
func relayTarget(req protocol.Request) (factory, agent string, ok bool) {
	if req.Command != "send" || len(req.Args) == 0 {
		return "", "", false
	}
	return strings.Cut(req.Args[0], "/")
}

// prepareRelay checks a send to another factory and returns what relayOut sends. Only the lead may send there.
func (e *Engine) prepareRelay(req protocol.Request, factory, agent string) (*outgoing, error) {
	if req.Caller.Agent == "" {
		return nil, fmt.Errorf("the operator has no session token to relay with. Run: SFY_HOME=<factory folder of %s> sfy send lead <text>", factory)
	}
	if factory == "" || agent == "" {
		return nil, errors.New("an address in another factory has the form <factory>/<agent>, such as default/lead")
	}
	body := text(req, req.Args[1:])
	if body == "" {
		return nil, errors.New("send needs a text")
	}
	if e.factories == nil {
		return nil, errors.New("this sfy process cannot reach other factories")
	}
	return &outgoing{
		factories: e.factories, factory: factory, command: protocol.RelayCommand, args: []string{agent},
		token: req.Caller.Token, body: body, selfHint: "sfy send " + agent + " <text>",
	}, nil
}

// taskRelayTarget returns the factory of a "task new --factory <factory>" request.
func taskRelayTarget(req protocol.Request) (factory string, ok bool) {
	if req.Command != "task new" || len(req.Args) == 0 || req.Args[0] != "--factory" {
		return "", false
	}
	if len(req.Args) < 2 {
		return "", true
	}
	return req.Args[1], true
}

// prepareTaskRelay checks a task for another factory and returns what relayOut sends. Only the lead may create one there.
func (e *Engine) prepareTaskRelay(req protocol.Request, factory string) (*outgoing, error) {
	if req.Caller.Agent == "" {
		return nil, fmt.Errorf("the operator has no session token to relay with. Run: SFY_HOME=<factory folder of %s> sfy task new <file>", factory)
	}
	if factory == "" {
		return nil, errors.New("task new --factory needs the name of a factory")
	}
	if req.Body == "" {
		return nil, errors.New("task new needs a task file")
	}
	if e.factories == nil {
		return nil, errors.New("this sfy process cannot reach other factories")
	}
	return &outgoing{
		factories: e.factories, factory: factory, command: protocol.RelayTaskCommand,
		token: req.Caller.Token, body: req.Body, selfHint: "sfy task new <file>",
	}, nil
}

// relayOut sends the request to the other factory's sfy process with the caller's token. That process checks the token.
// It runs without the engine's lock, so two factories that relay to each other at once do not wait on each other.
func relayOut(o outgoing) protocol.Answer {
	self, err := o.factories.Self()
	if err != nil {
		return protocol.Answer{Error: err.Error()}
	}
	if o.factory == self {
		return protocol.Answer{Error: fmt.Sprintf("%s is this factory. Run: %s", self, o.selfHint)}
	}
	req := protocol.Request{
		Command: o.command,
		Caller:  protocol.Caller{Factory: self, Agent: protocol.Lead, Token: o.token},
		Args:    o.args,
		Body:    o.body,
	}
	ans, err := o.factories.Call(o.factory, req)
	if err != nil {
		return protocol.Answer{Error: err.Error()}
	}
	return ans
}

// relay takes a message that another factory's sfy process passes on. The token must belong to that factory's lead.
// The message names the sender as <factory>/lead, so it never shows as the operator's.
func (e *Engine) relay(req protocol.Request) (string, error) {
	if e.factories == nil {
		return "", errors.New("this sfy process takes no messages from other factories")
	}
	from, err := e.relaySender(req)
	if err != nil {
		return "", err
	}
	if len(req.Args) == 0 || protocol.AgentName(req.Args[0]) != protocol.Lead {
		return "", errors.New("a message from another factory may reach only lead")
	}
	if req.Body == "" {
		return "", errors.New("send needs a text")
	}
	return "", e.message(protocol.Lead, from+"/"+protocol.Lead, req.Body)
}

// relayTask takes a task that another factory's sfy process passes on. The token must belong to that factory's lead.
// The task starts in new with the source <factory>/lead, so this factory's lead prepares it. The answer is the id, then any warning.
func (e *Engine) relayTask(req protocol.Request) (string, error) {
	if e.factories == nil {
		return "", errors.New("this sfy process takes no tasks from other factories")
	}
	from, err := e.relaySender(req)
	if err != nil {
		return "", err
	}
	t, err := task.Parse(req.Body)
	if err != nil {
		return "", err
	}
	t, warning, err := e.createTask(t, from+"/"+protocol.Lead)
	if err != nil {
		return "", err
	}
	if warning != "" {
		return t.ID + "\n" + warning, nil
	}
	return t.ID, nil
}

// leadOf describes a factory source such as a/lead as "the lead of factory a".
func leadOf(source string) string {
	factory, _, _ := strings.Cut(source, "/")
	return "the lead of factory " + factory
}

// relaySender checks that a relay comes from the lead of another factory and returns that factory's name.
// The caller checks first that this process has Factories, because its error names what the relay brings.
func (e *Engine) relaySender(req protocol.Request) (string, error) {
	from := req.Caller.Factory
	if from == "" {
		return "", errors.New("a relay needs the factory of its sender")
	}
	if self, err := e.factories.Self(); err == nil && from == self {
		return "", errors.New("a relay must come from another factory")
	}
	if req.Caller.Token == "" {
		return "", fmt.Errorf("the relay from factory %s carries no session token", from)
	}
	name, err := e.factories.Authenticate(from, protocol.Caller{Agent: req.Caller.Agent, Token: req.Caller.Token})
	if err != nil {
		return "", fmt.Errorf("the relay from factory %s carries no valid token of that factory: %w", from, err)
	}
	if protocol.AgentName(name) != protocol.Lead {
		return "", fmt.Errorf("only the lead of factory %s may send to another factory", from)
	}
	return from, nil
}
