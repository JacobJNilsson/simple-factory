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
	notes         []string
	shown         []string
	shownLead     []string
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
func (f *fakeMux) Stop(name string) error { f.stopped = name; return nil }
func (f *fakeMux) Notify(name, text string) error {
	f.notified = name + ":" + text
	f.notes = append(f.notes, f.notified)
	return nil
}
func (f *fakeMux) Show(name string) error {
	f.shown = append(f.shown, name)
	return f.showErr
}
func (f *fakeMux) ShowLead(name string) error {
	f.shownLead = append(f.shownLead, name)
	return f.showErr
}
func (f *fakeMux) Escape(string) error { return nil }

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
	// trusted lists the folders that Trust got, and trustErr makes Trust fail. onTrust runs at each Trust call.
	trusted  []string
	trustErr error
	onTrust  func()
}

func (p *fakeProvider) Preflight(string) error     { return p.preflight }
func (p *fakeProvider) TrustWarning(string) string { return "" }

func (p *fakeProvider) Trust(dir string) error {
	p.trusted = append(p.trusted, dir)
	if p.onTrust != nil {
		p.onTrust()
	}
	return p.trustErr
}

// Deliver takes the text when deliverable is set, and fails with deliverErr when that is set.
func (p *fakeProvider) Stuck(string) bool            { return false }
func (p *fakeProvider) Drain(string) (string, error) { return "", nil }

func (p *fakeProvider) Deliver(name, text, _ string) (bool, error) {
	if p.deliverErr != nil {
		return false, p.deliverErr
	}
	if p.deliverable {
		p.delivered = append(p.delivered, name+":"+text)
	}
	return p.deliverable, nil
}

