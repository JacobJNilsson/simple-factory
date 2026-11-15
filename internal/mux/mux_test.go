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

func TestHerdrAliveFailsWhenHcIsMissing(t *testing.T) {
	set, _ := Builtin("herdr")
	t.Setenv("PATH", t.TempDir())
	alive, err := Runner(set).Alive("factory-test-x")
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

func uniqueName() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "factory-test-" + hex.EncodeToString(b[:])
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
	// A short path under /tmp keeps the socket path below the OS limit.
	dir, err := os.MkdirTemp("/tmp", "fmx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	// tmux reads TMUX before TMUX_TMPDIR, so clear it to stay off the operator's server.
	t.Setenv("TMUX", "")
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
	set, _ := Builtin("herdr")
	name := uniqueName()
	exerciseSet(t, Runner(set), name, func() string {
		out, _ := exec.CommandContext(context.Background(), "hc", "read", name).Output()
		return string(out)
	})
}
