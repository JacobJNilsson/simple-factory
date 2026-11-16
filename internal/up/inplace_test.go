package up

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/mux"
)

// execRecorder replaces the real exec. It records each argv and returns err.
type execRecorder struct {
	argvs [][]string
	err   error
}

func (e *execRecorder) exec(argv []string) error {
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
	b, err := os.ReadFile(mainAddressPath(o.home))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b)), true
}

func TestUpRunsTheMainAgentInPlaceWhenWhoamiPrintsAnAddress(t *testing.T) {
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
		t.Errorf("the set started a main agent as well: %v", r.l.specs)
	}
	if len(r.l.commands) != 1 || r.l.commands[0].Name != "main" || r.l.commands[0].Dir != r.o.home {
		t.Errorf("command specs %+v", r.l.commands)
	}
	if len(r.m.starts) != 1 || r.m.starts[0][0] != "sfy" {
		t.Errorf("only the sfy process starts in the background, got %v", r.m.starts)
	}
}

func TestUpStartsTheSfyProcessBeforeItReplacesItself(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	r.o.exec = func(argv []string) error {
		if len(r.m.starts) != 1 {
			t.Errorf("the sfy process must run before the exec, starts %v", r.m.starts)
		}
		return rec.exec(argv)
	}
	if err := run(r.o); err != nil {
		t.Fatal(err)
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

func TestUpShowsAMainAgentThatAlreadyRunsAndDoesNotAskWhoami(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	r.m.alive["main"] = true
	asked := false
	r.o.whoami = func() (string, error) { asked = true; return "dev", nil }
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if asked || len(rec.argvs) != 0 {
		t.Errorf("asked %v, execs %v, want neither", asked, rec.argvs)
	}
	if !reflect.DeepEqual(r.m.shownMain, []string{"main"}) {
		t.Errorf("shown %v, want the main agent", r.m.shownMain)
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
func (l registeredLauncher) ShowMain(name string) error { return l.m.ShowMain(name) }

func TestSendToMainUsesTheRecordedAddress(t *testing.T) {
	r, _ := inPlaceRig(t, "dev", nil)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	m := mux.Registered(r.m, "main", mainAddressPath(r.o.home))
	if err := m.Send("main", "hi"); err != nil {
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
	r.o.launcher = registeredLauncher{r.l, mux.Registered(r.m, "main", mainAddressPath(r.o.home))}

	r.m.alive["dev"] = false
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(rec.argvs) != 2 {
		t.Fatalf("execs %d, want 2: the stale address must not count as a running main agent", len(rec.argvs))
	}
	if got, ok := readAddress(t, r.o); !ok || got != "dev" {
		t.Errorf("the second run records the new address, got %q, %v", got, ok)
	}
}

func TestUpShowsTheRecordedMainAgentAndStartsNothing(t *testing.T) {
	r, rec := inPlaceRig(t, "dev", nil)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	r.m.alive["dev"] = true
	r.o.launcher = registeredLauncher{r.l, mux.Registered(r.m, "main", mainAddressPath(r.o.home))}
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	if len(rec.argvs) != 1 {
		t.Errorf("execs %d, want 1", len(rec.argvs))
	}
	if !reflect.DeepEqual(r.m.shownMain, []string{"dev"}) {
		t.Errorf("shown %v, want the recorded address", r.m.shownMain)
	}
}

func TestStopOfMainUsesTheRecordedAddress(t *testing.T) {
	r, _ := inPlaceRig(t, "dev", nil)
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	m := mux.Registered(r.m, "main", mainAddressPath(r.o.home))
	if err := m.Stop("main"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.m.stops, []string{"dev"}) {
		t.Errorf("stops %v, want the recorded address", r.m.stops)
	}
	if _, ok := readAddress(t, r.o); ok {
		t.Error("the address stayed after the stop")
	}
}
