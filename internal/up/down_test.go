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

	"github.com/JacobJNilsson/simple-factory/internal/agentproc"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/registration"
	"github.com/JacobJNilsson/simple-factory/internal/server"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/task"
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

// downFactory returns a factory folder with a working task t1 and a lead that runs in place at the address dev.
// The process record names the test process, so the lead counts as running.
func downFactory(t *testing.T) string {
	t.Helper()
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
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(multiplexerConfig(home, "stopped")), 0o600); err != nil {
		t.Fatal(err)
	}
	working := task.Task{ID: "t1", Title: "Work", Description: "d", Goal: "g", State: task.Working, Round: 1, Dir: parent}
	if err := store.Files(home).Create(working); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(leadAddressPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leadAddressPath(home), []byte("dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := agentproc.Self()
	if err != nil {
		t.Fatal(err)
	}
	if err := agentproc.Write(registration.PidPath(home), self); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestDownStopsSessionsWithTheTemplatesLoadedAtStart(t *testing.T) {
	home := downFactory(t)
	done := serveFactory(t, home)
	// The operator changes the config after the sfy process started.
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(multiplexerConfig(home, "changed")), 0o600); err != nil {
		t.Fatal(err)
	}

	ans, err := server.Call(socket(home), protocol.Request{Command: "down"})

	if err != nil || !ans.OK || !strings.Contains(ans.Text, "Stopped worker-t1") {
		t.Fatalf("answer %+v, %v", ans, err)
	}
	waitExited(t, done)
	if _, err := os.Stat(filepath.Join(home, "stopped-sfy-work-worker-t1")); err != nil {
		t.Errorf("the start-time stop template did not run with the prefixed name: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "changed-sfy-work-worker-t1")); err == nil {
		t.Error("the changed config's stop template ran")
	}
	if _, err := os.Lstat(socket(home)); err == nil {
		t.Error("the socket stays")
	}
}

func TestDownLeavesALeadInPlaceRunningWithItsRecords(t *testing.T) {
	home := downFactory(t)
	done := serveFactory(t, home)

	ans, err := server.Call(socket(home), protocol.Request{Command: "down"})

	if err != nil || !ans.OK {
		t.Fatalf("answer %+v, %v", ans, err)
	}
	waitExited(t, done)
	if _, err := os.Stat(filepath.Join(home, "stopped-dev")); err == nil {
		t.Error("down ran stop for the lead's address")
	}
	for _, path := range []string{leadAddressPath(home), registration.PidPath(home)} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("down removed the lead's record: %v", err)
		}
	}
}

// serveFactory runs the sfy process of home and waits until it answers. The channel gets its result.
func serveFactory(t *testing.T, home string) <-chan error {
	t.Helper()
	env := func(k string) string {
		if k == "SFY_HOME" {
			return home
		}
		return ""
	}
	done := make(chan error, 1)
	go func() { done <- Serve(context.Background(), env, io.Discard) }()
	waitAnswering(t, socket(home))
	return done
}

func waitExited(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the sfy process did not exit")
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
