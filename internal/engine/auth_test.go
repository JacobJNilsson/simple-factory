package engine_test

import (
	"errors"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
)

// fakeAuth names the caller by its token, as the identity module does with the session files.
type fakeAuth struct{}

func (fakeAuth) Authenticate(c protocol.Caller) (string, error) {
	switch c.Token {
	case "":
		return "", nil
	case "worker-secret":
		return "worker-t1", nil
	}
	return "", errors.New("refused by the authenticator")
}

func authEnv(t *testing.T) env {
	t.Helper()
	v := newEnv(t)
	v.e.SetAuth(fakeAuth{})
	return v
}

func (v env) runWithToken(agent, token string) protocol.Answer {
	return v.e.Handle(protocol.Request{Caller: protocol.Caller{Agent: agent, Token: token}, Command: "status"})
}

func TestARefusalOfTheAuthenticatorBecomesAnErrorAnswer(t *testing.T) {
	v := authEnv(t)

	ans := v.runWithToken("lead", "guess")

	if ans.OK || ans.Error != "refused by the authenticator" {
		t.Fatalf("got %+v", ans)
	}
}

func TestTheTokenDecidesWhoCalls(t *testing.T) {
	v := authEnv(t)

	// task new is for the lead and the operator only, so a worker token that names no one still gets the worker's limits.
	ans := v.e.Handle(protocol.Request{Caller: protocol.Caller{Token: "worker-secret"}, Command: "task new", Body: "x"})

	if ans.OK {
		t.Fatalf("a worker token created a task: %+v", ans)
	}
}

func TestAnEngineWithoutAnAuthenticatorTrustsTheClaimedName(t *testing.T) {
	v := newEnv(t)

	if ans := v.runWithToken("lead", ""); !ans.OK {
		t.Fatalf("the claimed name was refused: %s", ans.Error)
	}
}
