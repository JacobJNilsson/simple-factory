package engine_test

import (
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
)

// tokens maps each token to its session, as the server's lookup of the session files does.
var tokens = map[string]string{"main-secret": "main", "worker-secret": "worker-t1"}

func authEnv(t *testing.T) env {
	t.Helper()
	v := newEnv(t)
	v.e.SetAuth(func(token string) (string, bool) {
		name, ok := tokens[token]
		return name, ok
	})
	return v
}

func (v env) runWithToken(agent, token string) protocol.Answer {
	return v.e.Handle(protocol.Request{Caller: protocol.Caller{Agent: agent, Token: token}, Command: "status"})
}

func TestAWorkerThatClaimsToBeMainIsRefused(t *testing.T) {
	v := authEnv(t)

	ans := v.runWithToken("main", "worker-secret")

	if ans.OK {
		t.Fatalf("a worker token acted as main: %+v", ans)
	}
}

func TestAClaimedNameWithoutATokenIsRefused(t *testing.T) {
	v := authEnv(t)

	for _, agent := range []string{"main", "worker-t1"} {
		if ans := v.runWithToken(agent, ""); ans.OK {
			t.Errorf("%s ran with no token: %+v", agent, ans)
		}
	}
}

func TestATokenThatNoSessionOwnsIsRefused(t *testing.T) {
	v := authEnv(t)

	if ans := v.runWithToken("worker-t1", "guess"); ans.OK {
		t.Fatalf("an unknown token was accepted: %+v", ans)
	}
}

func TestTheRightTokenAndTheOperatorPass(t *testing.T) {
	v := authEnv(t)

	for name, c := range map[string]struct{ agent, token string }{
		"worker":                {"worker-t1", "worker-secret"},
		"main":                  {"main", "main-secret"},
		"token without a name":  {"", "main-secret"},
		"operator with no name": {"", ""},
	} {
		if ans := v.runWithToken(c.agent, c.token); !ans.OK {
			t.Errorf("%s was refused: %s", name, ans.Error)
		}
	}
}

func TestTheTokenDecidesWhoCalls(t *testing.T) {
	v := authEnv(t)

	// task new is for main and the operator only, so a worker token that names no one still gets the worker's limits.
	ans := v.e.Handle(protocol.Request{Caller: protocol.Caller{Token: "worker-secret"}, Command: "task new", Body: "x"})

	if ans.OK {
		t.Fatalf("a worker token created a task: %+v", ans)
	}
}
