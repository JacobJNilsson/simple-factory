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

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/envrun/envruntest"
)

// fakeMux is one multiplexer for both the sfy process and the agent sessions.
type fakeMux struct {
	alive map[string]bool
	// serveAnswers makes the socket answer once the sfy session starts.
	serveAnswers bool
	// sessionsDie makes a started session end at once.
	sessionsDie bool
	answering   bool
	checks      int
	// answerAfter makes the socket answer after that many checks, when it is above zero.
	answerAfter int
	starts      [][]string
	// sends lists each Send as a name and its text.
	sends [][2]string
	// stops lists the sessions that Stop ended. stubborn makes the socket answer after the sfy session stops.
	stops    []string
	stubborn bool
	// shownMain lists the sessions that ShowMain showed.
	shownMain []string
}

func (m *fakeMux) probe() bool {
	m.checks++
	return m.answering || (m.answerAfter > 0 && m.checks > m.answerAfter)
}

func (m *fakeMux) Start(name, dir string, command []string) error {
	m.starts = append(m.starts, append([]string{name, dir}, command...))
	m.alive[name] = !m.sessionsDie
	if name == "sfy" && m.serveAnswers {
		m.answering = true
	}
	return nil
}
func (m *fakeMux) Send(name, text string) error {
	m.sends = append(m.sends, [2]string{name, text})
	return nil
}
func (m *fakeMux) Alive(n string) (bool, error) { return m.alive[n], nil }
func (m *fakeMux) Stop(name string) error {
	m.stops = append(m.stops, name)
	if name == "sfy" && !m.stubborn {
		m.answering = false
	}
	m.alive[name] = false
	return nil
}
func (m *fakeMux) Notify(string, string) error { return nil }
func (m *fakeMux) Show(string) error           { return nil }
func (m *fakeMux) ShowMain(name string) error {
	m.shownMain = append(m.shownMain, name)
	return nil
}

// fakeLauncher records the main agent's spec and marks its session alive.
type fakeLauncher struct {
	*fakeMux
	specs []engine.SessionSpec
	err   error
	// warning is what TrustWarning returns, and trustAsked lists the folders it was asked about.
	warning    string
	trustAsked []string
}

func (l *fakeLauncher) TrustWarning(dir string) string {
	l.trustAsked = append(l.trustAsked, dir)
	return l.warning
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
	err *bytes.Buffer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	m := &fakeMux{alive: map[string]bool{}, serveAnswers: true}
	l := &fakeLauncher{fakeMux: m}
	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	return &rig{m: m, l: l, out: out, err: errb, o: options{
		home: t.TempDir(), config: config.Config{Models: config.Models{Main: "opus"}},
		bin: "/bin/sfy", environ: []string{"PATH=/usr/bin"},
		mux: m, launcher: l, answers: m.probe,
		wait: time.Second, poll: time.Millisecond, out: out, warn: errb,
	}}
}

