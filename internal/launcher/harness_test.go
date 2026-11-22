package launcher

import (
	"errors"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/provider"
)

func TestStartPicksTheAdapterOfTheHarness(t *testing.T) {
	m, claude, other := &fakeMux{}, &fakeProvider{}, &fakeProvider{}
	l := WithAdapters(m, map[string]provider.Provider{"claude": claude, "other": other}, "claude", nil)

	if err := l.Start(engine.SessionSpec{Name: "worker-t1", Kind: "worker", Harness: "other"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(engine.SessionSpec{Name: "review-t1-1", Kind: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if len(other.specs) != 1 || other.specs[0].Name != "worker-t1" || len(claude.specs) != 1 || claude.specs[0].Name != "review-t1-1" {
		t.Fatalf("other started %v and claude started %v", other.specs, claude.specs)
	}
}

func TestStartRefusesAHarnessWithNoAdapter(t *testing.T) {
	m := &fakeMux{}
	err := New(m, &fakeProvider{}, nil).Start(engine.SessionSpec{Name: "worker-t1", Kind: "worker", Harness: "other"})

	var unknown *UnknownHarnessError
	if !errors.As(err, &unknown) || unknown.Harness != "other" || len(m.starts) != 0 {
		t.Fatalf("err %v, starts %v", err, m.starts)
	}
}
