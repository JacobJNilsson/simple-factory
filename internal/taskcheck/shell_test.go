package taskcheck_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/taskcheck"
)

func TestStartShellOutsideTheSandboxReturnsItsDrops(t *testing.T) {
	dir := t.TempDir()
	drop := filepath.Join(t.TempDir(), "drop", "2-gate")
	r := taskcheck.Runner{Ctx: context.Background(), Timeout: time.Minute}
	got := make(chan engine.CheckResult, 1)

	r.StartShell(engine.ShellSpec{Dir: dir, Command: "printf 'To: lead\\n\\nA gap.\\n' > " + drop + "/a.md", Drop: drop}, func(res engine.CheckResult) { got <- res })

	res := <-got
	if res.Err != nil {
		t.Fatalf("command failed: %v: %s", res.Err, res.Output)
	}
	if len(res.Drops) != 1 || string(res.Drops[0]) != "To: lead\n\nA gap.\n" {
		t.Fatalf("drops = %q, want the one file", res.Drops)
	}
}
