package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

func TestRegisteredAgentCannotRunOperatorCommands(t *testing.T) {
	home := t.TempDir()
	if err := registration.Write(home, registration.Record{ID: "s-main"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"up"}, {"hooks", "install"}} {
		var out, errb bytes.Buffer
		started := false
		a := app{
			getenv: func(k string) string {
				return map[string]string{"SFY_HOME": home, "CLAUDE_CODE_SESSION_ID": "s-main"}[k]
			},
			stdout: &out,
			stderr: &errb,
			start:  func() error { started = true; return nil },
		}

		code := a.run(args)

		if code != 1 || started || !strings.Contains(errb.String(), "usage:") {
			t.Errorf("%v: code %d, started %v, stderr %q", args, code, started, errb.String())
		}
	}
}
