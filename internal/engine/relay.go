package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
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

// outgoing is a message of the lead to an agent of another factory, which Handle sends after it lets go of the lock.
type outgoing struct {
	factories Factories
	factory   string
	agent     string
	token     string
	body      string
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
	return &outgoing{factories: e.factories, factory: factory, agent: agent, token: req.Caller.Token, body: body}, nil
}

// relayOut sends the message to the other factory's sfy process with the caller's token. That process checks the token.
// It runs without the engine's lock, so two factories that relay to each other at once do not wait on each other.
func relayOut(o outgoing) protocol.Answer {
	self, err := o.factories.Self()
	if err != nil {
		return protocol.Answer{Error: err.Error()}
	}
	if o.factory == self {
		return protocol.Answer{Error: fmt.Sprintf("%s is this factory. Run: sfy send %s <text>", self, o.agent)}
	}
	req := protocol.Request{
		Command: protocol.RelayCommand,
		Caller:  protocol.Caller{Factory: self, Agent: protocol.Lead, Token: o.token},
		Args:    []string{o.agent},
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
	from := req.Caller.Factory
	if e.factories == nil {
		return "", errors.New("this sfy process takes no messages from other factories")
	}
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
	if k, _ := kindOf(protocol.AgentName(name)); k != lead {
		return "", fmt.Errorf("only the lead of factory %s may send to another factory", from)
	}
	if len(req.Args) == 0 || protocol.AgentName(req.Args[0]) != protocol.Lead {
		return "", errors.New("a message from another factory may reach only lead")
	}
	if req.Body == "" {
		return "", errors.New("send needs a text")
	}
	return "", e.message(protocol.Lead, from+"/"+protocol.Lead, req.Body)
}
