package up

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/agentproc"
	"github.com/JacobJNilsson/simple-factory/internal/mux"
	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

// execRecorder replaces the real exec. It records each folder and argv and returns err.
type execRecorder struct {
	dirs  []string
	argvs [][]string
	err   error
}

func (e *execRecorder) exec(dir string, argv []string) error {
	e.dirs = append(e.dirs, dir)
	e.argvs = append(e.argvs, argv)
	return e.err
}

func inPlaceRig(t *testing.T, address string, whoamiErr error) (*rig, *execRecorder) {
	t.Helper()
	r := newRig(t)
	rec := &execRecorder{}
	r.o.whoami = func() (string, error) { return address, whoamiErr }
	r.o.exec = rec.exec
	return r, rec
}

func readAddress(t *testing.T, o options) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(leadAddressPath(o.home))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b)), true
}

func TestUpRunsTheLeadInPlaceWhenWhoamiPrintsAnAddress(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"/bin/sfy", "run", "env-file", "--", "claude"}}; !reflect.DeepEqual(rec.argvs, want) {
		t.Fatalf("exec got %v, want %v", rec.argvs, want)
	}
	if got, ok := readAddress(t, r.o); !ok || got != "dev" {
		t.Errorf("recorded address %q, %v, want dev", got, ok)
	}
	if len(r.l.specs) != 0 {
		t.Errorf("the set started a lead as well: %v", r.l.specs)
	}
	if len(r.l.commands) != 1 || r.l.commands[0].Name != "lead" || r.l.commands[0].Dir != r.o.home {
		t.Errorf("command specs %+v", r.l.commands)
	}
	if len(r.m.starts) != 1 || r.m.starts[0][0] != "sfy" {
		t.Errorf("only the sfy process starts in the background, got %v", r.m.starts)
	}
}

func TestUpRunsTheLeadInTheStateFolder(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if want := []string{r.o.home}; !reflect.DeepEqual(rec.dirs, want) {
		t.Fatalf("exec folders %v, want %v", rec.dirs, want)
	}
}

func TestUpFallsBackToStartWhenTheFolderChangeFails(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	rec.err = errors.New("chdir failed")
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(r.l.specs) != 1 {
		t.Fatalf("starts %v, want one start", r.l.specs)
	}
	if _, ok := readAddress(t, r.o); ok {
		t.Error("the address stayed after the failed change")
	}
}

func TestUpStartsTheSfyProcessBeforeItReplacesItself(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	r.o.exec = func(dir string, argv []string) error {
		if len(r.m.starts) != 1 {
			t.Errorf("the sfy process must run before the exec, starts %v", r.m.starts)
		}
		return rec.exec(dir, argv)
	}
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
}

func TestUpShowsTheLeadBeforeItReplacesItself(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	var addressAtShow string
	execsAtShow := -1
	r.m.onShowLead = func() {
		addressAtShow, _ = readAddress(t, r.o)
		execsAtShow = len(rec.argvs)
	}
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.m.shownLead, []string{"lead"}) {
		t.Fatalf("shown %v, want the lead once", r.m.shownLead)
	}
	if addressAtShow != "dev" || execsAtShow != 0 {
		t.Errorf("at show: address %q, execs %d, want dev and 0", addressAtShow, execsAtShow)
	}
	if len(rec.argvs) != 1 {
		t.Errorf("execs %v, want one", rec.argvs)
	}
}

func TestUpStartsInPlaceWhenShowLeadFails(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	r.m.showLeadErr = errors.New("no pane layout")
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(rec.argvs) != 1 {
		t.Fatalf("execs %v, want one", rec.argvs)
	}
	if !strings.Contains(r.err.String(), "no pane layout") {
		t.Errorf("warning %q does not name the failure", r.err.String())
	}
	if got, ok := readAddress(t, r.o); !ok || got != "dev" {
		t.Errorf("recorded address %q, %v, want dev", got, ok)
	}
}

func TestUpDoesNotShowLeadWhenTheStartGoesThroughTheSet(t *testing.T) {
	r := newRig(t)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(r.m.shownLead) != 0 {
		t.Errorf("up showed the lead itself: %v", r.m.shownLead)
	}
}

func TestUpFallsBackToStartWithoutWhoami(t *testing.T) {
	r := newRig(t)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(r.l.specs) != 1 || len(r.l.commands) != 0 {
		t.Fatalf("starts %v, commands %v, want one start", r.l.specs, r.l.commands)
	}
	if _, ok := readAddress(t, r.o); ok {
		t.Error("an address was recorded")
	}
}

func TestUpFallsBackToStartWhenTheSetPrintsNoAddress(t *testing.T) {
	r, rec := inPlaceRig(t, "", nil)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(rec.argvs) != 0 || len(r.l.specs) != 1 {
		t.Fatalf("execs %v, starts %v, want one start", rec.argvs, r.l.specs)
	}
	if _, ok := readAddress(t, r.o); ok {
		t.Error("an address was recorded")
	}
}

func TestUpFallsBackToStartWhenWhoamiFails(t *testing.T) {
	r, rec := inPlaceRig(t, "", errors.New("no tool"))
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(rec.argvs) != 0 || len(r.l.specs) != 1 {
		t.Fatalf("execs %v, starts %v, want one start", rec.argvs, r.l.specs)
	}
	if !strings.Contains(r.err.String(), "no tool") {
		t.Errorf("warning %q does not name the failure", r.err.String())
	}
}

func TestUpFallsBackToStartWhenTheExecFails(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	rec.err = errors.New("exec failed")
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(r.l.specs) != 1 {
		t.Fatalf("starts %v, want one start", r.l.specs)
	}
	if _, ok := readAddress(t, r.o); ok {
		t.Error("the address stayed after the failed exec")
	}
}