func (p *fakeProvider) Resumes(spec engine.SessionSpec) bool {
	if spec.Kind == "lead" {
		return p.resumable && !spec.Fresh
	}
	return p.resumable && spec.Resume
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

func TestStartLeadStartsFreshWhenNothingResumes(t *testing.T) {
	resumeGrace = 0
	m, p := &fakeMux{}, &fakeProvider{}
	resumed, err := New(m, p, nil).StartLead(engine.SessionSpec{Name: "lead", Kind: "lead"})
	if err != nil || resumed {
		t.Fatalf("resumed %v, err %v", resumed, err)
	}
	if len(m.starts) != 1 || p.spec.Fresh {
		t.Errorf("starts %v, spec %+v", m.starts, p.spec)
	}
}

func TestStartLeadResumesWhenTheSessionStaysAlive(t *testing.T) {
	resumeGrace = 0
	m, p := &fakeMux{endAfterStart: map[string]bool{}}, &fakeProvider{resumable: true}
	resumed, err := New(m, p, nil).StartLead(engine.SessionSpec{Name: "lead", Kind: "lead"})
	if err != nil || !resumed {
		t.Fatalf("resumed %v, err %v", resumed, err)
	}
	if len(m.starts) != 1 || len(m.shownLead) != 1 {
		t.Errorf("starts %v, shown %v", m.starts, m.shownLead)
	}
}

func TestStartLeadStartsFreshWhenTheResumedSessionEndsAtOnce(t *testing.T) {
	resumeGrace = 0
	m, p := &fakeMux{endAfterStart: map[string]bool{"lead": true}}, &fakeProvider{resumable: true}
	resumed, err := New(m, p, nil).StartLead(engine.SessionSpec{Name: "lead", Kind: "lead"})
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

func resumeSpec() engine.SessionSpec {
	return engine.SessionSpec{Name: "worker-t1", Kind: "worker", Resume: true}
}

func TestResumeThatEndsWithinTheGraceFails(t *testing.T) {
	resumeGrace = 0
	m, p := &fakeMux{endAfterStart: map[string]bool{"worker-t1": true}}, &fakeProvider{resumable: true}

	err := New(m, p, nil).Start(resumeSpec())

	var re *ResumeError
	if !errors.As(err, &re) || re.Name != "worker-t1" {
		t.Fatalf("err %v, want a ResumeError", err)
	}
	if len(m.starts) != 1 {
		t.Errorf("starts %v, want only the resume", m.starts)
	}
}

func TestResumeWithoutARecordedConversationRunsNoTemplate(t *testing.T) {
	m, p := &fakeMux{}, &fakeProvider{}

	err := New(m, p, nil).Start(resumeSpec())

	var re *ResumeError
	if !errors.As(err, &re) {
		t.Fatalf("err %v, want a ResumeError", err)
	}
	if len(m.starts) != 0 || len(m.shown) != 0 || len(p.specs) != 0 {
		t.Errorf("starts %v, shown %v, commands %d", m.starts, m.shown, len(p.specs))
	}
}

func TestResumeThatKeepsRunningShowsTheSessionOnce(t *testing.T) {
	resumeGrace = 0
	m, p := &fakeMux{endAfterStart: map[string]bool{}}, &fakeProvider{resumable: true}

	if err := New(m, p, nil).Start(resumeSpec()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.starts, []string{"worker-t1"}) || !reflect.DeepEqual(m.shown, []string{"worker-t1"}) {
		t.Errorf("starts %v, shown %v", m.starts, m.shown)
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
	if _, err := Mux(cfg, ""); err != nil {
		t.Fatal(err)
	}
	// An invalid config set shows that the config wins over the built-in one.
	cfg.Multiplexers["tmux"] = map[string]string{"start": "s"}
	if _, err := Mux(cfg, ""); err == nil {
		t.Fatal("want an error from the incomplete config set")
	}
}

func TestMuxBuiltinAndUnknown(t *testing.T) {
	if _, err := Mux(config.Config{Multiplexer: "tmux"}, ""); err != nil {
		t.Fatal(err)
	}
	_, err := Mux(config.Config{Multiplexer: "screenx"}, "")
	var ue *unknownError
	if !errors.As(err, &ue) || ue.Name != "screenx" {
		t.Fatalf("error %v does not name the set", err)
	}
}

func TestWorkerAndReviewerFoldersAreTrustedBeforeTheSessionStarts(t *testing.T) {
	for _, kind := range []string{"worker", "reviewer"} {
		m, p := &fakeMux{}, &fakeProvider{}
		startsAtTrust := -1
		p.onTrust = func() { startsAtTrust = len(m.starts) }

		if err := New(m, p, nil).Start(engine.SessionSpec{Name: "a", Kind: kind, Dir: "/w/t1"}); err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(p.trusted, []string{"/w/t1"}) || startsAtTrust != 0 {
			t.Errorf("%s: trusted %v with %d sessions started", kind, p.trusted, startsAtTrust)
		}
	}
}

func TestTheLeadFolderIsNotTrusted(t *testing.T) {
	m, p := &fakeMux{}, &fakeProvider{}
	if err := New(m, p, nil).Start(engine.SessionSpec{Name: "lead", Kind: "lead", Dir: "/w"}); err != nil {
		t.Fatal(err)
	}
	if len(p.trusted) != 0 {
		t.Fatalf("trusted %v", p.trusted)
	}
}

func TestFailedTrustIsLoggedAndTheLeadHearsOnce(t *testing.T) {
	m, p := &fakeMux{}, &fakeProvider{trustErr: errors.New("locked")}
	var log bytes.Buffer
	l := New(m, p, &log)

	for _, name := range []string{"worker-t1", "worker-t2"} {
		if err := l.Start(engine.SessionSpec{Name: name, Kind: "worker", Dir: "/w/" + name}); err != nil {
			t.Fatalf("start failed: %v", err)
		}
	}

	if !reflect.DeepEqual(m.starts, []string{"worker-t1", "worker-t2"}) {
		t.Errorf("starts %v", m.starts)
	}
	for _, name := range []string{"worker-t1", "worker-t2"} {
		if want := "trust of /w/" + name + " for " + name + " failed: locked"; strings.Count(log.String(), want) != 1 {
			t.Errorf("log %q lacks %q once", log.String(), want)
		}
	}
	want := []string{"lead:sfy could not mark /w/worker-t1 trusted: locked. The agent may show the trust dialog."}
	if !reflect.DeepEqual(m.notes, want) {
		t.Errorf("notes %q, want %q", m.notes, want)
	}
}

func TestFailedShowIsLoggedAndStartSucceeds(t *testing.T) {
	m := &fakeMux{showErr: errors.New("no pane")}
	var log bytes.Buffer
	if err := New(m, &fakeProvider{}, &log).Start(engine.SessionSpec{Name: "lead", Kind: "lead", Dir: "/w"}); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if !strings.Contains(log.String(), "show of lead failed: no pane") {
		t.Errorf("log %q", log.String())
	}
}

func TestStartShowsTheLeadWithShowLead(t *testing.T) {
	m := &fakeMux{}
	l := New(m, &fakeProvider{}, nil)
	if err := l.Start(engine.SessionSpec{Name: "lead", Kind: "lead"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.shownLead, []string{"lead"}) || len(m.shown) != 0 {
		t.Errorf("shownLead %v, shown %v", m.shownLead, m.shown)
	}
}

func TestLeadCommandReturnsTheArgvAndStartsNoSession(t *testing.T) {
	m, p := &fakeMux{}, &fakeProvider{resumable: true}
	argv, resumed, err := New(m, p, nil).LeadCommand(engine.SessionSpec{Name: "lead", Kind: "lead"})
	if err != nil || !resumed {
		t.Fatalf("resumed %v, err %v", resumed, err)
	}
	if !reflect.DeepEqual(argv, []string{"agent", "--flag"}) {
		t.Errorf("argv %v", argv)
	}
	if len(m.starts) != 0 || len(m.shownLead) != 0 {
		t.Errorf("starts %v, shown %v, want none", m.starts, m.shownLead)
	}
}

func TestLeadCommandOfAFreshSpecDoesNotResume(t *testing.T) {
	m, p := &fakeMux{}, &fakeProvider{resumable: true}
	_, resumed, err := New(m, p, nil).LeadCommand(engine.SessionSpec{Name: "lead", Kind: "lead", Fresh: true})
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
	if !reflect.DeepEqual(m.shown, []string{"worker-t1", "reviewer-t1"}) || len(m.shownLead) != 0 {
		t.Errorf("shown %v, shownLead %v", m.shown, m.shownLead)
	}
}
