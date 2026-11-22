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

func TestRegisteredSendsTheMainCallsToTheRecordedAddress(t *testing.T) {
	a := &aliveMux{alive: true}
	m := Registered(a, "lead", addressFile(t, "dev\n"))

	_ = m.Send("lead", "hi")
	_ = m.Notify("lead", "n")
	_ = m.Show("lead")
	_ = m.ShowMain("lead")
	_, _ = m.Alive("lead")
	_ = m.Send("worker-t1", "hi")

	want := []string{"send dev", "notify dev", "show dev", "showmain dev", "alive dev", "send worker-t1"}
	if !slices.Equal(a.calls, want) {
		t.Fatalf("got %q, want %q", a.calls, want)
	}
}

func TestRegisteredLeavesTheCallsAloneWithoutARecord(t *testing.T) {
	a := &aliveMux{alive: true}
	m := Registered(a, "lead", addressFile(t, ""))

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
	m := Registered(a, "lead", path)

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
	m := Registered(&aliveMux{alive: false}, "lead", addressFile(t, "dev"), extra)

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
	m := Registered(a, "lead", path)

	if _, err := m.Alive("lead"); err == nil {
		t.Fatal("want the error")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the record must stay: %v", err)
	}
}

func TestRegisteredKeepsTheRecordWhileTheAddressRuns(t *testing.T) {
	path := addressFile(t, "dev")
	m := Registered(&aliveMux{alive: true}, "lead", path)

	if _, err := m.Alive("lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the record must stay: %v", err)
	}
}

func TestRegisteredStopUsesTheAddressAndClearsTheRecord(t *testing.T) {
	path := addressFile(t, "dev")
	a := &aliveMux{alive: true}
	m := Registered(a, "lead", path)

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
	m := Prefixed(Registered(a, "pre-lead", addressFile(t, "dev")), "pre-")

	_ = m.Send("lead", "hi")
	_ = m.Send("worker-t1", "hi")

	want := []string{"send dev", "send pre-worker-t1"}
	if !slices.Equal(a.calls, want) {
		t.Fatalf("got %q, want %q", a.calls, want)
	}
}
