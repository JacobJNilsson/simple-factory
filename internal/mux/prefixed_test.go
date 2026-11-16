package mux

import (
	"slices"
	"testing"
)

type recordingMux struct{ calls []string }

func (r *recordingMux) record(call, name string) { r.calls = append(r.calls, call+" "+name) }

func (r *recordingMux) Start(name, _ string, _ []string) error { r.record("start", name); return nil }
func (r *recordingMux) Send(name, _ string) error              { r.record("send", name); return nil }
func (r *recordingMux) Alive(name string) (bool, error)        { r.record("alive", name); return true, nil }
func (r *recordingMux) Stop(name string) error                 { r.record("stop", name); return nil }
func (r *recordingMux) Notify(name, _ string) error            { r.record("notify", name); return nil }
func (r *recordingMux) Show(name string) error                 { r.record("show", name); return nil }
func (r *recordingMux) ShowMain(name string) error             { r.record("showmain", name); return nil }

func TestPrefixedPutsThePrefixInFrontOfEverySessionName(t *testing.T) {
	r := &recordingMux{}
	m := Prefixed(r, "sfy-work-")

	_ = m.Start("main", "/d", []string{"x"})
	_ = m.Send("main", "hi")
	_, _ = m.Alive("worker-t1")
	_ = m.Notify("sfy", "n")
	_ = m.Show("worker-t1")
	_ = m.ShowMain("main")
	_ = m.Stop("main")

	want := []string{
		"start sfy-work-main", "send sfy-work-main", "alive sfy-work-worker-t1", "notify sfy-work-sfy",
		"show sfy-work-worker-t1", "showmain sfy-work-main", "stop sfy-work-main",
	}
	if !slices.Equal(r.calls, want) {
		t.Fatalf("got %q, want %q", r.calls, want)
	}
}

func TestPrefixedWithAnEmptyPrefixIsTheSameMux(t *testing.T) {
	r := &recordingMux{}
	if Prefixed(r, "") != Mux(r) {
		t.Fatal("an empty prefix must return the Mux itself")
	}
}