func TestUpShowsALeadThatAlreadyRunsAndDoesNotAskWhoami(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	r.m.alive["lead"] = true
	asked := false
	r.o.whoami = func() (string, error) { asked = true; return "dev", nil }
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if asked || len(rec.argvs) != 0 {
		t.Errorf("asked %v, execs %v, want neither", asked, rec.argvs)
	}
	if !reflect.DeepEqual(r.m.shownLead, []string{"lead"}) {
		t.Errorf("shown %v, want the lead", r.m.shownLead)
	}
}

func TestRestartNeverReplacesItself(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	r.m.answering = true
	r.m.alive["sfy"] = true
	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}
	if len(rec.argvs) != 0 || len(r.l.specs) != 1 {
		t.Errorf("execs %v, starts %v, want a start through the set", rec.argvs, r.l.specs)
	}
}

// registeredLauncher routes the launcher calls of a rig through mux.Registered, as the real wiring does.
type registeredLauncher struct {
	*fakeLauncher
	m mux.Mux
}

func (l registeredLauncher) Send(name, text string) error { return l.m.Send(name, text) }
func (l registeredLauncher) Alive(name string) (bool, error) {
	return l.m.Alive(name)
}
func (l registeredLauncher) Stop(name string) error     { return l.m.Stop(name) }
func (l registeredLauncher) ShowLead(name string) error { return l.m.ShowLead(name) }
func (l registeredLauncher) Escape(name string) error   { return l.m.Escape(name) }

func TestSendToLeadUsesTheRecordedAddress(t *testing.T) {
	r, _ := inPlaceRig(t, "dev", nil)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	m := mux.Registered(r.m, "lead", leadAddressPath(r.o.home), nil)
	if err := m.Send("lead", "hi"); err != nil {
		t.Fatal(err)
	}
	if want := [][2]string{{"dev", "hi"}}; !reflect.DeepEqual(r.m.sends, want) {
		t.Fatalf("sends %v, want %v", r.m.sends, want)
	}
}

func TestAGoneAddressIsClearedAndTheNextUpRunsInPlaceAgain(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	r.m.alive["dev"] = true
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	r.o.launcher = registeredLauncher{r.l, mux.Registered(r.m, "lead", leadAddressPath(r.o.home), nil)}

	r.m.alive["dev"] = false
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(rec.argvs) != 2 {
		t.Fatalf("execs %d, want 2: the stale address must not count as a running lead", len(rec.argvs))
	}
	if got, ok := readAddress(t, r.o); !ok || got != "dev" {
		t.Errorf("the second run records the new address, got %q, %v", got, ok)
	}
}

func TestUpStartsTheLeadAgainWhenItsTerminalOutlivesIt(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	r.m.alive["dev"] = true
	// The recorded process ended: the pid now belongs to a process with another start time.
	if err := agentproc.Write(registration.PidPath(r.o.home), agentproc.Record{Pid: os.Getpid(), Start: "not this process"}); err != nil {
		t.Fatal(err)
	}
	r.o.launcher = registeredLauncher{r.l, mux.Registered(r.m, "lead", leadAddressPath(r.o.home), leadGone(r.o.home), registration.PidPath(r.o.home))}
	r.out.Reset()

	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(rec.argvs) != 2 {
		t.Fatalf("execs %d, want 2: a terminal without its lead must not count as a running lead", len(rec.argvs))
	}
	if got, ok := readAddress(t, r.o); !ok || got != "dev" {
		t.Errorf("the second run records the address again, got %q, %v", got, ok)
	}
	if strings.Contains(r.out.String(), "The lead already runs") {
		t.Errorf("output %q says the lead runs", r.out.String())
	}
}

func TestUpRecordsTheLeadProcessBeforeItReplacesItself(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	want, err := agentproc.Self()
	if err != nil {
		t.Fatal(err)
	}
	var got agentproc.Record
	r.o.exec = func(dir string, argv []string) error {
		b, err := os.ReadFile(registration.PidPath(r.o.home))
		if err != nil {
			t.Errorf("no process record at the exec: %v", err)
		} else if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("process record %q: %v", b, err)
		}
		return rec.exec(dir, argv)
	}
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("process record %+v, want %+v", got, want)
	}
}

func TestUpRemovesTheProcessRecordWhenTheExecFails(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	rec.err = errors.New("exec failed")
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(registration.PidPath(r.o.home)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the process record stayed after the failed exec: %v", err)
	}
}

func TestUpShowsTheRecordedLeadAndStartsNothing(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	r.m.alive["dev"] = true
	r.m.shownLead = nil
	// The process record names the live test process, so the lead counts as running.
	r.o.launcher = registeredLauncher{r.l, mux.Registered(r.m, "lead", leadAddressPath(r.o.home), leadGone(r.o.home), registration.PidPath(r.o.home))}
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(rec.argvs) != 1 {
		t.Errorf("execs %d, want 1", len(rec.argvs))
	}
	if !reflect.DeepEqual(r.m.shownLead, []string{"dev"}) {
		t.Errorf("shown %v, want the recorded address", r.m.shownLead)
	}
}

func TestStopOfLeadUsesTheRecordedAddress(t *testing.T) {
	r, _ := inPlaceRig(t, "dev", nil)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	m := mux.Registered(r.m, "lead", leadAddressPath(r.o.home), nil)
	if err := m.Stop("lead"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.m.stops, []string{"dev"}) {
		t.Errorf("stops %v, want the recorded address", r.m.stops)
	}
	if _, ok := readAddress(t, r.o); ok {
		t.Error("the address stayed after the stop")
	}
}
