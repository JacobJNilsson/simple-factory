package up

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/factory/internal/config"
	"github.com/JacobJNilsson/factory/internal/engine"
)

// fakeMux is one multiplexer for both the factory process and the agent sessions.
type fakeMux struct {
	alive map[string]bool
	// serveAnswers makes the socket answer once the factory session starts.
	serveAnswers bool
	// sessionsDie makes a started session end at once.
	sessionsDie bool
	answering   bool
	checks      int
	// answerAfter makes the socket answer after that many checks, when it is above zero.
	answerAfter int
	starts      [][]string
}

func (m *fakeMux) probe() bool {
	m.checks++
	return m.answering || (m.answerAfter > 0 && m.checks > m.answerAfter)
}

func (m *fakeMux) Start(name, dir string, command []string) error {
	m.starts = append(m.starts, append([]string{name, dir}, command...))
	m.alive[name] = !m.sessionsDie
	if name == "factory" && m.serveAnswers {
		m.answering = true
	}
	return nil
}
func (m *fakeMux) Send(string, string) error    { return nil }
func (m *fakeMux) Alive(n string) (bool, error) { return m.alive[n], nil }
func (m *fakeMux) Stop(string) error            { return nil }
func (m *fakeMux) Notify(string, string) error  { return nil }

// fakeLauncher records the main agent's spec and marks its session alive.
type fakeLauncher struct {
	*fakeMux
	specs []engine.SessionSpec
	err   error
}

func (l *fakeLauncher) Start(s engine.SessionSpec) error {
	if l.err != nil {
		return l.err
	}
	l.specs = append(l.specs, s)
	l.alive[s.Name] = true
	return nil
}

type rig struct {
	m   *fakeMux
	l   *fakeLauncher
	o   options
	out *bytes.Buffer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	m := &fakeMux{alive: map[string]bool{}, serveAnswers: true}
	l := &fakeLauncher{fakeMux: m}
	out := &bytes.Buffer{}
	return &rig{m: m, l: l, out: out, o: options{
		home: t.TempDir(), config: config.Config{Models: config.Models{Main: "opus"}},
		bin: "/bin/factory", environ: []string{"PATH=/usr/bin"},
		mux: m, launcher: l, answers: m.probe,
		wait: time.Second, poll: time.Millisecond, out: out,
	}}
}

func TestUpStartsServeAndMain(t *testing.T) {
	r := newRig(t)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(r.m.starts) != 1 || r.m.starts[0][0] != "factory" || r.m.starts[0][1] != r.o.home {
		t.Fatalf("serve starts: %v", r.m.starts)
	}
	if got := strings.Join(r.m.starts[0][2:], " "); !strings.HasSuffix(got, "/bin/factory serve") || !strings.HasPrefix(got, "env -i ") {
		t.Errorf("serve command: %q", got)
	}
	if len(r.l.specs) != 1 {
		t.Fatalf("main starts: %v", r.l.specs)
	}
	m := r.l.specs[0]
	if m.Name != "main" || m.Kind != "main" || m.Dir != r.o.home || m.Model != "opus" ||
		!strings.Contains(m.Prompt, "notes.md") || !strings.Contains(m.Prompt, "status.md") {
		t.Errorf("main spec: %+v", m)
	}
	info, err := os.Stat(filepath.Join(r.o.home, "notes.md"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("notes.md: %v %v", info, err)
	}
	if !strings.Contains(r.out.String(), "Started") {
		t.Errorf("output: %q", r.out.String())
	}
}

func TestSecondUpStartsNothing(t *testing.T) {
	r := newRig(t)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	r.out.Reset()
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(r.m.starts) != 1 || len(r.l.specs) != 1 {
		t.Errorf("second run started more: %v %v", r.m.starts, r.l.specs)
	}
	if !strings.Contains(r.out.String(), "already") {
		t.Errorf("output: %q", r.out.String())
	}
}

func TestUpCreatesAMissingHome(t *testing.T) {
	r := newRig(t)
	r.o.home = filepath.Join(r.o.home, "new", "home")
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.o.home, "notes.md")); err != nil {
		t.Error(err)
	}
}

func TestUpKeepsExistingNotes(t *testing.T) {
	r := newRig(t)
	path := filepath.Join(r.o.home, "notes.md")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "keep" {
		t.Errorf("notes.md is %q", b)
	}
}

func TestUpRestartsOnlyDeadMain(t *testing.T) {
	r := newRig(t)
	r.m.answering = true
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(r.m.starts) != 0 || len(r.l.specs) != 1 {
		t.Errorf("starts: %v %v", r.m.starts, r.l.specs)
	}
}

func TestUpWaitsForSocket(t *testing.T) {
	r := newRig(t)
	r.m.serveAnswers = false
	r.m.answerAfter = 3
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if r.m.checks < 4 {
		t.Errorf("checked %d times", r.m.checks)
	}
}

func TestUpFailsWhenSocketNeverAnswers(t *testing.T) {
	r := newRig(t)
	r.m.serveAnswers = false
	r.o.wait = 20 * time.Millisecond
	if err := run(r.o); !errors.Is(err, ErrNotAnswering) {
		t.Fatalf("err = %v", err)
	}
	if len(r.l.specs) != 0 {
		t.Error("main started without a factory process")
	}
}

func TestUpFailsAtOnceWhenServeExits(t *testing.T) {
	r := newRig(t)
	r.m.serveAnswers = false
	r.m.sessionsDie = true
	r.o.wait = time.Minute
	start := time.Now()
	if err := run(r.o); !errors.Is(err, ErrServeExited) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("up waited for the full time")
	}
}

func TestUpNamesTheStartedServeWhenMainFails(t *testing.T) {
	r := newRig(t)
	cause := errors.New("no pane")
	r.l.err = cause
	err := run(r.o)
	var mf *mainFailedError
	if !errors.As(err, &mf) || !errors.Is(err, cause) {
		t.Fatalf("err = %v", err)
	}
}

func TestServeEnvPassesOnlyTheAllowlist(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin", "HOME=/h", "USER=u", "TMPDIR=/t", "SECRET=x", "AWS_KEY=y",
		"ANTHROPIC_API_KEY=k", "CLAUDE_CONFIG_DIR=/c", "https_proxy=p", "NO_PROXY=n", "HTTP_PROXY=",
		"FACTORY_HOME=/other", "LANG=C.UTF-8", "LC_ALL=C", "SHELL=/bin/sh", "LOGNAME=u",
		"SSH_AUTH_SOCK=/s", "GH_TOKEN=g", "GITHUB_TOKEN=h", "XDG_CONFIG_HOME=/x",
	}
	want := []string{
		"FACTORY_HOME=/home", "PATH=/usr/bin", "HOME=/h", "USER=u", "TMPDIR=/t",
		"ANTHROPIC_API_KEY=k", "CLAUDE_CONFIG_DIR=/c", "https_proxy=p", "NO_PROXY=n",
		"LANG=C.UTF-8", "LC_ALL=C", "SHELL=/bin/sh", "LOGNAME=u",
		"SSH_AUTH_SOCK=/s", "GH_TOKEN=g", "GITHUB_TOKEN=h", "XDG_CONFIG_HOME=/x",
	}
	if got := serveEnv(environ, "/home"); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestServeEnvNeverPassesAnEmptyPath(t *testing.T) {
	for _, e := range serveEnv([]string{"PATH="}, "/home") {
		if strings.HasPrefix(e, "PATH") {
			t.Errorf("passed %q", e)
		}
	}
}
