package up

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unknownTemplateEnv returns an environment whose config holds a template key that this code does not know.
func unknownTemplateEnv(t *testing.T) func(string) string {
	t.Helper()
	home := t.TempDir()
	cfg := "multiplexer = \"herdr\"\n[multiplexers.herdr]\nfuture_template = \"x\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return func(key string) string {
		if key == "SFY_HOME" {
			return home
		}
		return ""
	}
}

func TestRestartUpdateUpgradesBeforeItReadsTheConfig(t *testing.T) {
	f := &fakeBrew{versions: [2]string{"0.1.3", "0.1.4"}}
	reruns := 0
	u := updater{brew: f.run, rerun: func([]string, io.Writer, io.Writer) error { reruns++; return nil }}
	var out, errOut strings.Builder
	if err := restartWith(u, nil, unknownTemplateEnv(t), &out, &errOut, true); err != nil {
		t.Fatal(err)
	}
	if !f.upgraded() {
		t.Errorf("brew calls %v, want the upgrade to run", f.calls)
	}
	if reruns != 1 {
		t.Errorf("the new binary ran %d times, want 1", reruns)
	}
}

func TestRestartUpdateReturnsTheNewBinarysError(t *testing.T) {
	f := &fakeBrew{versions: [2]string{"0.1.3", "0.1.4"}}
	u := updater{brew: f.run, rerun: func([]string, io.Writer, io.Writer) error { return errors.New("bad config") }}
	var out, errOut strings.Builder
	if err := restartWith(u, nil, unknownTemplateEnv(t), &out, &errOut, true); err == nil || err.Error() != "bad config" {
		t.Fatalf("err = %v", err)
	}
}

func TestRestartWithoutUpdateSkipsBrewAndValidatesTheConfig(t *testing.T) {
	u := updater{
		brew:  func([]string, ...string) ([]byte, error) { t.Fatal("brew ran"); return nil, nil },
		rerun: func([]string, io.Writer, io.Writer) error { t.Fatal("the new binary ran"); return nil },
	}
	var out, errOut strings.Builder
	if err := restartWith(u, nil, unknownTemplateEnv(t), &out, &errOut, false); err == nil {
		t.Fatal("want a config error")
	}
}

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

func TestRestartUpdateSkipsTheRestartWhenBrewFails(t *testing.T) {
	f := &fakeBrew{versions: [2]string{"0.1.0", "0.1.1"}, failOn: "upgrade sfy", output: "boom"}
	u := updater{brew: f.run, rerun: func([]string, io.Writer, io.Writer) error { t.Fatal("the new binary ran"); return nil }}
	var out, errOut strings.Builder
	var be *brewError
	if err := restartWith(u, nil, unknownTemplateEnv(t), &out, &errOut, true); !errors.As(err, &be) || be.output != "boom" {
		t.Fatalf("err = %v", err)
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
