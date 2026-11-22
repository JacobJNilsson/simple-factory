package up

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/server"
)

// TestMain lets the test binary act as a sfy process, so headless tests start real detached processes.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "serve" && os.Getenv("SFY_HOME") != "" {
		os.Exit(fakeServe(os.Getenv("SFY_HOME")))
	}
	os.Exit(m.Run())
}

// fakeServe stands in for sfy serve. It takes the lock and writes the pid file as the server does,
// marks itself ready instead of listening, and exits on SIGTERM or SIGHUP after it releases both.
func fakeServe(home string) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	lock, err := os.OpenFile(filepath.Join(home, "sfy.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 1
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return 2
	}
	pidPath, ready := filepath.Join(home, "sfy.pid"), filepath.Join(home, "ready")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return 1
	}
	defer func() { _ = os.Remove(pidPath) }()
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		return 1
	}
	defer func() { _ = os.Remove(ready) }()
	<-ctx.Done()
	return 0
}

// headlessRig returns a rig whose config says headless and whose sfy process is this test binary.
// The socket counts as answering while the fake process is ready.
func headlessRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(r.o.home, "ready")
	r.o.bin = exe
	r.o.config.Headless = true
	r.o.spawn = spawnDetached
	r.o.answers = func() bool { _, err := os.Stat(ready); return err == nil }
	r.o.wait, r.o.poll = 20*time.Second, 5*time.Millisecond
	t.Cleanup(func() {
		_ = stopHeadless(r.o.home)
		for range 1000 {
			if _, held := server.HolderPid(r.o.home); !held {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Error("the sfy process did not exit")
	})
	return r
}

func holder(t *testing.T, home string) int {
	t.Helper()
	pid, ok := server.HolderPid(home)
	if !ok {
		t.Fatal("no sfy process holds the lock")
	}
	return pid
}

func TestHeadlessStartRunsDetachedInTheFactoryFolder(t *testing.T) {
	r := newRig(t)
	r.o.config.Headless = true
	var gotDir, gotLog string
	var gotArgv, gotEnv []string
	r.o.spawn = func(dir string, env, argv []string, logPath string) (detached, error) {
		gotDir, gotEnv, gotArgv, gotLog = dir, env, argv, logPath
		r.m.answering = true
		return detached{exited: make(chan struct{})}, nil
	}
	r.o.environ = []string{"PATH=/usr/bin", "SECRET=x"}

	if err := r.o.startServe(); err != nil {
		t.Fatal(err)
	}

	if len(r.m.starts) != 0 {
		t.Errorf("headless mode opened a window: %v", r.m.starts)
	}
	if gotDir != r.o.home || gotLog != filepath.Join(r.o.home, "sfy.log") || strings.Join(gotArgv, " ") != "/bin/sfy serve" {
		t.Errorf("spawn(%q, %q, %q)", gotDir, gotLog, gotArgv)
	}
	env := strings.Join(gotEnv, "\n")
	if !strings.Contains(env, "PATH=/usr/bin") || !strings.Contains(env, "SFY_HOME="+r.o.home) || strings.Contains(env, "SECRET") {
		t.Errorf("env %q", env)
	}
}

func TestHeadlessStartReportsAProcessThatExits(t *testing.T) {
	r := newRig(t)
	r.o.config.Headless = true
	r.o.answers = func() bool { return false }
	r.o.spawn = func(string, []string, []string, string) (detached, error) {
		done := make(chan struct{})
		close(done)
		return detached{exited: done}, nil
	}

	if err := r.o.startServe(); !errors.Is(err, ErrServeExited) {
		t.Errorf("error = %v, want %v", err, ErrServeExited)
	}
}

func TestHeadlessStartStartsADetachedProcessThatAnswers(t *testing.T) {
	r := headlessRig(t)

	if err := r.o.startServe(); err != nil {
		t.Fatal(err)
	}

	pid := holder(t, r.o.home)
	if pid == os.Getpid() {
		t.Fatal("the lock holder is the test process")
	}
	// A process that called setsid leads its own process group.
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
		t.Errorf("process group of %d = %d, %v, want its own group", pid, pgid, err)
	}
	if len(r.m.starts) != 0 {
		t.Errorf("headless mode opened a window: %v", r.m.starts)
	}
	if _, err := os.Stat(filepath.Join(r.o.home, "sfy.log")); err != nil {
		t.Errorf("sfy.log: %v", err)
	}
}

