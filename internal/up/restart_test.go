package up

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// runningRig returns a rig whose sfy process, main agent and one worker already run.
func runningRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.m.answering = true
	r.m.alive["sfy"] = true
	r.m.alive["main"] = true
	r.m.alive["worker-t1"] = true
	return r
}

func TestRestartStopsAndStartsServeAndMain(t *testing.T) {
	r := runningRig(t)
	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}
	if want := []string{"sfy", "main"}; !reflect.DeepEqual(r.m.stops, want) {
		t.Errorf("stops = %v, want %v", r.m.stops, want)
	}
	if len(r.m.starts) != 1 || r.m.starts[0][0] != "sfy" || !strings.HasSuffix(strings.Join(r.m.starts[0][2:], " "), " -- /bin/sfy serve") {
		t.Errorf("serve starts: %v", r.m.starts)
	}
	if len(r.l.specs) != 1 || r.l.specs[0].Name != "main" || !strings.Contains(r.l.specs[0].Prompt, "notes.md") {
		t.Errorf("main starts: %v", r.l.specs)
	}
	want := "Stopped the sfy process and the main agent.\nStarted the sfy process and the main agent.\n"
	if r.out.String() != want {
		t.Errorf("output %q, want %q", r.out.String(), want)
	}
}

func TestRestartKeepsWorkersRunning(t *testing.T) {
	r := runningRig(t)
	r.m.alive["review-t1-1"] = true
	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}
	if !r.m.alive["worker-t1"] || !r.m.alive["review-t1-1"] {
		t.Errorf("agents after restart: %v", r.m.alive)
	}
	for _, name := range r.m.stops {
		if name != "sfy" && name != "main" {
			t.Errorf("stopped %q", name)
		}
	}
}

func TestRestartWithNothingRunningActsLikeUp(t *testing.T) {
	r := newRig(t)
	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}
	if len(r.m.stops) != 0 {
		t.Errorf("stops = %v", r.m.stops)
	}
	if len(r.m.starts) != 1 || len(r.l.specs) != 1 {
		t.Errorf("starts: %v %v", r.m.starts, r.l.specs)
	}
	if r.out.String() != "Started the sfy process and the main agent.\n" {
		t.Errorf("output %q", r.out.String())
	}
}

func TestRestartWithDeadMainStopsOnlyServe(t *testing.T) {
	r := runningRig(t)
	r.m.alive["main"] = false
	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.m.stops, []string{"sfy"}) {
		t.Errorf("stops = %v", r.m.stops)
	}
	if !strings.HasPrefix(r.out.String(), "Stopped the sfy process.\nStarted ") {
		t.Errorf("output %q", r.out.String())
	}
}

func TestRestartFailsWhenServeKeepsAnswering(t *testing.T) {
	r := runningRig(t)
	r.m.stubborn = true
	r.o.wait = 20 * time.Millisecond
	if err := restart(r.o); !errors.Is(err, ErrNotStopped) {
		t.Fatalf("err = %v", err)
	}
	if len(r.m.starts) != 0 || len(r.l.specs) != 0 {
		t.Error("restart started something after a failed stop")
	}
}

func TestRestartRefusesEveryAgent(t *testing.T) {
	for _, agent := range []string{"main", "worker-t1", "review-t1-1"} {
		env := func(key string) string {
			if key == "SFY_AGENT" {
				return agent
			}
			return ""
		}
		var out, errOut strings.Builder
		if err := Restart(nil, env, &out, &errOut); !errors.Is(err, ErrNotOperator) {
			t.Errorf("agent %s: err = %v", agent, err)
		}
	}
}
