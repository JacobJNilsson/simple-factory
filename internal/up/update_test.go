package up

import (
	"errors"
	"strings"
	"testing"
)

// fakeBrew records each brew command. Its list command answers with the first version before the upgrade ran and the second after.
type fakeBrew struct {
	calls    []string
	versions [2]string
	failOn   string
	output   string
}

func (f *fakeBrew) run(_ []string, args ...string) ([]byte, error) {
	cmd := strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	if cmd == f.failOn {
		return []byte(f.output), errors.New("exit status 1")
	}
	if args[0] == "list" {
		v := f.versions[0]
		if f.upgraded() {
			v = f.versions[1]
		}
		return []byte("sfy " + v + "\n"), nil
	}
	return nil, nil
}

func (f *fakeBrew) upgraded() bool {
	for _, c := range f.calls {
		if c == "upgrade sfy" {
			return true
		}
	}
	return false
}

func TestUpgradeNamesBothVersionsWhenTheVersionChanged(t *testing.T) {
	r := newRig(t)
	f := &fakeBrew{versions: [2]string{"0.1.0", "0.1.1"}}
	r.o.brew = f.run
	if err := upgrade(r.o); err != nil {
		t.Fatal(err)
	}
	if got := r.out.String(); got != "Upgraded sfy from 0.1.0 to 0.1.1.\n" {
		t.Fatalf("out = %q", got)
	}
}

func TestUpgradeSaysAlreadyNewestWhenTheVersionIsUnchanged(t *testing.T) {
	r := newRig(t)
	f := &fakeBrew{versions: [2]string{"0.1.1", "0.1.1"}}
	r.o.brew = f.run
	if err := upgrade(r.o); err != nil {
		t.Fatal(err)
	}
	if got := r.out.String(); got != "sfy 0.1.1 is already the newest version.\n" {
		t.Fatalf("out = %q", got)
	}
}

func TestUpgradeUpdatesTheTapBeforeTheUpgrade(t *testing.T) {
	r := newRig(t)
	f := &fakeBrew{versions: [2]string{"0.1.0", "0.1.1"}}
	r.o.brew = f.run
	if err := upgrade(r.o); err != nil {
		t.Fatal(err)
	}
	want := "list --cask --versions sfy|update|upgrade sfy|list --cask --versions sfy"
	if got := strings.Join(f.calls, "|"); got != want {
		t.Fatalf("brew calls = %q, want %q", got, want)
	}
}

func TestUpgradeReportsBrewsError(t *testing.T) {
	for _, failOn := range []string{"update", "upgrade sfy"} {
		t.Run(failOn, func(t *testing.T) {
			r := newRig(t)
			f := &fakeBrew{versions: [2]string{"0.1.0", "0.1.1"}, failOn: failOn, output: "Error: no available formula"}
			r.o.brew = f.run
			var be *brewError
			if err := upgrade(r.o); !errors.As(err, &be) || be.output != "Error: no available formula" {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(be.Error(), "brew "+failOn) {
				t.Fatalf("error does not name the command: %v", be)
			}
			if r.out.Len() != 0 {
				t.Fatalf("out = %q", r.out.String())
			}
		})
	}
}

func TestRestartUpdateUpgradesThenRestarts(t *testing.T) {
	r := runningRig(t)
	f := &fakeBrew{versions: [2]string{"0.1.0", "0.1.1"}}
	r.o.brew = f.run
	if err := restartCommand(r.o, true); err != nil {
		t.Fatal(err)
	}
	if !f.upgraded() || len(r.m.starts) != 1 {
		t.Fatalf("brew calls %v, serve starts %v", f.calls, r.m.starts)
	}
}

func TestRestartUpdateRestartsWhenAlreadyNewest(t *testing.T) {
	r := runningRig(t)
	f := &fakeBrew{versions: [2]string{"0.1.1", "0.1.1"}}
	r.o.brew = f.run
	if err := restartCommand(r.o, true); err != nil {
		t.Fatal(err)
	}
	if len(r.m.starts) != 1 {
		t.Fatalf("serve starts %v", r.m.starts)
	}
}

func TestRestartUpdateKeepsTheOldProcessWhenBrewFails(t *testing.T) {
	r := runningRig(t)
	f := &fakeBrew{versions: [2]string{"0.1.0", "0.1.1"}, failOn: "upgrade sfy", output: "boom"}
	r.o.brew = f.run
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
	r.o.brew = func([]string, ...string) ([]byte, error) { t.Fatal("brew ran"); return nil, nil }
	if err := restartCommand(r.o, false); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeReportsMissingBrew(t *testing.T) {
	r := newRig(t)
	r.o.brew = runBrew
	r.o.environ = []string{"PATH=" + t.TempDir()}
	if err := upgrade(r.o); err == nil {
		t.Fatal("want an error when brew is missing")
	}
}
