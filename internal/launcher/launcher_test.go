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
	// starts lists each started session name. endAfterStart names sessions that end at once, and Stop clears them.
	starts        []string
	endAfterStart map[string]bool
	name, dir     string
	command       []string
	sent          string
	stopped       string
	notified      string
	shown         []string
	shownMain     []string
	showErr       error
}

func (f *fakeMux) Start(name, dir string, command []string) error {
	f.name, f.dir, f.command = name, dir, command
	f.starts = append(f.starts, name)
	return nil
}
func (f *fakeMux) Send(name, text string) error { f.sent = name + ":" + text; return nil }
func (f *fakeMux) Alive(name string) (bool, error) {
	if f.endAfterStart != nil {
		// The first start of the name ends at once. A second start runs.
		started := 0
		for _, s := range f.starts {
			if s == name {
				started++
			}
		}
		return started > 0 && (!f.endAfterStart[name] || started > 1), nil
	}
	return name == "up", nil
}
func (f *fakeMux) Stop(name string) error         { f.stopped = name; return nil }
func (f *fakeMux) Notify(name, text string) error { f.notified = name + ":" + text; return nil }
func (f *fakeMux) Show(name string) error {
	f.shown = append(f.shown, name)
	return f.showErr
}
func (f *fakeMux) ShowMain(name string) error {
	f.shownMain = append(f.shownMain, name)
	return f.showErr
}

type fakeProvider struct {
	spec  engine.SessionSpec
	specs []engine.SessionSpec
	// resumable is what Resumes reports for a spec that is not fresh.
	resumable bool
	// deliverable makes Deliver take a text, deliverErr makes it fail, and delivered lists the texts it took.
	deliverable bool
	deliverErr  error
	delivered   []string
	preflight   error
}

func (p *fakeProvider) Preflight(string) error     { return p.preflight }
func (p *fakeProvider) TrustWarning(string) string { return "" }

// Deliver takes the text when deliverable is set, and fails with deliverErr when that is set.
func (p *fakeProvider) Deliver(name, text string) (bool, error) {
	if p.deliverErr != nil {
		return false, p.deliverErr
	}
	if p.deliverable {
		p.delivered = append(p.delivered, name+":"+text)
	}
	return p.deliverable, nil
}

func (p *fakeProvider) Resumes(spec engine.SessionSpec) bool {
	return p.resumable && spec.Kind == "main" && !spec.Fresh
}

func (p *fakeProvider) Command(spec engine.SessionSpec) ([]string, error) {
	p.spec = spec
	p.specs = append(p.specs, spec)
	return []string{"agent", "--flag"}, nil
}

func TestAFailedPreflightStartsNoSession(t *testing.T) {
	want := &engine.PreflightError{Msg: "no sandbox"}
	m, p := &fakeMux{}, &fakeProvider{preflight: want}

	err := New(m, p, nil).Start(engine.SessionSpec{Name: "worker-t1", Kind: "worker"})

	if !errors.Is(err, want) || len(m.starts) != 0 || len(p.specs) != 0 {
		t.Fatalf("err %v, starts %v, commands %d", err, m.starts, len(p.specs))
	}
}

func TestNoteDeliversThroughTheProviderAndTypesNothing(t *testing.T) {
	m, p := &fakeMux{}, &fakeProvider{deliverable: true}

	err := New(m, p, nil).Note("worker-t1", "sfy restarted.")

	if err != nil || m.sent != "" || len(p.delivered) != 1 || p.delivered[0] != "worker-t1:sfy restarted." {
		t.Fatalf("err %v, typed %q, delivered %q", err, m.sent, p.delivered)
	}
}

func TestNoteTypesTheTextWhenTheProviderCannotDeliver(t *testing.T) {
	m, p := &fakeMux{}, &fakeProvider{}

	err := New(m, p, nil).Note("worker-t1", "sfy restarted.")

	if err != nil || m.sent != "worker-t1:sfy restarted." {
		t.Fatalf("err %v, typed %q", err, m.sent)
	}
}

func TestNoteTypesTheTextWhenDeliveryFails(t *testing.T) {
	m, p := &fakeMux{}, &fakeProvider{deliverErr: errors.New("disk full")}

	err := New(m, p, nil).Note("worker-t1", "sfy restarted.")

	if err != nil || m.sent != "worker-t1:sfy restarted." {
		t.Fatalf("err %v, typed %q", err, m.sent)
	}
}

