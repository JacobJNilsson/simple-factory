package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
)

// answerOnSocket listens on the socket of a short factory folder and answers each request with text.
// It returns the env of worker-t1 for that folder and a count of the requests that arrived.
func answerOnSocket(t *testing.T, text string) (func(string) string, func() int) {
	t.Helper()
	home, err := os.MkdirTemp("", "fc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "unix", filepath.Join(home, "sfy.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	var commands []string
	var mu sync.Mutex
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			var req protocol.Request
			if json.NewDecoder(bufio.NewReader(conn)).Decode(&req) == nil {
				mu.Lock()
				commands = append(commands, req.Command)
				mu.Unlock()
			}
			_ = json.NewEncoder(conn).Encode(protocol.Answer{OK: true, Text: text})
			_ = conn.Close()
		}
	}()
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(commands)
	}
	return envOf(map[string]string{"SFY_HOME": home, "SFY_AGENT": "worker-t1"}), count
}

func TestRunWrapsAHookAnswerForClaude(t *testing.T) {
	env, _ := answerOnSocket(t, "why")
	var out, errb bytes.Buffer

	code := Run([]string{"hook", "permission"}, env, strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`), &out, &errb)

	if code != exitOK || !strings.Contains(out.String(), `"message":"why"`) {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

func TestRunPrintsAnAnswerOfAnotherCommandAsIs(t *testing.T) {
	env, commands := answerOnSocket(t, "plain")
	var out, errb bytes.Buffer

	code := Run([]string{"status"}, env, strings.NewReader(""), &out, &errb)

	if code != exitOK || !strings.HasSuffix(out.String(), "plain\n") || commands() != 1 {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

func TestRunRefusesAFreeHookEventForAWorker(t *testing.T) {
	env, commands := answerOnSocket(t, "")
	var out, errb bytes.Buffer

	code := Run([]string{"hook", "x"}, env, strings.NewReader(""), &out, &errb)

	if code != exitFailed || !strings.Contains(errb.String(), "may not run") || commands() != 0 {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
}
