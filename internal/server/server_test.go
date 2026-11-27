package server_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/server"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

type launcher struct {
	mu      sync.Mutex
	started []string
}

func (l *launcher) Start(s engine.SessionSpec) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.started = append(l.started, s.Name)
	return nil
}
func (l *launcher) startedNames() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.started...)
}
func (l *launcher) Send(string, string) error   { return nil }
func (l *launcher) Alive(string) (bool, error)  { return true, nil }
func (l *launcher) Stop(string) error           { return nil }
func (l *launcher) Notify(string, string) error { return nil }
func (l *launcher) Escape(string) error         { return nil }

type workspaces struct{}

func (workspaces) Prepare(t task.Task) (task.Prepared, error) {
	return task.Prepared{Dir: "/work/" + t.ID, Branch: t.Branch}, nil
}
func (workspaces) BranchOf(t task.Task) string            { return t.Branch }
func (workspaces) Remove(task.Task) error                 { return nil }
func (workspaces) HoldsWork(task.Task) (bool, error)      { return false, nil }
func (workspaces) RepoFolder(repo string) string          { return repo }
func (workspaces) RemoteBranch(task.Task) (string, error) { return "abc123", nil }
func (workspaces) Integrate(task.Task) error              { return nil }
func (workspaces) HasRemote(task.Task) (bool, error)      { return true, nil }

type clock struct{}

func (clock) Now() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) }

const taskFile = "# Title\n\n## Description\n\nDo it.\n\n## Goal\n\nDone.\n"

// shortDir returns a short folder, because macOS limits socket paths to about 104 bytes.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newEngine(t *testing.T) (*engine.Engine, *launcher, store.Store) {
	t.Helper()
	st := store.Files(t.TempDir())
	l := &launcher{}
	cfg := config.Config{ReviewRounds: 3, MaxWorkers: 2, ResendMinutes: 5}
	return engine.New(st, l, nil, workspaces{}, cfg, clock{}), l, st
}

type running struct {
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
	err    error
}

// stop ends the server and records the error that Serve returned.
func (r *running) stop() {
	r.once.Do(func() {
		r.cancel()
		r.err = <-r.done
	})
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func dial(socket string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(context.Background(), "unix", socket)
}

// start runs Serve in the background and returns when the socket accepts connections.
func start(t *testing.T, socket string, eng *engine.Engine, tick time.Duration, log io.Writer) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- server.Serve(ctx, socket, eng, tick, log) }()
	t.Cleanup(r.stop)
	waitForSocket(t, socket)
	return r
}

func newSocket(t *testing.T) string {
	t.Helper()
	return filepath.Join(shortDir(t), "f.sock")
}

func TestRequestReachesEngine(t *testing.T) {
	eng, _, _ := newEngine(t)
	socket := newSocket(t)
	start(t, socket, eng, time.Hour, io.Discard)
	ans, err := server.Call(socket, protocol.Request{Command: "task new", Body: taskFile})
	if err != nil {
		t.Fatal(err)
	}
	if !ans.OK || ans.Text == "" {
		t.Fatalf("want an id, got %+v", ans)
	}
}

func TestSecondServeFailsWhileFirstRuns(t *testing.T) {
	eng, _, _ := newEngine(t)
	socket := newSocket(t)
	start(t, socket, eng, time.Hour, io.Discard)
	err := server.Serve(context.Background(), socket, eng, time.Hour, io.Discard)
	if !errors.Is(err, server.ErrAlreadyRunning) {
		t.Fatalf("want ErrAlreadyRunning, got %v", err)
	}
	if _, err := server.Call(socket, protocol.Request{Command: "status"}); err != nil {
		t.Fatalf("first server must survive: %v", err)
	}
}

func TestServerRecordsItsPidWhileItRuns(t *testing.T) {
	eng, _, _ := newEngine(t)
	socket := newSocket(t)
	dir := filepath.Dir(socket)
	if _, ok := server.HolderPid(dir); ok {
		t.Fatal("a folder with no server has a holder")
	}
	r := start(t, socket, eng, time.Hour, io.Discard)

	if pid, ok := server.HolderPid(dir); !ok || pid != os.Getpid() {
		t.Errorf("holder = %d, %v, want %d", pid, ok, os.Getpid())
	}

	r.stop()
	if _, err := os.Stat(filepath.Join(dir, "sfy.pid")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the pid file stays after the server ends: %v", err)
	}
	if _, ok := server.HolderPid(dir); ok {
		t.Error("a stopped server has a holder")
	}
}

func TestStaleSocketIsReplaced(t *testing.T) {
	eng, _, _ := newEngine(t)
	socket := newSocket(t)
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	unix, ok := ln.(*net.UnixListener)
	if !ok {
		t.Fatal("not a unix listener")
	}
	unix.SetUnlinkOnClose(false)
	_ = ln.Close()
	if _, err := os.Stat(socket); err != nil {
		t.Fatal("stale file missing")
	}
	start(t, socket, eng, time.Hour, io.Discard)
	if ans, err := server.Call(socket, protocol.Request{Command: "status"}); err != nil || !ans.OK {
		t.Fatalf("got %+v, %v", ans, err)
	}
}

