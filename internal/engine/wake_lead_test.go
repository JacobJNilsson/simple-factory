package engine_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
)

var leadWake = engine.SessionSpec{Name: "lead", Kind: "lead", Dir: "/work", Model: "opus", Prompt: "Start.", ResumePrompt: "sfy woke you."}

// goneLead returns an env whose lead session is gone, with lead waking set to on and a lead spec when spec is true.
func goneLead(t *testing.T, on, spec bool) env {
	t.Helper()
	v := newWakeEnv(t, config.Wake{Lead: on, MaxPerHour: 3})
	if spec {
		v.e.SetLeadSpec(func() (engine.SessionSpec, error) { return leadWake, nil })
	}
	v.l.dead = map[string]bool{"lead": true}
	return v
}

func TestAMessageWakesTheGoneLead(t *testing.T) {
	v := goneLead(t, true, true)

	mustOK(t, v.run("", "", "send", "", "lead", "hello"))
	v.tick(t)

	if !reflect.DeepEqual(v.l.started, []engine.SessionSpec{leadWake}) {
		t.Fatalf("started %+v", v.l.started)
	}
	if want := []string{"lead: sfy woke the lead, because a message arrived for it."}; !reflect.DeepEqual(v.l.notified, want) {
		t.Errorf("notified %q", v.l.notified)
	}
}

func TestTheLeadDoesNotWakeWithoutItsSettingOrItsSpec(t *testing.T) {
	for name, c := range map[string]struct{ on, spec bool }{
		"setting off": {false, true},
		"no spec":     {true, false},
	} {
		t.Run(name, func(t *testing.T) {
			v := goneLead(t, c.on, c.spec)

			mustOK(t, v.run("", "", "send", "", "lead", "hello"))
			v.tick(t)

			if len(v.l.started) != 0 || len(v.l.notified) != 0 {
				t.Fatalf("started %+v, notified %q", v.l.started, v.l.notified)
			}
		})
	}
}

func TestARegisteredLeadGetsNoWake(t *testing.T) {
	v := goneLead(t, true, true)
	v.e.SetRegistration(&fakeRegistration{registered: true})

	mustOK(t, v.run("", "", "send", "", "lead", "hello"))
	v.tick(t)

	if len(v.l.started) != 0 {
		t.Fatalf("started %+v", v.l.started)
	}
}

func TestAFailedLeadWakeNotifiesAndStoresNoMessage(t *testing.T) {
	v := goneLead(t, true, true)
	v.l.startErr = errors.New("no pane")

	mustOK(t, v.run("", "", "send", "", "lead", "hello"))
	v.tick(t)

	if want := []string{"lead: sfy could not wake the lead: no pane"}; !reflect.DeepEqual(v.l.notified, want) {
		t.Errorf("notified %q", v.l.notified)
	}
	if msgs := v.inbox(t, "lead"); len(msgs) != 1 || msgs[0].Body != "hello" {
		t.Errorf("lead inbox %+v, want only the message", msgs)
	}
}

func TestAFailedNotifyAfterALeadWakeOnlyWritesTheLog(t *testing.T) {
	v := goneLead(t, true, true)
	v.l.notifyErr = errors.New("no notify")
	var log bytes.Buffer
	v.e.SetLog(&log)

	mustOK(t, v.run("", "", "send", "", "lead", "hello"))
	v.tick(t)

	if got := strings.Count(log.String(), "notify of lead failed: no notify"); got != 1 {
		t.Errorf("log %q", log.String())
	}
}

func TestTheCapOfTheLeadOnlyWritesTheLog(t *testing.T) {
	v := goneLead(t, true, true)
	var log bytes.Buffer
	v.e.SetLog(&log)

	for range 4 {
		mustOK(t, v.run("", "", "send", "", "lead", "hello"))
		v.tick(t)
	}

	if len(v.l.started) != 3 {
		t.Fatalf("%d wakes, want 3", len(v.l.started))
	}
	if got := v.leadNotes(t, "times in the last hour"); len(got) != 0 {
		t.Errorf("the lead got %q", got)
	}
	if !strings.Contains(log.String(), "woke lead 3 times in the last hour") {
		t.Errorf("log %q", log.String())
	}
}
