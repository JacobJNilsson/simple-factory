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

	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
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
	# Exit code 2 keeps the turn going, as in Claude Code, so the next turn end runs the hook again.
	for i in 1 2 3 4 5 6 7 8; do
		eval "$cmd" </dev/null
		[ $? -eq 2 ] || break
	done
}
case "$SFY_AGENT" in
worker-*)
	sfy report done "done"
	stop_hook
	;;
review-*)
	sfy verdict pass 'The result meets the goal.'
	stop_hook
	;;
esac
exec sleep 600
`

// runSfy runs the built binary as the named agent, with standard input, and returns its output.
type runSfy func(agent, stdin string, args ...string) (string, error)

// withoutSfyVars drops every SFY_ variable, so a session that runs the test does not leak its identity into the sfy processes.
func withoutSfyVars(env []string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, "SFY_") {
			out = append(out, kv)
		}
	}
	return out
}

// newRig builds the binary and prepares a private home and a private tmux server.
// It skips the test when tmux is missing.
func newRig(t *testing.T) (home string, sfy runSfy) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH")
	}
	// macOS limits a socket path to 104 bytes, so use TMPDIR only when it leaves room for the tmux socket name.
	base := os.TempDir()
	if len(base) > 40 {
		base = "/tmp"
	}
	sockDir, err := os.MkdirTemp(base, "fx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	// A private socket folder keeps tmux off the shared /tmp, which a sandbox may forbid writing.
	t.Setenv("TMUX_TMPDIR", sockDir)
	skipIfTmuxCannotStartPane(t)
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
	bin := filepath.Join(binDir, "sfy")
	if out, err := exec.CommandContext(context.Background(), "go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	// The sandbox preflight looks for bwrap and socat and runs bwrap once, so fakes keep the test independent of the machine.
	for name, script := range map[string]string{"claude": fakeClaude, "bwrap": "#!/bin/sh\nexit 0\n", "socat": "#!/bin/sh\nexit 0\n"} {
		if err := os.WriteFile(filepath.Join(fakeDir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	sock := fmt.Sprintf("sfy-e2e-%d", rand.Uint32())
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

	env := append(withoutSfyVars(os.Environ()),
		"SFY_HOME="+home, "TMUX=",
		"PATH="+fakeDir+string(os.PathListSeparator)+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	return home, func(agent, stdin string, args ...string) (string, error) {
		cmd := exec.CommandContext(context.Background(), bin, args...)
		cmd.Env = append(append([]string{}, env...), "SFY_AGENT="+agent)
		if agent != "" {
			cmd.Env = append(cmd.Env, "SFY_TOKEN="+claude.ReadToken(home, agent))
		}
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
}

func TestOneTaskReachesDoneThroughTheLoop(t *testing.T) {
	start := time.Now()
	home, sfy := newRig(t)

	if out, err := sfy("", ""); err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	if out, err := sfy("", ""); err != nil || !strings.Contains(out, "The main agent already runs and was shown.") {
		t.Fatalf("second start: %v\n%s", err, out)
	}
	task := "# Write a note\n\n## Description\n\nWrite one line.\n\n## Goal\n\nA note exists.\n"
	if out, err := sfy("", task, "task", "new", "-"); err != nil {
		t.Fatalf("task new: %v\n%s", err, out)
	}
	if out, err := sfy("main", "", "task", "queue", "t1"); err != nil {
		t.Fatalf("task queue: %v\n%s", err, out)
	}

	var shown string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		shown, _ = sfy("", "", "task", "show", "t1")
		if strings.Contains(shown, "State: approved\n") && strings.Contains(shown, "## Verdict, round 1: pass") {
			if out, err := sfy("main", "", "task", "complete", "t1"); err != nil {
				t.Fatalf("task complete: %v\n%s", err, out)
			}
			if shown, _ = sfy("", "", "task", "show", "t1"); !strings.Contains(shown, "State: done\n") {
				t.Fatalf("task t1 is not done after complete:\n%s", shown)
			}
			t.Logf("loop finished in %s", time.Since(start))
			if _, err := os.Stat(filepath.Join(home, "sfy.log")); err != nil {
				t.Errorf("sfy.log: %v", err)
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	status, _ := sfy("", "", "status")
	log, _ := os.ReadFile(filepath.Join(home, "sfy.log"))
	t.Fatalf("task t1 did not finish in 30 seconds.\ntask show:\n%s\nstatus:\n%s\nsfy.log:\n%s", shown, status, log)
}

// skipIfTmuxCannotStartPane starts a throwaway tmux session and skips the test when tmux cannot fork a pane.
// Any other start failure fails the test.
func skipIfTmuxCannotStartPane(t *testing.T) {
	t.Helper()
	sock := fmt.Sprintf("sfy-probe-%d", rand.Uint32())
	out, err := exec.CommandContext(context.Background(), "tmux", "-f", "/dev/null", "-L", sock, "new-session", "-d", "-s", "probe", "--", "sleep", "5").CombinedOutput()
	_ = exec.CommandContext(context.Background(), "tmux", "-L", sock, "kill-server").Run()
	if err == nil {
		return
	}
	msg := strings.TrimSpace(string(out))
	if tmuxForkBlocked(msg) {
		t.Skipf("tmux cannot start a pane here, for example inside a sandbox: %s", msg)
	}
	t.Fatalf("tmux probe session failed: %v: %s", err, msg)
}

// tmuxForkBlocked reports whether a tmux start error says the system refused to fork a pane.
func tmuxForkBlocked(msg string) bool {
	return strings.Contains(msg, "fork failed") && strings.Contains(msg, "Operation not permitted")
}

func TestTmuxForkBlocked(t *testing.T) {
	cases := map[string]bool{
		"create window failed: fork failed: Operation not permitted": true,
		"create window failed: fork failed: Resource busy":           false,
		"error connecting: Operation not permitted":                  false,
		"": false,
	}
	for msg, want := range cases {
		if got := tmuxForkBlocked(msg); got != want {
			t.Errorf("tmuxForkBlocked(%q) = %v, want %v", msg, got, want)
		}
	}
}
