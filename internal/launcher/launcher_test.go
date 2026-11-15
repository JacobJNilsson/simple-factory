package launcher

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
)

type fakeMux struct {
	name, dir string
	command   []string
	sent      string
	stopped   string
	notified  string
	shown     []string
	showErr   error
}

func (f *fakeMux) Start(name, dir string, command []string) error {
	f.name, f.dir, f.command = name, dir, command
	return nil
}
func (f *fakeMux) Send(name, text string) error { f.sent = name + ":" + text; return nil }
func (f *fakeMux) Alive(name string) (bool, error) {
	return name == "up", nil
}
func (f *fakeMux) Stop(name string) error         { f.stopped = name; return nil }
func (f *fakeMux) Notify(name, text string) error { f.notified = name + ":" + text; return nil }
func (f *fakeMux) Show(name string) error {
	f.shown = append(f.shown, name)
	return f.showErr
}

type fakeProvider struct{ spec engine.SessionSpec }

func (p *fakeProvider) TrustWarning(string) string { return "" }

func (p *fakeProvider) Command(spec engine.SessionSpec) ([]string, error) {
	p.spec = spec
	return []string{"agent", "--flag"}, nil
}

func TestStartPassesArgvNameAndDir(t *testing.T) {
	m, p := &fakeMux{}, &fakeProvider{}
	l := New(m, p, nil)
	spec := engine.SessionSpec{Name: "worker-t1", Dir: "/work/t1", Kind: "worker"}
	if err := l.Start(spec); err != nil {
		t.Fatal(err)
	}
	if p.spec.Name != "worker-t1" {
		t.Errorf("provider got spec %+v", p.spec)
	}
	if m.name != "worker-t1" || m.dir != "/work/t1" || !reflect.DeepEqual(m.command, []string{"agent", "--flag"}) {
		t.Errorf("mux got %q %q %v", m.name, m.dir, m.command)
	}
}

func TestOtherCallsPassThrough(t *testing.T) {
	m := &fakeMux{}
	l := New(m, &fakeProvider{}, nil)
	_ = l.Send("a", "hi")
	_ = l.Stop("a")
	_ = l.Notify("a", "n")
	alive, _ := l.Alive("up")
	if m.sent != "a:hi" || m.stopped != "a" || m.notified != "a:n" || !alive {
		t.Errorf("pass through failed: %+v", m)
	}
}

func TestMuxPrefersConfigSet(t *testing.T) {
	cfg := config.Config{Multiplexer: "tmux", Multiplexers: map[string]map[string]string{
		"tmux": {"start": "s", "send": "s", "alive": "a", "stop": "x"},
	}}
	if _, err := Mux(cfg); err != nil {
		t.Fatal(err)
	}
	// An invalid config set shows that the config wins over the built-in one.
	cfg.Multiplexers["tmux"] = map[string]string{"start": "s"}
	if _, err := Mux(cfg); err == nil {
		t.Fatal("want an error from the incomplete config set")
	}
}

func TestMuxBuiltinAndUnknown(t *testing.T) {
	if _, err := Mux(config.Config{Multiplexer: "tmux"}); err != nil {
		t.Fatal(err)
	}
	_, err := Mux(config.Config{Multiplexer: "screenx"})
	var ue *unknownError
	if !errors.As(err, &ue) || ue.Name != "screenx" {
		t.Fatalf("error %v does not name the set", err)
	}
}

func TestStartShowsEveryAgentKind(t *testing.T) {
	for _, kind := range []string{"main", "worker", "reviewer"} {
		m := &fakeMux{}
		if err := New(m, &fakeProvider{}, nil).Start(engine.SessionSpec{Name: kind + "-t1", Kind: kind, Dir: "/w"}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(m.shown, []string{kind + "-t1"}) {
			t.Errorf("%s shown %v", kind, m.shown)
		}
	}
}

func TestFailedShowIsLoggedAndStartSucceeds(t *testing.T) {
	m := &fakeMux{showErr: errors.New("no pane")}
	var log bytes.Buffer
	if err := New(m, &fakeProvider{}, &log).Start(engine.SessionSpec{Name: "main", Kind: "main", Dir: "/w"}); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if !strings.Contains(log.String(), "show of main failed: no pane") {
		t.Errorf("log %q", log.String())
	}
}
