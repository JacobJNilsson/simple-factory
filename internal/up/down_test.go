package up

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/server"
)

// multiplexerConfig returns a config whose stop template creates <prefix>-<name> in home.
// The alive template exits 0 until the stopped-<name> file exists.
func multiplexerConfig(home, stopPrefix string) string {
	return fmt.Sprintf(`multiplexer = "files"

[multiplexers.files]
start = "true"
send = "true"
alive = "test ! -e %[1]s/stopped-{name}"
stop = "touch %[1]s/%[2]s-{name}"
`, home, stopPrefix)
}

func TestDownStopsSessionsWithTheTemplatesLoadedAtStart(t *testing.T) {
	parent, err := os.MkdirTemp("", "fu")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	// A folder other than ~/.sfy prefixes every session name.
	home := filepath.Join(parent, ".sfy-work")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte(multiplexerConfig(home, "stopped")), 0o600); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string {
		if k == "SFY_HOME" {
			return home
		}
		return ""
	}
	done := make(chan error, 1)
	go func() { done <- Serve(context.Background(), env, io.Discard) }()
	waitAnswering(t, socket(home))
	// The operator changes the config after the sfy process started.
	if err := os.WriteFile(configPath, []byte(multiplexerConfig(home, "changed")), 0o600); err != nil {
		t.Fatal(err)
	}

	ans, err := server.Call(socket(home), protocol.Request{Command: "down"})

	if err != nil || !ans.OK || !strings.Contains(ans.Text, "Stopped main") {
		t.Fatalf("answer %+v, %v", ans, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the sfy process did not exit")
	}
	if _, err := os.Stat(filepath.Join(home, "stopped-sfy-work-main")); err != nil {
		t.Errorf("the start-time stop template did not run with the prefixed name: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "changed-sfy-work-main")); err == nil {
		t.Error("the changed config's stop template ran")
	}
	if _, err := os.Lstat(socket(home)); err == nil {
		t.Error("the socket stays")
	}
}

func waitAnswering(t *testing.T, sock string) {
	t.Helper()
	for range 1000 {
		if server.Ping(sock, time.Second) == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the sfy process did not answer")
}
