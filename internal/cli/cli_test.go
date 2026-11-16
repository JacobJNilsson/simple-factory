package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/server"
	"github.com/JacobJNilsson/simple-factory/internal/store"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParse(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "t.md")
	if err := os.WriteFile(file, []byte("FILE"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want protocol.Request
	}{
		{"task new", []string{"task", "new", file}, protocol.Request{Command: "task new", Body: "FILE"}},
		{"task new stdin", []string{"task", "new", "-"}, protocol.Request{Command: "task new", Body: "IN"}},
		{"task list", []string{"task", "list"}, protocol.Request{Command: "task list"}},
		{"task show", []string{"task", "show", "t1"}, protocol.Request{Command: "task show", Args: []string{"t1"}}},
		{"task edit", []string{"task", "edit", "t1", file}, protocol.Request{Command: "task edit", Args: []string{"t1"}, Body: "FILE"}},
		{"task queue", []string{"task", "queue", "t1"}, protocol.Request{Command: "task queue", Args: []string{"t1"}}},
		{"task cancel", []string{"task", "cancel", "t1"}, protocol.Request{Command: "task cancel", Args: []string{"t1"}}},
		{"task archive", []string{"task", "archive", "t1"}, protocol.Request{Command: "task archive", Args: []string{"t1"}}},
		{"task list archived", []string{"task", "list", "--archived"}, protocol.Request{Command: "task list", Args: []string{"--archived"}}},
		{"task resume", []string{"task", "resume", "t1"}, protocol.Request{Command: "task resume", Args: []string{"t1"}}},
		{"task feedback", []string{"task", "feedback", "t1", "fix", "it"}, protocol.Request{Command: "task feedback", Args: []string{"t1", "fix", "it"}}},
		{"task complete", []string{"task", "complete", "t1"}, protocol.Request{Command: "task complete", Args: []string{"t1"}}},
		{"send", []string{"send", "worker-t1", "hello", "there"}, protocol.Request{Command: "send", Args: []string{"worker-t1", "hello", "there"}}},
		{"report done", []string{"report", "done", "all", "done"}, protocol.Request{Command: "report done", Args: []string{"all", "done"}}},
		{"report pushed", []string{"report", "pushed", "ok"}, protocol.Request{Command: "report pushed", Args: []string{"ok"}}},
		{"report blocked", []string{"report", "blocked", "stuck"}, protocol.Request{Command: "report blocked", Args: []string{"stuck"}}},
		{"report progress", []string{"report", "progress", "half"}, protocol.Request{Command: "report progress", Args: []string{"half"}}},
		{"verdict stores a path as text", []string{"verdict", "pass", file}, protocol.Request{Command: "verdict pass", Body: file}},
		{"verdict pass words", []string{"verdict", "pass", "looks", "good"}, protocol.Request{Command: "verdict pass", Body: "looks good"}},
		{"verdict fail multi-line", []string{"verdict", "fail", "fix a\n\nfix b"}, protocol.Request{Command: "verdict fail", Body: "fix a\n\nfix b"}},
		{"verdict stdin", []string{"verdict", "pass", "-"}, protocol.Request{Command: "verdict pass", Body: "IN"}},
		{"inbox", []string{"inbox"}, protocol.Request{Command: "inbox"}},
		{"escalate", []string{"escalate", "t1", "needs", "you"}, protocol.Request{Command: "escalate", Args: []string{"t1", "needs", "you"}}},
		{"status", []string{"status"}, protocol.Request{Command: "status"}},
		{"hook", []string{"hook", "turn-ended"}, protocol.Request{Command: "hook turn-ended", Body: "IN"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parse(tt.args, envOf(nil), strings.NewReader("IN"))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseCaller(t *testing.T) {
	env := envOf(map[string]string{"SFY_AGENT": "worker-t1", "SFY_TASK": "t1"})
	got, err := parse([]string{"inbox"}, env, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if got.Caller != (protocol.Caller{Agent: "worker-t1", Task: "t1"}) {
		t.Fatalf("caller %+v", got.Caller)
	}
}

// The verdict forms are not here. They take text and open no path.
func TestParseRefusesAFilePathFromAWorkerOrReviewer(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "secret")
	for _, agent := range []string{"worker-t1", "review-t1-1"} {
		for _, args := range [][]string{{"task", "new", missing}, {"task", "edit", "t1", missing}} {
			_, err := parse(args, envOf(map[string]string{"SFY_AGENT": agent}), strings.NewReader(""))
			var ue *usageError
			if !errors.As(err, &ue) || !strings.Contains(ue.msg, "standard input") {
				t.Errorf("%s %v: err = %v", agent, args, err)
			}
		}
	}
}

func TestParseReadsStdinForAWorkerAndAFileForMain(t *testing.T) {
	file := filepath.Join(t.TempDir(), "t.md")
	if err := os.WriteFile(file, []byte("FILE"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := parse([]string{"verdict", "pass", "-"}, envOf(map[string]string{"SFY_AGENT": "review-t1-1"}), strings.NewReader("IN"))
	if err != nil || w.Body != "IN" {
		t.Fatalf("reviewer: %+v, %v", w, err)
	}
	m, err := parse([]string{"task", "new", file}, envOf(map[string]string{"SFY_AGENT": "main"}), strings.NewReader(""))
	if err != nil || m.Body != "FILE" {
		t.Fatalf("main: %+v, %v", m, err)
	}
}

func TestRunBadForm(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}, {"task"}, {"task", "show"}, {"task", "list", "x"}, {"report", "done"}, {"verdict", "maybe", "f"}, {"verdict", "pass"}, {"inbox", "x"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var out, errb bytes.Buffer
			code := Run(args, envOf(nil), strings.NewReader(""), &out, &errb)
			if code != exitUsage || !strings.Contains(errb.String(), "usage:") {
				t.Errorf("code %d, stderr %q", code, errb.String())
			}
		})
	}
}

// quietLauncher accepts every call and starts nothing.
type quietLauncher struct{}

func (quietLauncher) Start(engine.SessionSpec) error { return nil }
func (quietLauncher) Send(string, string) error      { return nil }
func (quietLauncher) Alive(string) (bool, error)     { return true, nil }
func (quietLauncher) Stop(string) error              { return nil }
func (quietLauncher) Notify(string, string) error    { return nil }
func (quietLauncher) TrustWarning(string) string     { return "" }

func (quietLauncher) Deliver(string, string, string) (bool, error) { return false, nil }
func (quietLauncher) Stuck(string) bool                            { return false }
func (quietLauncher) Drain(string) (string, error)                 { return "", nil }

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) }

// liveServer starts a real server and returns the environment of a client that talks to it.
func liveServer(t *testing.T) func(string) string {
	t.Helper()
	return liveServerWith(t, quietLauncher{}, nil)
}

// warnLauncher is a quietLauncher whose trust check always warns.
type warnLauncher struct{ quietLauncher }

func (warnLauncher) TrustWarning(string) string { return "untrusted" }

// repoWorkspaces names every repo as its own folder. It supports no other call.
type repoWorkspaces struct{ engine.Workspaces }

func (repoWorkspaces) RepoFolder(repo string) string { return repo }

func liveServerWith(t *testing.T, l engine.Launcher, ws engine.Workspaces) func(string) string {
	t.Helper()
	home, err := os.MkdirTemp("", "fc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	eng := engine.New(store.Files(t.TempDir()), l, ws, config.Config{}, fixedClock{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, filepath.Join(home, "sfy.sock"), eng, time.Hour, io.Discard) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	env := envOf(map[string]string{"SFY_HOME": home})
	for i := 0; i < 1000; i++ {
		if _, err := server.Call(filepath.Join(home, "sfy.sock"), protocol.Request{Command: "status"}); err == nil {
			return env
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return nil
}

func TestRunPrintsAnswerText(t *testing.T) {
	env := liveServer(t)
	var out, errb bytes.Buffer
	code := Run([]string{"task", "new", "-"}, env, strings.NewReader("# Title\n\n## Description\n\nDo it.\n\n## Goal\n\nDone.\n"), &out, &errb)
	if code != exitOK || strings.TrimSpace(out.String()) == "" || errb.Len() != 0 {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

func TestRunPrintsAnswerError(t *testing.T) {
	env := liveServer(t)
	var out, errb bytes.Buffer
	code := Run([]string{"task", "show", "t99"}, env, strings.NewReader(""), &out, &errb)
	if code != exitFailed || !strings.Contains(errb.String(), "t99") || out.Len() != 0 {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

func TestRunNotRunning(t *testing.T) {
	home, err := os.MkdirTemp("", "fc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	var out, errb bytes.Buffer
	code := Run([]string{"status"}, envOf(map[string]string{"SFY_HOME": home}), strings.NewReader(""), &out, &errb)
	if code != exitNotRunning {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
}

func TestRunMissingFile(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"task", "new", "/nonexistent/file"}, envOf(nil), strings.NewReader(""), &out, &errb)
	if code != exitFailed {
		t.Fatalf("code %d", code)
	}
}

// escalatedTask starts a server with one escalated task and returns a function that runs sfy as a caller.
func escalatedTask(t *testing.T) func(agent, task string, args ...string) (int, string) {
	t.Helper()
	home := liveServer(t)
	run := func(agent, task string, args ...string) (int, string) {
		env := func(k string) string {
			switch k {
			case "SFY_AGENT":
				return agent
			case "SFY_TASK":
				return task
			}
			return home(k)
		}
		var out, errb bytes.Buffer
		code := Run(args, env, strings.NewReader("# Title\n\n## Description\n\nDo it.\n\n## Goal\n\nDone.\n"), &out, &errb)
		return code, errb.String()
	}
	if code, msg := run("main", "", "task", "new", "-"); code != exitOK {
		t.Fatalf("task new: %d %s", code, msg)
	}
	if code, msg := run("main", "", "escalate", "t1", "why"); code != exitOK {
		t.Fatalf("escalate: %d %s", code, msg)
	}
	return run
}

func TestRunResumeSucceedsForTheOperator(t *testing.T) {
	run := escalatedTask(t)
	if code, msg := run("", "", "task", "resume", "t1"); code != exitOK {
		t.Fatalf("code %d, stderr %s", code, msg)
	}
}

func TestRunResumeIsRefusedForAWorker(t *testing.T) {
	run := escalatedTask(t)
	code, msg := run("worker-t1", "t1", "task", "resume", "t1")
	if code != exitFailed || !strings.Contains(msg, "not for a worker") {
		t.Fatalf("code %d, stderr %q", code, msg)
	}
}

func TestTaskNewPrintsOnlyTheIdToStdoutAndTheWarningToStderr(t *testing.T) {
	env := liveServerWith(t, warnLauncher{}, repoWorkspaces{})
	var out, errb bytes.Buffer
	body := "# Title\n\n## Description\n\nDo it.\n\n## Goal\n\nDone.\n\n## Repo\n\n/some/repo\n"
	code := Run([]string{"task", "new", "-"}, env, strings.NewReader(body), &out, &errb)
	if code != exitOK || out.String() != "t1\n" {
		t.Fatalf("code %d, stdout %q", code, out.String())
	}
	if !strings.Contains(errb.String(), "untrusted") {
		t.Errorf("stderr %q", errb.String())
	}
}