func TestUpStartsServeAndMain(t *testing.T) {
	r := newRig(t)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(r.m.starts) != 1 || r.m.starts[0][0] != "sfy" || r.m.starts[0][1] != r.o.home {
		t.Fatalf("serve starts: %v", r.m.starts)
	}
	if got := strings.Join(r.m.starts[0][2:], " "); !strings.HasPrefix(got, "/bin/sfy run -i ") || !strings.HasSuffix(got, " -- /bin/sfy serve") {
		t.Errorf("serve command: %q", got)
	}
	if len(r.l.specs) != 1 {
		t.Fatalf("main starts: %v", r.l.specs)
	}
	m := r.l.specs[0]
	if m.Name != "main" || m.Kind != "main" || m.Dir != r.o.home || m.Model != "opus" ||
		!strings.Contains(m.Prompt, "notes.md") || !strings.Contains(m.Prompt, "status.md") ||
		!strings.Contains(m.Prompt, r.o.home) {
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
	if r.out.String() != "The main agent already runs and was shown.\n" {
		t.Errorf("output: %q", r.out.String())
	}
	if !reflect.DeepEqual(r.m.shownMain, []string{"main"}) {
		t.Errorf("shown %v, want the main agent once", r.m.shownMain)
	}
}

func TestUpWithServeStartedAndMainRunningNamesBoth(t *testing.T) {
	r := newRig(t)
	r.m.alive["main"] = true
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	want := "Started the sfy process.\nThe main agent already runs and was shown.\n"
	if r.out.String() != want {
		t.Errorf("output %q, want %q", r.out.String(), want)
	}
}

func TestUpDoesNotShowANewMainAgentTwice(t *testing.T) {
	r := newRig(t)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(r.m.shownMain) != 0 {
		t.Errorf("up showed a main agent that the launcher already showed: %v", r.m.shownMain)
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
		t.Error("main started without a sfy process")
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
		"SFY_HOME=/other", "LANG=C.UTF-8", "LC_ALL=C", "SHELL=/bin/sh", "LOGNAME=u",
		"SSH_AUTH_SOCK=/s", "GH_TOKEN=g", "GITHUB_TOKEN=h", "XDG_CONFIG_HOME=/x",
	}
	want := []string{
		"SFY_HOME=/home", "PATH=/usr/bin", "HOME=/h", "USER=u", "TMPDIR=/t",
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

// serveRun holds what startedServe observed: the parsed command, its raw arguments, and the sfy home.
type serveRun struct {
	parsed  envruntest.Command
	command []string
	home    string
}

// startedServe runs up with a token in the environment and returns the parsed serve command.
func startedServe(t *testing.T) serveRun {
	t.Helper()
	r := newRig(t)
	r.o.environ = []string{"PATH=/usr/bin", "GITHUB_TOKEN=s3cret-token"}
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	command := r.m.starts[0][2:]
	return serveRun{parsed: envruntest.Parse(t, command), command: command, home: r.o.home}
}

func TestServeCommandHoldsNoEnvironmentValue(t *testing.T) {
	s := startedServe(t)

	if strings.Contains(strings.Join(s.command, " "), "s3cret-token") {
		t.Errorf("command holds the token: %v", s.command)
	}
}

func TestServeEnvFileHasMode0600(t *testing.T) {
	s := startedServe(t)

	info, err := os.Stat(s.parsed.File)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("env file %v, %v", info, err)
	}
}

func TestServeEnvFileHoldsTheServeEnvironment(t *testing.T) {
	s := startedServe(t)

	if want := []string{"SFY_HOME=" + s.home, "PATH=/usr/bin", "GITHUB_TOKEN=s3cret-token"}; !reflect.DeepEqual(s.parsed.Env, want) {
		t.Errorf("env file holds %q", s.parsed.Env)
	}
}

func TestFailedServeStartLeavesNoEnvFile(t *testing.T) {
	r := newRig(t)
	r.o.environ = []string{"PATH=/usr/bin", "GITHUB_TOKEN=s3cret-token"}
	r.o.mux = failingMux{r.m}

	if err := run(r.o); err == nil {
		t.Fatal("run succeeded")
	}

	left, _ := os.ReadDir(filepath.Join(r.o.home, "sessions", "sfy"))
	if len(left) != 0 {
		t.Errorf("env files stay: %v", left)
	}
}

// failingMux fails every start.
type failingMux struct{ *fakeMux }

func (failingMux) Start(string, string, []string) error { return errors.New("no pane") }

func TestUpWarnsWhenHomeIsNotTrusted(t *testing.T) {
	r := newRig(t)
	r.l.warning = "some warning"
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(r.l.trustAsked) != 1 || r.l.trustAsked[0] != r.o.home {
		t.Errorf("asked %v, want the sfy home", r.l.trustAsked)
	}
	if !strings.Contains(r.err.String(), "some warning") {
		t.Errorf("stderr: %q", r.err.String())
	}
	if len(r.l.specs) != 1 || r.out.Len() == 0 {
		t.Errorf("up must still start the main agent: %v, stdout %q", r.l.specs, r.out.String())
	}
}

func TestUpSaysNothingWhenHomeIsTrusted(t *testing.T) {
	r := newRig(t)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if r.err.Len() != 0 {
		t.Errorf("stderr: %q", r.err.String())
	}
}

func TestUpDoesNotWarnWhenMainAlreadyRuns(t *testing.T) {
	r := newRig(t)
	r.l.warning = "some warning"
	r.m.alive["main"] = true
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if r.err.Len() != 0 || len(r.l.trustAsked) != 0 {
		t.Errorf("stderr %q, asked %v", r.err.String(), r.l.trustAsked)
	}
}