func TestHeadlessSecondStartRefuses(t *testing.T) {
	r := headlessRig(t)
	if err := r.o.startServe(); err != nil {
		t.Fatal(err)
	}
	first := holder(t, r.o.home)
	r.o.spawn = func(string, []string, []string, string) (detached, error) {
		t.Error("a second start spawned a process")
		return detached{}, errors.New("spawned")
	}

	if err := r.o.startServe(); err != nil {
		t.Fatal(err)
	}

	if got := holder(t, r.o.home); got != first {
		t.Errorf("holder changed from %d to %d", first, got)
	}
}

func TestHeadlessStartWaitsForAProcessThatStartsInAWindow(t *testing.T) {
	r := headlessRig(t)
	r.m.alive[serveSession] = true
	r.m.answerAfter = 3
	r.o.answers = r.m.probe
	r.o.spawn = func(string, []string, []string, string) (detached, error) {
		t.Error("a window already runs the sfy process")
		return detached{}, errors.New("spawned")
	}

	if err := r.o.startServe(); err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessRestartHandsOverToANewProcess(t *testing.T) {
	r := headlessRig(t)
	r.m.alive["lead"] = true
	if err := r.o.startServe(); err != nil {
		t.Fatal(err)
	}
	old := holder(t, r.o.home)

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	// The lock is free before the process ends, and a zombie still answers signal 0 until the parent reaps it.
	var err error
	for range 400 {
		if err = syscall.Kill(old, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !errors.Is(err, syscall.ESRCH) {
		t.Errorf("the old process %d still runs: %v", old, err)
	}
	if now := holder(t, r.o.home); now == old {
		t.Error("the same process still serves")
	}
	if !r.o.answers() {
		t.Error("the new process does not answer")
	}
	if len(r.m.starts) != 0 || len(r.m.stops) != 0 {
		t.Errorf("restart used the multiplexer: starts %v, stops %v", r.m.starts, r.m.stops)
	}
	want := "Stopped the sfy process.\nStarted the sfy process.\nTold the main agent.\n"
	if r.out.String() != want {
		t.Errorf("output %q, want %q", r.out.String(), want)
	}
}

// windowMux ends the process of the window when the window stops, as a real window hangs up its process.
type windowMux struct {
	*fakeMux
	home string
}

func (m windowMux) Stop(name string) error {
	if name == serveSession {
		if err := stopHeadless(m.home); err != nil {
			return err
		}
	}
	return m.fakeMux.Stop(name)
}

func TestRestartMovesAWindowedProcessToHeadless(t *testing.T) {
	r := headlessRig(t)
	r.m.alive["lead"] = true
	// The process started as it did before the config said headless.
	p, err := spawnDetached(r.o.home, serveEnv(r.o.environ, r.o.home), []string{r.o.bin, "serve"}, filepath.Join(r.o.home, "sfy.log"))
	if err != nil {
		t.Fatal(err)
	}
	r.m.alive[serveSession] = true
	r.o.mux = windowMux{fakeMux: r.m, home: r.o.home}
	for !r.o.answers() {
		time.Sleep(5 * time.Millisecond)
	}
	old := holder(t, r.o.home)

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	<-p.exited
	if r.m.alive[serveSession] {
		t.Error("the window still runs")
	}
	if len(r.m.stops) != 1 || r.m.stops[0] != serveSession || len(r.m.starts) != 0 {
		t.Errorf("stops %v, starts %v", r.m.stops, r.m.starts)
	}
	if now := holder(t, r.o.home); now == old || !r.o.answers() {
		t.Errorf("new holder %d (old %d), answers %v", now, old, r.o.answers())
	}
}

func TestStopHeadlessIgnoresAStalePidFile(t *testing.T) {
	r := newRig(t)
	// A pid file with no lock behind it names a process that sfy must not signal.
	if err := os.WriteFile(filepath.Join(r.o.home, "sfy.pid"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := stopHeadless(r.o.home); err != nil {
		t.Fatal(err)
	}
	if _, ok := server.HolderPid(r.o.home); ok {
		t.Error("a stale pid file counts as a running process")
	}
}