func TestRegularFileAtSocketPathFails(t *testing.T) {
	eng, _, _ := newEngine(t)
	socket := newSocket(t)
	if err := os.WriteFile(socket, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := server.Serve(context.Background(), socket, eng, time.Hour, io.Discard)
	if err == nil {
		t.Fatal("want an error")
	}
	if b, _ := os.ReadFile(socket); string(b) != "mine" {
		t.Fatal("Serve removed a file that is not a socket")
	}
}

func TestSocketFileIs0600(t *testing.T) {
	eng, _, _ := newEngine(t)
	socket := newSocket(t)
	start(t, socket, eng, time.Hour, io.Discard)
	info, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v", info.Mode().Perm())
	}
}

func TestMissingFolderIsCreatedWith0700(t *testing.T) {
	eng, _, _ := newEngine(t)
	dir := filepath.Join(shortDir(t), "home")
	start(t, filepath.Join(dir, "f.sock"), eng, time.Hour, io.Discard)
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode %v", info.Mode().Perm())
	}
}

func TestWideFolderIsNarrowedTo0700(t *testing.T) {
	eng, _, _ := newEngine(t)
	dir := filepath.Join(shortDir(t), "home")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	start(t, filepath.Join(dir, "f.sock"), eng, time.Hour, io.Discard)
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode %v", info.Mode().Perm())
	}
}

func TestFolderOfAnotherOwnerFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root owns /tmp")
	}
	eng, _, _ := newEngine(t)
	socket := "/tmp/sfy-owner-test.sock"
	err := server.Serve(context.Background(), socket, eng, time.Hour, io.Discard)
	if !errors.Is(err, server.ErrNotOwner) {
		t.Fatalf("want ErrNotOwner, got %v", err)
	}
	if _, err := os.Lstat(socket); err == nil {
		t.Fatal("Serve created a socket in a folder it does not own")
	}
}

func TestSocketIsRemovedOnShutdown(t *testing.T) {
	eng, _, _ := newEngine(t)
	socket := newSocket(t)
	r := start(t, socket, eng, time.Hour, io.Discard)
	r.stop()
	if r.err != nil {
		t.Fatal(r.err)
	}
	if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket file remains: %v", err)
	}
}

func TestShutdownWithSilentClient(t *testing.T) {
	eng, _, _ := newEngine(t)
	socket := newSocket(t)
	r := start(t, socket, eng, time.Hour, io.Discard)
	conn, err := dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	time.Sleep(50 * time.Millisecond)
	stopped := make(chan struct{})
	go func() { r.stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve waits for a client that sends nothing")
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket file remains: %v", err)
	}
}

func TestCallWhenNothingListens(t *testing.T) {
	_, err := server.Call(newSocket(t), protocol.Request{Command: "status"})
	var nr *server.NotRunningError
	if !errors.As(err, &nr) {
		t.Fatalf("want NotRunningError, got %v", err)
	}
}

func TestServeTicks(t *testing.T) {
	eng, l, st := newEngine(t)
	if err := st.Create(task.Task{ID: "t1", Title: "Title", Description: "Do it.", Goal: "Done.", State: task.Queued, Source: "lead"}); err != nil {
		t.Fatal(err)
	}
	start(t, newSocket(t), eng, 10*time.Millisecond, io.Discard)
	deadline := time.Now().Add(5 * time.Second)
	for len(l.startedNames()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("tick never started the queued task")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// panicEngine returns an engine whose store is nil, so Handle and Tick panic on their first read.
func panicEngine() *engine.Engine {
	return engine.New(nil, &launcher{}, nil, workspaces{}, config.Config{}, clock{})
}

func TestPanicInHandleBecomesErrorAnswer(t *testing.T) {
	socket := newSocket(t)
	start(t, socket, panicEngine(), time.Hour, io.Discard)
	ans, err := server.Call(socket, protocol.Request{Command: "task list"})
	if err != nil {
		t.Fatal(err)
	}
	if ans.OK || !strings.Contains(ans.Error, "panic") {
		t.Fatalf("want a panic error, got %+v", ans)
	}
	if _, err := server.Call(socket, protocol.Request{Command: "task list"}); err != nil {
		t.Fatalf("server must survive a panic: %v", err)
	}
}

func TestPanicInTickIsLoggedAndTicksContinue(t *testing.T) {
	var log lockedBuffer
	start(t, newSocket(t), panicEngine(), 10*time.Millisecond, &log)
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(log.String(), "panic") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("want two logged panics, log: %q", log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRequestOverTheLimitIsRefused(t *testing.T) {
	eng, _, _ := newEngine(t)
	socket := newSocket(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.ServeWithMaxLine(ctx, socket, eng, time.Hour, io.Discard, 1024) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	waitForSocket(t, socket)
	ans, err := server.Call(socket, protocol.Request{Command: "task new", Body: strings.Repeat("x", 4096)})
	if err != nil {
		t.Fatal(err)
	}
	if ans.OK || ans.Error != "request too large" {
		t.Fatalf("got %+v", ans)
	}
}
