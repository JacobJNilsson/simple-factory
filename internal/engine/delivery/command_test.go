package delivery_test

// These tests check the rule for slash commands: sfy types a command only into a session that waits for input,
// and holds it until the next turn end or idle report otherwise. A command never goes through the channel.

import (
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/engine/delivery"
)

func TestACommandToASessionThatWaitsIsTypedAndNeverDelivered(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.idle("lead")

	outcome, err := r.d.Command("lead", "/effort high")

	if err != nil || outcome != delivery.Given {
		t.Fatalf("outcome %v, err %v", outcome, err)
	}
	r.wantTyped("lead: /effort high")
	if len(r.ch.delivered) != 0 {
		t.Errorf("delivered %q", r.ch.delivered)
	}
}

func TestACommandIsHeldUnlessTheSessionWaits(t *testing.T) {
	tests := []struct {
		name  string
		state func(r *rig)
	}{
		{"unknown state", func(*rig) {}},
		{"turn runs", func(r *rig) { r.d.Busy("lead") }},
		{"permission prompt", func(r *rig) { r.d.Busy("lead"); r.d.Prompted("lead") }},
		{"hook holds queued text", func(r *rig) { r.ch.canDeliver = true; r.idle("lead"); r.message("lead", "hi") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			tc.state(r)

			outcome, err := r.d.Command("lead", "/effort high")

			if err != nil || outcome != delivery.Held {
				t.Fatalf("outcome %v, err %v", outcome, err)
			}
			r.wantNothingTyped()
		})
	}
}

func TestAHeldCommandIsTypedAtTheTurnEndAndTheHeldNoteWaitsForTheNextWait(t *testing.T) {
	r := newRig(t)
	if _, err := r.d.Note("lead", "sfy restarted."); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.Command("lead", "/model opus"); err != nil {
		t.Fatal(err)
	}

	r.turnEnded("lead")
	r.wantTyped("lead: /model opus")

	r.idle("lead")
	r.wantTyped("lead: /model opus", "lead: sfy restarted.")
}

func TestHeldCommandsAreTypedOnePerWait(t *testing.T) {
	r := newRig(t)
	for _, text := range []string{"/model opus", "/effort high"} {
		if _, err := r.d.Command("lead", text); err != nil {
			t.Fatal(err)
		}
	}

	r.idle("lead")
	r.wantTyped("lead: /model opus")

	r.idle("lead")
	r.wantTyped("lead: /model opus", "lead: /effort high")
}

func TestATypedCommandMakesTheNextCommandWait(t *testing.T) {
	r := newRig(t)
	r.idle("lead")
	if _, err := r.d.Command("lead", "/compact"); err != nil {
		t.Fatal(err)
	}

	outcome, err := r.d.Command("lead", "/effort high")

	if err != nil || outcome != delivery.Held {
		t.Fatalf("outcome %v, err %v", outcome, err)
	}
	r.wantTyped("lead: /compact")
}

func TestATypedCommandKeepsTheTickFromTypingUntilTheNextWait(t *testing.T) {
	r := newRig(t)
	if _, err := r.d.Note("lead", "sfy restarted."); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.Command("lead", "/compact"); err != nil {
		t.Fatal(err)
	}
	r.idle("lead")

	r.advance(30 * time.Minute)
	r.tick("lead")
	r.wantTyped("lead: /compact")

	r.idle("lead")
	r.wantTyped("lead: /compact", "lead: sfy restarted.")
}

func TestTickNeverTypesAHeldCommand(t *testing.T) {
	r := newRig(t)
	if _, err := r.d.Command("lead", "/effort high"); err != nil {
		t.Fatal(err)
	}
	r.advance(30 * time.Minute)

	r.tick("lead")

	r.wantNothingTyped()
}

func TestCancelCommandsDropsTheHeldCommands(t *testing.T) {
	r := newRig(t)
	for _, text := range []string{"/model opus", "/effort high"} {
		if _, err := r.d.Command("lead", text); err != nil {
			t.Fatal(err)
		}
	}

	dropped := r.d.CancelCommands("lead")
	r.idle("lead")

	if dropped != 2 {
		t.Errorf("dropped %d, want 2", dropped)
	}
	r.wantNothingTyped()
}

func TestACommandToADeadSessionIsSkipped(t *testing.T) {
	r := newRig(t)
	r.panes.dead = map[string]bool{"lead": true}

	outcome, err := r.d.Command("lead", "/effort high")
	r.idle("lead")

	if err != nil || outcome != delivery.Skipped {
		t.Fatalf("outcome %v, err %v", outcome, err)
	}
	r.wantNothingTyped()
}
