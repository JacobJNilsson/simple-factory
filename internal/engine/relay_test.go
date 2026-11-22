package engine_test

import (
	"fmt"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
)

// fakeMachine connects the engines of two factories, as the sfy processes on one machine reach each other through their sockets.
type fakeMachine struct {
	self    string
	homes   map[string]string
	engines map[string]env
}

func (m fakeMachine) Self() (string, error) { return m.self, nil }

func (m fakeMachine) Call(factory string, req protocol.Request) (protocol.Answer, error) {
	v, ok := m.engines[factory]
	if !ok {
		return protocol.Answer{}, fmt.Errorf("factory %s is not running", factory)
	}
	return v.e.Handle(req), nil
}

func (m fakeMachine) Authenticate(factory string, c protocol.Caller) (string, error) {
	home, ok := m.homes[factory]
	if !ok {
		return "", fmt.Errorf("no factory %s on this machine", factory)
	}
	return identity.Folder{Home: home}.Authenticate(c)
}

// twoFactories returns the factories a and b, which check tokens with their own session folders and reach each other.
func twoFactories(t *testing.T) (a, b env) {
	t.Helper()
	a, b = newEnv(t), newEnv(t)
	homes := map[string]string{"a": a.home, "b": b.home}
	engines := map[string]env{"a": a, "b": b}
	for name, v := range engines {
		v.e.SetAuth(identity.Folder{Home: v.home})
		v.e.SetFactories(fakeMachine{self: name, homes: homes, engines: engines})
	}
	return a, b
}

func issue(t *testing.T, v env, name string) string {
	t.Helper()
	token, err := identity.Folder{Home: v.home}.Issue(name)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func sendAs(v env, agent, token, to, body string) protocol.Answer {
	return v.e.Handle(protocol.Request{Caller: protocol.Caller{Agent: agent, Token: token}, Command: "send", Args: []string{to}, Body: body})
}

// relayTo sends a relay request straight to factory v, as a process that claims to be factory a would.
func relayTo(v env, agent, token string) protocol.Answer {
	return v.e.Handle(protocol.Request{
		Command: protocol.RelayCommand, Caller: protocol.Caller{Factory: "a", Agent: agent, Token: token},
		Args: []string{"lead"}, Body: "hello",
	})
}

func TestTheLeadOfOneFactoryReachesTheLeadOfAnotherAsItsFactoryAndName(t *testing.T) {
	a, b := twoFactories(t)

	mustOK(t, sendAs(a, "lead", issue(t, a, "lead"), "b/lead", "the repo moved"))

	msgs := b.inbox(t, "lead")
	if len(msgs) != 1 || msgs[0].From != "a/lead" || msgs[0].Body != "the repo moved" {
		t.Fatalf("b's lead inbox: %+v", msgs)
	}
}

func TestTheReceiverRepliesToTheFromAddress(t *testing.T) {
	a, b := twoFactories(t)
	mustOK(t, sendAs(a, "lead", issue(t, a, "lead"), "b/lead", "the repo moved"))
	from := b.inbox(t, "lead")[0].From

	mustOK(t, sendAs(b, "lead", issue(t, b, "lead"), from, "thanks"))

	msgs := a.inbox(t, "lead")
	if len(msgs) != 1 || msgs[0].From != "b/lead" || msgs[0].Body != "thanks" {
		t.Fatalf("a's lead inbox: %+v", msgs)
	}
}

func TestARelayWithATokenOfTheReceiverClaimedAsAnotherFactoryFails(t *testing.T) {
	_, b := twoFactories(t)

	mustFail(t, relayTo(b, "lead", issue(t, b, "lead")), "no valid token")

	if msgs := b.inbox(t, "lead"); len(msgs) != 0 {
		t.Fatalf("b's lead got %+v", msgs)
	}
}

func TestARelayWithNoTokenFails(t *testing.T) {
	_, b := twoFactories(t)

	mustFail(t, relayTo(b, "lead", ""), "no session token")
	mustFail(t, relayTo(b, "", ""), "no session token")

	if msgs := b.inbox(t, "lead"); len(msgs) != 0 {
		t.Fatalf("b's lead got %+v", msgs)
	}
}

func TestARelayWithAValidWorkerTokenOfTheSenderFails(t *testing.T) {
	a, b := twoFactories(t)
	token := issue(t, a, "worker-t1")

	mustFail(t, relayTo(b, "", token), "only the lead")
	mustFail(t, relayTo(b, "lead", token), "no valid token")

	if msgs := b.inbox(t, "lead"); len(msgs) != 0 {
		t.Fatalf("b's lead got %+v", msgs)
	}
}

func TestAWorkerCannotSendToTheLeadOfAnotherFactory(t *testing.T) {
	a, b := twoFactories(t)

	mustFail(t, sendAs(a, "worker-t1", issue(t, a, "worker-t1"), "b/lead", "hi"), "not for a worker")

	if msgs := b.inbox(t, "lead"); len(msgs) != 0 {
		t.Fatalf("b's lead got %+v", msgs)
	}
}

func TestTheOperatorCannotRelay(t *testing.T) {
	a, b := twoFactories(t)

	mustFail(t, sendAs(a, "", "", "b/lead", "hi"), "SFY_HOME")

	if msgs := b.inbox(t, "lead"); len(msgs) != 0 {
		t.Fatalf("b's lead got %+v", msgs)
	}
}

func TestARelayReachesOnlyTheLeadOfTheOtherFactory(t *testing.T) {
	a, _ := twoFactories(t)

	mustFail(t, sendAs(a, "lead", issue(t, a, "lead"), "b/worker-t1", "hi"), "only lead")
}

func TestARelayToAFactoryThatDoesNotRunFails(t *testing.T) {
	a, _ := twoFactories(t)

	mustFail(t, sendAs(a, "lead", issue(t, a, "lead"), "c/lead", "hi"), "factory c is not running")
}

func TestOnlyARelayTakesACallerFromAnotherFactory(t *testing.T) {
	_, b := twoFactories(t)

	ans := b.e.Handle(protocol.Request{Command: "task new", Caller: protocol.Caller{Factory: "a"}, Body: taskFile})

	mustFail(t, ans, "another factory")
}