func TestDeliverAsksTheProvider(t *testing.T) {
	p := &fakeProvider{deliverable: true}

	ok, err := New(&fakeMux{}, p, nil).Deliver("main", "hi")

	if err != nil || !ok || len(p.delivered) != 1 {
		t.Fatalf("ok %v, err %v, delivered %q", ok, err, p.delivered)
	}
}

func TestStartMainStartsFreshWhenNothingResumes(t *testing.T) {
	resumeGrace = 0
	m, p := &fakeMux{}, &fakeProvider{}
	resumed, err := New(m, p, nil).StartMain(engine.SessionSpec{Name: "main", Kind: "main"})
	if err != nil || resumed {
		t.Fatalf("resumed %v, err %v", resumed, err)
	}
	if len(m.starts) != 1 || p.spec.Fresh {
		t.Errorf("starts %v, spec %+v", m.starts, p.spec)
	}
}

func TestStartMainResumesWhenTheSessionStaysAlive(t *testing.T) {
	resumeGrace = 0
	m, p := &fakeMux{endAfterStart: map[string]bool{}}, &fakeProvider{resumable: true}
	resumed, err := New(m, p, nil).StartMain(engine.SessionSpec{Name: "main", Kind: "main"})
	if err != nil || !resumed {
		t.Fatalf("resumed %v, err %v", resumed, err)
	}
	if len(m.starts) != 1 || len(m.shownMain) != 1 {
		t.Errorf("starts %v, shown %v", m.starts, m.shownMain)
	}
}

func TestStartMainStartsFreshWhenTheResumedSessionEndsAtOnce(t *testing.T) {
	resumeGrace = 0
	m, p := &fakeMux{endAfterStart: map[string]bool{"main": true}}, &fakeProvider{resumable: true}
	resumed, err := New(m, p, nil).StartMain(engine.SessionSpec{Name: "main", Kind: "main"})
	if err != nil || resumed {
		t.Fatalf("resumed %v, err %v", resumed, err)
	}
	if len(m.starts) != 2 {
		t.Fatalf("starts %v, want a resume and a fresh start", m.starts)
	}
	if len(p.specs) != 2 || p.specs[0].Fresh || !p.specs[1].Fresh {
		t.Errorf("specs %+v", p.specs)
	}
}

func TestStartNeverResumesAWorker(t *testing.T) {
	resumeGrace = 0
	m, p := &fakeMux{}, &fakeProvider{resumable: true}
	if err := New(m, p, nil).Start(engine.SessionSpec{Name: "worker-t1", Kind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if len(m.starts) != 1 {
		t.Errorf("starts %v", m.starts)
	}
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

func TestStartShowsTheMainAgentWithShowMain(t *testing.T) {
	m := &fakeMux{}
	l := New(m, &fakeProvider{}, nil)
	if err := l.Start(engine.SessionSpec{Name: "main", Kind: "main"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.shownMain, []string{"main"}) || len(m.shown) != 0 {
		t.Errorf("shownMain %v, shown %v", m.shownMain, m.shown)
	}
}

func TestMainCommandReturnsTheArgvAndStartsNoSession(t *testing.T) {
	m, p := &fakeMux{}, &fakeProvider{resumable: true}
	argv, resumed, err := New(m, p, nil).MainCommand(engine.SessionSpec{Name: "main", Kind: "main"})
	if err != nil || !resumed {
		t.Fatalf("resumed %v, err %v", resumed, err)
	}
	if !reflect.DeepEqual(argv, []string{"agent", "--flag"}) {
		t.Errorf("argv %v", argv)
	}
	if len(m.starts) != 0 || len(m.shownMain) != 0 {
		t.Errorf("starts %v, shown %v, want none", m.starts, m.shownMain)
	}
}

func TestMainCommandOfAFreshSpecDoesNotResume(t *testing.T) {
	m, p := &fakeMux{}, &fakeProvider{resumable: true}
	_, resumed, err := New(m, p, nil).MainCommand(engine.SessionSpec{Name: "main", Kind: "main", Fresh: true})
	if err != nil || resumed {
		t.Fatalf("resumed %v, err %v", resumed, err)
	}
}

func TestStartShowsWorkersAndReviewersWithShow(t *testing.T) {
	m := &fakeMux{}
	l := New(m, &fakeProvider{}, nil)
	for _, kind := range []string{"worker", "reviewer"} {
		if err := l.Start(engine.SessionSpec{Name: kind + "-t1", Kind: kind}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(m.shown, []string{"worker-t1", "reviewer-t1"}) || len(m.shownMain) != 0 {
		t.Errorf("shown %v, shownMain %v", m.shown, m.shownMain)
	}
}
