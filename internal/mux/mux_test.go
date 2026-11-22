package mux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestFromMapNamesMissingTemplate(t *testing.T) {
	_, err := FromMap(map[string]string{"start": "a", "send": "b", "stop": "c"})
	var te *templateError
	if !errors.As(err, &te) || te.template != "alive" {
		t.Fatalf("want an error naming alive, got %v", err)
	}
}

func TestFromMapRejectsUnknownKey(t *testing.T) {
	_, err := FromMap(map[string]string{"start": "a", "send": "b", "alive": "c", "stop": "d", "bogus": "e"})
	var te *templateError
	if !errors.As(err, &te) || te.template != "bogus" {
		t.Fatalf("want an error naming bogus, got %v", err)
	}
}

func TestFromMapAcceptsOptionalNotify(t *testing.T) {
	got, err := FromMap(map[string]string{"start": "a", "send": "b", "alive": "c", "stop": "d", "notify": "e"})
	if err != nil {
		t.Fatal(err)
	}
	want := Templates{Start: "a", Send: "b", Alive: "c", Stop: "d", Notify: "e"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFromMapAcceptsOptionalShow(t *testing.T) {
	got, err := FromMap(map[string]string{"start": "a", "send": "b", "alive": "c", "stop": "d", "show": "e"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Show != "e" {
		t.Fatalf("got %+v", got)
	}
}

func TestFromMapAcceptsOptionalShowLead(t *testing.T) {
	got, err := FromMap(map[string]string{"start": "a", "send": "b", "alive": "c", "stop": "d", "show_lead": "e"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ShowLead != "e" || got.Show != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestBuiltinRejectsUnknownName(t *testing.T) {
	if _, ok := Builtin("nope"); ok {
		t.Error("unknown builtin must not exist")
	}
}

func TestQuotingSurvivesTheShell(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	text := "it's $HOME `id` with  spaces\nand $(echo hi) \"q\" \\ end"
	m := Runner(Templates{Start: "true", Send: "printf %s {text} > " + shellQuote(out), Alive: "true", Stop: "true"})
	if err := m.Send("x", text); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != text {
		t.Fatalf("got %q, want %q", got, text)
	}
}

func TestCommandIsQuotedPerElement(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	m := Runner(Templates{Start: "printf '%s|' {command} > " + shellQuote(out), Send: "true", Alive: "true", Stop: "true"})
	if err := m.Start("n", "/", []string{"a b", "it's", "$x"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a b|it's|$x|" {
		t.Fatalf("got %q", got)
	}
}

func TestAliveExitCodes(t *testing.T) {
	cases := []struct {
		cmd     string
		want    bool
		wantErr bool
	}{{"exit 0", true, false}, {"exit 1", false, false}, {"exit 3", false, true}}
	for _, c := range cases {
		t.Run(c.cmd, func(t *testing.T) {
			m := Runner(Templates{Start: "true", Send: "true", Alive: c.cmd, Stop: "true"})
			got, err := m.Alive("n")
			if got != c.want || (err != nil) != c.wantErr {
				t.Errorf("got %v, %v", got, err)
			}
		})
	}
}

func TestTimeoutIsAnErrorAndAliveDoesNotAnswerFalse(t *testing.T) {
	m := runner{t: Templates{Start: "true", Send: "true", Alive: "sleep 5", Stop: "true"}, timeout: 100 * time.Millisecond}
	alive, err := m.Alive("n")
	if err == nil || alive {
		t.Fatalf("got %v, %v, want an error", alive, err)
	}
}

func TestValueWithPlaceholderIsNotFilledTwice(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	m := Runner(Templates{Start: "true", Send: "printf %s {text} > " + shellQuote(out), Alive: "true", Stop: "true"})
	if err := m.Send("{text}", "{name}"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{name}" {
		t.Fatalf("got %q", got)
	}
}

func TestNotifyFillsTemplate(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	m := Runner(Templates{Start: "true", Send: "true", Alive: "true", Stop: "true", Notify: "printf '%s:%s' {name} {text} > " + shellQuote(out)})
	if err := m.Notify("n 1", "it's"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "n 1:it's" {
		t.Fatalf("got %q", got)
	}
}

// herdrSet loads the example config in docs/herdr.md and returns its template set.
func herdrSet(t *testing.T) Templates {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "herdr.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(doc), "```toml\n")
	if !ok {
		t.Fatal("docs/herdr.md has no toml block")
	}
	block, _, ok := strings.Cut(rest, "```")
	if !ok {
		t.Fatal("docs/herdr.md toml block does not end")
	}
	var cfg struct {
		Multiplexer  string                       `toml:"multiplexer"`
		Multiplexers map[string]map[string]string `toml:"multiplexers"`
	}
	if _, err := toml.Decode(block, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Multiplexer != "herdr" {
		t.Fatalf("multiplexer = %q, want herdr", cfg.Multiplexer)
	}
	set, err := FromMap(cfg.Multiplexers["herdr"])
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestHerdrExampleHidesWorkersAndShowsTheLead(t *testing.T) {
	set := herdrSet(t)
	if set.Show != "" || set.ShowLead == "" {
		t.Fatalf("show %q, show_lead %q", set.Show, set.ShowLead)
	}
}

func TestHerdrAliveFailsWhenHcIsMissing(t *testing.T) {
	set := herdrSet(t)
	t.Setenv("PATH", t.TempDir())
	alive, err := Runner(set).Alive("sfy-test-x")
	if err == nil || alive {
		t.Fatalf("got %v, %v, want an error", alive, err)
	}
}

func TestErrorIncludesStderr(t *testing.T) {
	m := Runner(Templates{Start: "true", Send: "true", Alive: "true", Stop: "echo boom >&2; exit 2"})
	err := m.Stop("n")
	var ce *commandError
	if !errors.As(err, &ce) || ce.stderr != "boom" {
		t.Fatalf("want stderr in the error, got %v", err)
	}
}

func TestNotifyWithoutTemplateDoesNothing(t *testing.T) {
	m := Runner(Templates{Start: "true", Send: "true", Alive: "true", Stop: "true"})
	if err := m.Notify("n", "hi"); err != nil {
		t.Fatal(err)
	}
}

func TestShowFillsTemplateAfterStart(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	m := Runner(Templates{Start: "echo start >> " + shellQuote(out), Send: "true", Alive: "true", Stop: "true",
		Show: "printf 'show %s\\n' {name} >> " + shellQuote(out)})
	if err := m.Start("n 1", t.TempDir(), []string{"true"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Show("n 1"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "start\nshow n 1\n" {
		t.Fatalf("got %q", got)
	}
}

func TestShowFailureIsAnError(t *testing.T) {
	m := Runner(Templates{Start: "true", Send: "true", Alive: "true", Stop: "true", Show: "echo boom >&2; exit 1"})
	if err := m.Show("n"); err == nil {
		t.Fatal("want an error")
	}
}

func showSet(show, showLead string) Templates {
	return Templates{Start: "true", Send: "true", Alive: "true", Stop: "true", Show: show, ShowLead: showLead}
}

func TestShowLeadRunsTheShowLeadTemplate(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	m := Runner(showSet("echo show >> "+shellQuote(out), "echo show_lead >> "+shellQuote(out)))
	if err := m.ShowLead("lead"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != "show_lead\n" {
		t.Fatalf("got %q", got)
	}
}

func TestShowLeadFallsBackToShow(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	m := Runner(showSet("echo show >> "+shellQuote(out), ""))
	if err := m.ShowLead("lead"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != "show\n" {
		t.Fatalf("got %q", got)
	}
}

func TestShowWithEmptyShowDoesNothingForAWorker(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	m := Runner(showSet("", "echo show_lead >> "+shellQuote(out)))
	if err := m.Show("worker-t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("show_lead ran for a worker")
	}
}

func TestShowWithoutTemplateDoesNothing(t *testing.T) {
	m := Runner(Templates{Start: "true", Send: "true", Alive: "true", Stop: "true"})
	if err := m.Show("n"); err != nil {
		t.Fatal(err)
	}
}

func TestHerdrShowPassesTheAddressToHerdrApp(t *testing.T) {
	set := herdrSet(t)
	bin := t.TempDir()
	out := filepath.Join(t.TempDir(), "out")
	stub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("hc", `echo '{"address":"t6z","agent":"sleep","name":"main","state":"live","turn":0}'`)
	stub("herdr-app", `echo "$@" > `+shellQuote(out))
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := Runner(set).ShowLead("lead"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if strings.TrimSpace(string(got)) != "do terminal.keep --terminal t6z" {
		t.Fatalf("got %q", got)
	}
}

// herdrStubs puts an hc that knows the terminal and a herdr-app with the given body on PATH.
func herdrStubs(t *testing.T, herdrApp string) {
	t.Helper()
	bin := t.TempDir()
	stub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("hc", `echo '{"address":"t6z","name":"main","state":"live"}'`)
	stub("herdr-app", herdrApp)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("SFY_SHOW_WAIT", "0.01")
}

func TestHerdrShowRetriesUntilTheTerminalIsKnown(t *testing.T) {
	set := herdrSet(t)
	count := filepath.Join(t.TempDir(), "count")
	herdrStubs(t, `echo x >> `+shellQuote(count)+`
if [ $(wc -l < `+shellQuote(count)+`) -le 2 ]; then echo '{"error":"no running terminal"}'; exit 1; fi
echo '{"pending": true}'`)
	if err := Runner(set).ShowLead("lead"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(count)
	if n := strings.Count(string(got), "x"); n != 3 {
		t.Fatalf("herdr-app ran %d times, want 3", n)
	}
}

func TestHerdrShowFailureCarriesHerdrAppOutput(t *testing.T) {
	set := herdrSet(t)
	count := filepath.Join(t.TempDir(), "count")
	herdrStubs(t, `echo x >> `+shellQuote(count)+`
echo '{"error":"no running terminal \"main\""}'; exit 1`)
	err := Runner(set).ShowLead("lead")
	var ce *commandError
	if !errors.As(err, &ce) || !strings.Contains(ce.stderr, "no running terminal") {
		t.Fatalf("want herdr-app's message in the error, got %v", err)
	}
	got, _ := os.ReadFile(count)
	if n := strings.Count(string(got), "x"); n != 10 {
		t.Fatalf("herdr-app ran %d times, want 10", n)
	}
}

func TestHerdrShowFailsWithoutAnAddress(t *testing.T) {
	set := herdrSet(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "hc"), []byte("#!/bin/sh\necho '{\"name\":\"main\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := Runner(set).ShowLead("lead"); err == nil {
		t.Fatal("want an error")
	}
}

func uniqueName() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "sfy-test-" + hex.EncodeToString(b[:])
}

// exerciseSet drives one session through the whole lifecycle and reads the screen with read.
func exerciseSet(t *testing.T, m Mux, name string, read func() string) {
	// A command that starts with a dash and a text that starts with a dash must reach the multiplexer intact.
	t.Helper()
	t.Cleanup(func() { _ = m.Stop(name) })
	if err := m.Start(name, t.TempDir(), []string{"cat", "-n"}); err != nil {
		t.Fatal(err)
	}
	alive, err := m.Alive(name)
	if err != nil || !alive {
		t.Fatalf("Alive after Start = %v, %v", alive, err)
	}
	for _, text := range []string{"-n", "it's $HOME with spaces"} {
		if err := m.Send(name, text); err != nil {
			t.Fatal(err)
		}
		waitFor(t, read, text)
	}
	if err := m.Stop(name); err != nil {
		t.Fatal(err)
	}
	waitGone(t, m, name)
}

func waitFor(t *testing.T, read func() string, text string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(read(), text) {
		if time.Now().After(deadline) {
			t.Fatalf("text not on screen: %q", read())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitGone(t *testing.T, m Mux, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		alive, err := m.Alive(name)
		if err == nil && !alive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Alive after Stop = %v, %v", alive, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestTmuxSet(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH")
	}
	// macOS limits a socket path to 104 bytes, so use TMPDIR only when it leaves room for the tmux socket name.
	base := os.TempDir()
	if len(base) > 40 {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "fmx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	// tmux reads TMUX before TMUX_TMPDIR, so clear it to stay off the operator's server.
	t.Setenv("TMUX", "")
	skipIfTmuxCannotStartPane(t)
	set, _ := Builtin("tmux")
	name := uniqueName()
	exerciseSet(t, Runner(set), name, func() string {
		out, _ := exec.CommandContext(context.Background(), "tmux", "capture-pane", "-p", "-t", "="+name+":").Output()
		return string(out)
	})
}

func TestHerdrSet(t *testing.T) {
	if _, err := exec.LookPath("hc"); err != nil {
		t.Skip("hc is not on PATH")
	}
	if err := exec.CommandContext(context.Background(), "hc", "server", "status").Run(); err != nil {
		t.Skip("hc server is not running")
	}
	set := herdrSet(t)
	name := uniqueName()
	exerciseSet(t, Runner(set), name, func() string {
		out, _ := exec.CommandContext(context.Background(), "hc", "read", name).Output()
		return string(out)
	})
}

// skipIfTmuxCannotStartPane starts a throwaway tmux session and skips the test when tmux cannot fork a pane.
// Any other start failure fails the test.
func skipIfTmuxCannotStartPane(t *testing.T) {
	t.Helper()
	sock := "probe-" + uniqueName()
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

func TestHomeFillsPlaceholderAndEnvForEveryTemplate(t *testing.T) {
	dir := t.TempDir()
	home := "/state/it's here"
	probe := func(file string) string {
		return "printf '%s|%s' {home} \"$SFY_HOME\" > " + shellQuote(filepath.Join(dir, file))
	}
	m := RunnerFor(Templates{Start: probe("start"), Send: probe("send"), Alive: probe("alive"), Stop: probe("stop"),
		Notify: probe("notify"), Show: probe("show"), ShowLead: probe("show_lead")}, home)
	calls := map[string]func() error{
		"start": func() error { return m.Start("n", dir, []string{"true"}) }, "send": func() error { return m.Send("n", "x") },
		"alive": func() error { _, err := m.Alive("n"); return err }, "stop": func() error { return m.Stop("n") },
		"notify": func() error { return m.Notify("n", "x") }, "show": func() error { return m.Show("n") },
		"show_lead": func() error { return m.ShowLead("n") },
	}
	for file, call := range calls {
		if err := call(); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		got, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		if want := home + "|" + home; string(got) != want {
			t.Errorf("%s: got %q, want %q", file, got, want)
		}
	}
}

func TestRunnerWithoutHomeKeepsTheCallerEnv(t *testing.T) {
	t.Setenv("SFY_HOME", "/from/caller")
	out := filepath.Join(t.TempDir(), "out")
	m := Runner(Templates{Start: "true", Send: "true", Alive: "true", Stop: "true", Show: "printf %s \"$SFY_HOME\" > " + shellQuote(out)})
	if err := m.Show("n"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(out); string(got) != "/from/caller" {
		t.Fatalf("got %q", got)
	}
}
