package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude acts like the agent CLI. It runs the Stop hook command from its settings file after each turn.
const fakeClaude = `#!/bin/sh
settings=
while [ $# -gt 0 ]; do
	if [ "$1" = "--settings" ]; then settings=$2; fi
	shift
done
stop_hook() {
	cmd=$(grep 'hook turn-ended' "$settings" | sed 's/.*"command": "\(.*\)",*$/\1/')
	eval "$cmd" </dev/null
}
case "$FACTORY_AGENT" in
worker-*)
	factory report done "done"
	stop_hook
	;;
review-*)
	factory verdict pass - <<'VERDICT'
The result meets the goal.
VERDICT
	stop_hook
	;;
esac
exec sleep 600
`

// runFactory runs the built binary as the named agent, with standard input, and returns its output.
type runFactory func(agent, stdin string, args ...string) (string, error)

// newRig builds the binary and prepares a private home and a private tmux server.
// It skips the test when tmux is missing.
func newRig(t *testing.T) (home string, factory runFactory) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH")
	}
	tmp, err := os.MkdirTemp("", "fe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	home = filepath.Join(tmp, "h")
	binDir := filepath.Join(tmp, "b")
	fakeDir := filepath.Join(tmp, "f")
	for _, d := range []string{home, binDir, fakeDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(binDir, "factory")
	if out, err := exec.CommandContext(context.Background(), "go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(fakeDir, "claude"), []byte(fakeClaude), 0o700); err != nil {
		t.Fatal(err)
	}

	sock := fmt.Sprintf("factory-e2e-%d", rand.Uint32())
	// The -f /dev/null option keeps the operator's tmux.conf from loading.
	tm := "tmux -f /dev/null -L " + sock
	config := `multiplexer = "tmuxtest"
[multiplexers.tmuxtest]
start = "` + tm + ` new-session -d -s {name} -c {dir} -- {command}"
send = "` + tm + ` send-keys -t ={name}: -l -- {text} && ` + tm + ` send-keys -t ={name}: Enter"
alive = "` + tm + ` has-session -t ={name}"
stop = "` + tm + ` kill-session -t ={name}"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	// Only the private tmux server dies here. The operator's own server is never touched.
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "tmux", "-L", sock, "kill-server").Run()
	})

	env := append(os.Environ(),
		"FACTORY_HOME="+home, "TMUX=",
		"PATH="+fakeDir+string(os.PathListSeparator)+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	return home, func(agent, stdin string, args ...string) (string, error) {
		cmd := exec.CommandContext(context.Background(), bin, args...)
		cmd.Env = append(append([]string{}, env...), "FACTORY_AGENT="+agent)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
}

func TestOneTaskReachesDoneThroughTheLoop(t *testing.T) {
	start := time.Now()
	home, factory := newRig(t)

	if out, err := factory("", "", "up"); err != nil {
		t.Fatalf("up: %v\n%s", err, out)
	}
	if out, err := factory("", "", "up"); err != nil || !strings.Contains(out, "Everything already runs.") {
		t.Fatalf("second up: %v\n%s", err, out)
	}
	task := "# Write a note\n\n## Description\n\nWrite one line.\n\n## Goal\n\nA note exists.\n"
	if out, err := factory("", task, "task", "new", "-"); err != nil {
		t.Fatalf("task new: %v\n%s", err, out)
	}
	if out, err := factory("main", "", "task", "queue", "t1"); err != nil {
		t.Fatalf("task queue: %v\n%s", err, out)
	}

	var shown string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		shown, _ = factory("", "", "task", "show", "t1")
		if strings.Contains(shown, "State: done\n") && strings.Contains(shown, "## Verdict, round 1: pass") {
			t.Logf("loop finished in %s", time.Since(start))
			if _, err := os.Stat(filepath.Join(home, "factory.log")); err != nil {
				t.Errorf("factory.log: %v", err)
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	status, _ := factory("", "", "status")
	t.Fatalf("task t1 did not finish in 30 seconds.\ntask show:\n%s\nstatus:\n%s", shown, status)
}
