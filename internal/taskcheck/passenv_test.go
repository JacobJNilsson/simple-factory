package taskcheck_test

import (
	"context"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/taskcheck"
)

func TestStartShellWithAPassEnvListKeepsOnlyThoseVariables(t *testing.T) {
	t.Setenv("READ_TOKEN", "r")
	t.Setenv("WRITE_TOKEN", "w")
	r := taskcheck.Runner{Ctx: context.Background(), Timeout: time.Minute}
	got := make(chan engine.CheckResult, 1)

	spec := engine.ShellSpec{Dir: t.TempDir(), Command: `printf '%s/%s' "$READ_TOKEN" "$WRITE_TOKEN"`, PassEnv: &[]string{"READ_TOKEN"}}
	r.StartShell(spec, func(res engine.CheckResult) { got <- res })

	if res := <-got; res.Err != nil || res.Output != "r/" {
		t.Fatalf("output %q, %v, want only the passed variable", res.Output, res.Err)
	}
}
