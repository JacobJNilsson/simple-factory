package up

import (
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/mux"
	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

// registeredRig returns a rig whose main agent registered and runs at the address dev.
// Its launcher routes main through mux.Registered, as the real wiring does.
func registeredRig(t *testing.T) *rig {
	t.Helper()
	r := registerRig(t)
	r.m.answering = true
	if err := register(r.o); err != nil {
		t.Fatal(err)
	}
	r.m.alive["dev"] = true
	m := mux.Registered(r.m, "main", mainAddressPath(r.o.home), registration.Path(r.o.home))
	r.o.mux = m
	r.o.launcher = registeredLauncher{r.l, m}
	r.out.Reset()
	return r
}

func TestAGoneAddressClearsTheRegistrationAndTheNextUpStartsAMainAgent(t *testing.T) {
	r := registeredRig(t)
	r.m.alive["dev"] = false
	r.o.whoami = nil

	if err := run(r.o); err != nil {
		t.Fatal(err)
	}

	if _, ok := registration.Read(r.o.home); ok {
		t.Error("the registration stayed")
	}
	if len(r.l.specs) != 1 || r.l.specs[0].Name != "main" {
		t.Fatalf("starts %v, want a main agent started as usual", r.l.specs)
	}
}

func TestRestartKeepsARegisteredAgentAndSendsTheNote(t *testing.T) {
	r := registeredRig(t)
	r.m.alive["sfy"] = true

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	if want := [][2]string{{"dev", "sfy restarted."}}; !reflect.DeepEqual(r.m.sends, want) {
		t.Errorf("sends %v, want the note to the registered address", r.m.sends)
	}
	if len(r.l.specs) != 0 {
		t.Errorf("restart started a main agent: %v", r.l.specs)
	}
	if _, ok := registration.Read(r.o.home); !ok {
		t.Error("restart cleared the registration")
	}
}

func TestUpDoesNotShowARegisteredAgent(t *testing.T) {
	r := registeredRig(t)

	if err := run(r.o); err != nil {
		t.Fatal(err)
	}

	if len(r.m.shownMain) != 0 {
		t.Errorf("shown %v", r.m.shownMain)
	}
	if !strings.Contains(r.out.String(), "registered main agent already runs") {
		t.Errorf("output %q", r.out.String())
	}
}
