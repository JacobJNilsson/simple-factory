package up

import (
	"errors"
	"strings"
	"testing"
)

func TestUpgradeSucceedsWhenBrewSucceeds(t *testing.T) {
	r := newRig(t)
	r.o.brew = func([]string) ([]byte, error) { return []byte("already installed"), nil }
	if err := upgrade(r.o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.out.String(), "Upgraded sfy") {
		t.Fatalf("out = %q", r.out.String())
	}
}

func TestUpgradeReportsBrewsError(t *testing.T) {
	r := newRig(t)
	r.o.brew = func([]string) ([]byte, error) {
		return []byte("Error: no available formula"), errors.New("exit status 1")
	}
	var be *brewError
	if err := upgrade(r.o); !errors.As(err, &be) || be.output != "Error: no available formula" {
		t.Fatalf("err = %v", err)
	}
}

func TestRestartUpdateUpgradesThenRestarts(t *testing.T) {
	r := runningRig(t)
	var calls []string
	r.o.brew = func([]string) ([]byte, error) { calls = append(calls, "brew"); return nil, nil }
	if err := restartCommand(r.o, true); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || len(r.m.starts) != 1 {
		t.Fatalf("brew calls %v, serve starts %v", calls, r.m.starts)
	}
}

func TestRestartUpdateKeepsTheOldProcessWhenBrewFails(t *testing.T) {
	r := runningRig(t)
	r.o.brew = func([]string) ([]byte, error) { return []byte("boom"), errors.New("exit status 1") }
	var be *brewError
	if err := restartCommand(r.o, true); !errors.As(err, &be) || be.output != "boom" {
		t.Fatalf("err = %v", err)
	}
	if len(r.m.starts) != 0 || !r.m.alive["sfy"] {
		t.Fatalf("sfy was restarted: starts %v, alive %v", r.m.starts, r.m.alive)
	}
}

func TestRestartWithoutUpdateSkipsBrew(t *testing.T) {
	r := runningRig(t)
	r.o.brew = func([]string) ([]byte, error) { t.Fatal("brew ran"); return nil, nil }
	if err := restartCommand(r.o, false); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeReportsMissingBrew(t *testing.T) {
	r := newRig(t)
	r.o.brew = brewUpgrade
	r.o.environ = []string{"PATH=" + t.TempDir()}
	if err := upgrade(r.o); err == nil {
		t.Fatal("want an error when brew is missing")
	}
}
