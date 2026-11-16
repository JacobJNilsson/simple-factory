package engine_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// fakeRegistration is a registration that the test sets and reads.
type fakeRegistration struct {
	registered bool
	cleared    int
	err        error
}

func (f *fakeRegistration) Registered() bool { return f.registered }
func (f *fakeRegistration) Clear() error {
	if f.err != nil {
		return f.err
	}
	f.registered = false
	f.cleared++
	return nil
}

func TestDownLeavesARegisteredMainRunningAndClearsTheRegistration(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 1)
	reg := &fakeRegistration{registered: true}
	v.e.SetRegistration(reg)

	a := v.run("", "", "down", "")

	mustOK(t, a)
	if want := []string{"worker-t1", "review-t1-1"}; !slices.Equal(v.l.stopped, want) {
		t.Fatalf("stopped %v, want %v", v.l.stopped, want)
	}
	if reg.cleared != 1 {
		t.Errorf("cleared %d times", reg.cleared)
	}
	want := "Stopped worker-t1, review-t1-1 and the sfy process. Left main running and cleared its registration, because sfy did not start it."
	if a.Text != want || !v.e.Stopping() {
		t.Fatalf("text %q", a.Text)
	}
}

func TestDownWithOnlyARegisteredMainStopsNoSession(t *testing.T) {
	v := newEnv(t)
	reg := &fakeRegistration{registered: true}
	v.e.SetRegistration(reg)

	a := v.run("", "", "down", "")

	mustOK(t, a)
	if len(v.l.stopped) != 0 {
		t.Fatalf("stopped %v", v.l.stopped)
	}
	want := "Stopped the sfy process. Left main running and cleared its registration, because sfy did not start it."
	if a.Text != want {
		t.Fatalf("text %q", a.Text)
	}
}

func TestDownStopsMainWhenNothingRegistered(t *testing.T) {
	v := newEnv(t)
	v.e.SetRegistration(&fakeRegistration{})

	a := v.run("", "", "down", "")

	mustOK(t, a)
	if !slices.Equal(v.l.stopped, []string{"main"}) || a.Text != "Stopped main and the sfy process." {
		t.Fatalf("stopped %v, text %q", v.l.stopped, a.Text)
	}
}

func TestDownKeepsRunningWhenTheRegistrationCannotBeCleared(t *testing.T) {
	v := newEnv(t)
	v.e.SetRegistration(&fakeRegistration{registered: true, err: errors.New("read-only folder")})

	mustFail(t, v.run("", "", "down", ""), "read-only folder")

	if v.e.Stopping() {
		t.Fatal("the engine stops although the registration stayed")
	}
}
