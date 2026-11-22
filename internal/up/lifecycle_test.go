package up

import (
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/mux"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

// registeredRig returns a rig whose lead registered and runs at the address dev.
// Its launcher routes main through mux.Registered, as the real wiring does.
func registeredRig(t *testing.T) *rig {
	t.Helper()
	r := registerRig(t)
	r.m.answering = true
	if err := register(r.o); err != nil {
		t.Fatal(err)
	}
	r.m.alive["dev"] = true
	m := mux.Registered(r.m, "lead", leadAddressPath(r.o.home), registration.Path(r.o.home))
	r.o.mux = m
	r.o.launcher = registeredLauncher{r.l, m}
	r.out.Reset()
	return r
}

func TestAGoneAddressClearsTheRegistrationAndTheNextUpStartsALead(t *testing.T) {
	r := registeredRig(t)
	r.m.alive["dev"] = false
	r.o.whoami = nil

	if err := run(r.o); err != nil {
		t.Fatal(err)
	}

	if _, ok := registration.Read(r.o.home); ok {
		t.Error("the registration stayed")
	}
	if len(r.l.specs) != 1 || r.l.specs[0].Name != "lead" {
		t.Fatalf("starts %v, want a lead started as usual", r.l.specs)
	}
}

func TestRestartKeepsARegisteredAgentAndSendsTheNote(t *testing.T) {
	r := registeredRig(t)
	r.m.alive["sfy"] = true

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	if want := []protocol.Request{{Command: "restarted", Body: "sfy restarted."}}; !reflect.DeepEqual(r.calls, want) || len(r.m.sends) != 0 {
		t.Errorf("requests %v, typed %v, want the note request and nothing typed", r.calls, r.m.sends)
	}
	if len(r.l.specs) != 0 {
		t.Errorf("restart started a lead: %v", r.l.specs)
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

	if len(r.m.shownLead) != 0 {
		t.Errorf("shown %v", r.m.shownLead)
	}
	if !strings.Contains(r.out.String(), "registered lead already runs") {
		t.Errorf("output %q", r.out.String())
	}
}
