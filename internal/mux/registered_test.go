package mux

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// aliveMux answers Alive with a fixed result and records the names that each call received.
type aliveMux struct {
	recordingMux
	alive bool
	err   error
}

func (a *aliveMux) Alive(name string) (bool, error) {
	a.record("alive", name)
	return a.alive, a.err
}

func addressFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "address")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestRegisteredSendsTheLeadCallsToTheRecordedAddress(t *testing.T) {
	a := &aliveMux{alive: true}
	m := Registered(a, "lead", addressFile(t, "dev\n"), nil)

	_ = m.Send("lead", "hi")
	_ = m.Notify("lead", "n")
	_ = m.Show("lead")
	_ = m.ShowLead("lead")
	_, _ = m.Alive("lead")
	_ = m.Send("worker-t1", "hi")

	want := []string{"send dev", "notify dev", "show dev", "showlead dev", "alive dev", "send worker-t1"}
	if !slices.Equal(a.calls, want) {
		t.Fatalf("got %q, want %q", a.calls, want)
	}
}

func TestRegisteredLeavesTheCallsAloneWithoutARecord(t *testing.T) {
	a := &aliveMux{alive: true}
	m := Registered(a, "lead", addressFile(t, ""), nil)

	_ = m.Start("lead", "/d", nil)
	_ = m.Send("lead", "hi")
	_, _ = m.Alive("lead")

	want := []string{"start lead", "send lead", "alive lead"}
	if !slices.Equal(a.calls, want) {
		t.Fatalf("got %q, want %q", a.calls, want)
	}
}

func TestRegisteredClearsTheRecordWhenTheAddressIsGone(t *testing.T) {
	path := addressFile(t, "dev")
	a := &aliveMux{alive: false}
	m := Registered(a, "lead", path, nil)

	alive, err := m.Alive("lead")
	if err != nil || alive {
		t.Fatalf("got %v, %v, want false and no error", alive, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the record must be gone, stat error: %v", err)
	}
	_ = m.Send("lead", "hi")
	if got := a.calls[len(a.calls)-1]; got != "send lead" {
		t.Fatalf("after the clear, send must use the name, got %q", got)
	}
}

func TestRegisteredClearsTheExtraFilesWithTheAddress(t *testing.T) {
	extra := addressFile(t, "{}")
	m := Registered(&aliveMux{alive: false}, "lead", addressFile(t, "dev"), nil, extra)

	if _, err := m.Alive("lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(extra); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the extra file must be gone, stat error: %v", err)
	}
}

func TestRegisteredKeepsTheRecordWhenAliveFails(t *testing.T) {
	path := addressFile(t, "dev")
	a := &aliveMux{err: errors.New("tool down")}
	m := Registered(a, "lead", path, nil)

	if _, err := m.Alive("lead"); err == nil {
		t.Fatal("want the error")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the record must stay: %v", err)
	}
}

func TestRegisteredKeepsTheRecordWhileTheAddressRuns(t *testing.T) {
	path := addressFile(t, "dev")
	m := Registered(&aliveMux{alive: true}, "lead", path, nil)

	if _, err := m.Alive("lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the record must stay: %v", err)
	}
}

// goneFunc returns a gone function that answers gone and err, and counts its calls in calls.
func goneFunc(gone bool, err error, calls *int) func() (bool, error) {
	return func() (bool, error) {
		*calls++
		return gone, err
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestRegisteredForgetsAnAddressWhoseAgentIsGone(t *testing.T) {
	path, extra := addressFile(t, "dev"), addressFile(t, "{}")
	calls := 0
	m := Registered(&aliveMux{alive: true}, "lead", path, goneFunc(true, nil, &calls), extra)

	alive, err := m.Alive("lead")
	if err != nil || alive {
		t.Fatalf("got %v, %v, want false and no error", alive, err)
	}
	if calls != 1 {
		t.Errorf("gone ran %d times, want 1", calls)
	}
	if exists(t, path) || exists(t, extra) {
		t.Error("the address and the extra file must be gone")
	}
}

func TestRegisteredKeepsAnAddressWhoseAgentRuns(t *testing.T) {
	path, extra := addressFile(t, "dev"), addressFile(t, "{}")
	calls := 0
	m := Registered(&aliveMux{alive: true}, "lead", path, goneFunc(false, nil, &calls), extra)

	alive, err := m.Alive("lead")
	if err != nil || !alive {
		t.Fatalf("got %v, %v, want true and no error", alive, err)
	}
	if calls != 1 {
		t.Errorf("gone ran %d times, want 1", calls)
	}
	if !exists(t, path) || !exists(t, extra) {
		t.Error("the address and the extra file must stay")
	}
}

func TestRegisteredKeepsTheAddressWhenGoneFails(t *testing.T) {
	path, extra := addressFile(t, "dev"), addressFile(t, "{}")
	calls := 0
	m := Registered(&aliveMux{alive: true}, "lead", path, goneFunc(false, errors.New("no ps"), &calls), extra)

	if _, err := m.Alive("lead"); err == nil {
		t.Fatal("want the error")
	}
	if calls != 1 {
		t.Errorf("gone ran %d times, want 1", calls)
	}
	if !exists(t, path) || !exists(t, extra) {
		t.Error("the address and the extra file must stay")
	}
}

func TestRegisteredAsksNotForTheAgentWhenTheTerminalIsGone(t *testing.T) {
	path := addressFile(t, "dev")
	calls := 0
	m := Registered(&aliveMux{alive: false}, "lead", path, goneFunc(false, nil, &calls))

	if alive, err := m.Alive("lead"); err != nil || alive {
		t.Fatalf("got %v, %v, want false and no error", alive, err)
	}
	if calls != 0 {
		t.Errorf("gone ran %d times, want 0", calls)
	}
	if exists(t, path) {
		t.Error("the address must be gone")
	}
}

func TestRegisteredAsksNotForTheAgentWithoutAnAddress(t *testing.T) {
	calls := 0
	m := Registered(&aliveMux{alive: true}, "lead", addressFile(t, ""), goneFunc(true, nil, &calls))

	if alive, err := m.Alive("lead"); err != nil || !alive {
		t.Fatalf("got %v, %v, want true and no error", alive, err)
	}
	if calls != 0 {
		t.Errorf("gone ran %d times, want 0", calls)
	}
}

func TestRegisteredStopUsesTheAddressAndClearsTheRecord(t *testing.T) {
	path := addressFile(t, "dev")
	a := &aliveMux{alive: true}
	m := Registered(a, "lead", path, nil)

	if err := m.Stop("lead"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"stop dev"}; !slices.Equal(a.calls, want) {
		t.Fatalf("got %q, want %q", a.calls, want)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the record must be gone, stat error: %v", err)
	}
}

func TestRegisteredInsidePrefixedKeepsTheAddressUnprefixed(t *testing.T) {
	a := &aliveMux{alive: true}
	m := Prefixed(Registered(a, "pre-lead", addressFile(t, "dev"), nil), "pre-")

	_ = m.Send("lead", "hi")
	_ = m.Send("worker-t1", "hi")

	want := []string{"send dev", "send pre-worker-t1"}
	if !slices.Equal(a.calls, want) {
		t.Fatalf("got %q, want %q", a.calls, want)
	}
}
