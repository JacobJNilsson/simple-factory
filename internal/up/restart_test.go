package up

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

// runningRig returns a rig whose sfy process, lead and one worker already run.
// The lead started with the prompt that the factory folder holds now.
func runningRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.m.answering = true
	r.m.alive["sfy"] = true
	r.m.alive["lead"] = true
	r.m.alive["worker-t1"] = true
	if _, err := recordLeadPrompt(r.o.home, r.o.layers); err != nil {
		t.Fatal(err)
	}
	return r
}

// overrideLeadPrompt makes the next build's main prompt differ from the one the running lead has.
func overrideLeadPrompt(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "prompts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lead.md"), []byte("A new prompt.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// noteRequest returns the request that restart sends to give the lead a note.
func noteRequest(body string) protocol.Request {
	return protocol.Request{Command: "restarted", Body: body}
}

func TestRestartStopsAndStartsOnlyServe(t *testing.T) {
	r := runningRig(t)
	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}
	if want := []string{"sfy"}; !reflect.DeepEqual(r.m.stops, want) {
		t.Errorf("stops = %v, want %v", r.m.stops, want)
	}
	if len(r.m.starts) != 1 || r.m.starts[0][0] != "sfy" || !strings.HasSuffix(strings.Join(r.m.starts[0][2:], " "), " -- /bin/sfy serve") {
		t.Errorf("serve starts: %v", r.m.starts)
	}
	if len(r.l.specs) != 0 {
		t.Errorf("restart started the lead: %v", r.l.specs)
	}
	if !r.m.alive["lead"] {
		t.Error("the lead no longer runs")
	}
	want := "Stopped the sfy process.\nStarted the sfy process.\nTold the lead.\n"
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
		if name != "sfy" {
			t.Errorf("stopped %q", name)
		}
	}
}

func TestRestartNotesThatSfyRestarted(t *testing.T) {
	r := runningRig(t)
	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}
	if want := []protocol.Request{noteRequest("sfy restarted.")}; !reflect.DeepEqual(r.calls, want) || len(r.m.sends) != 0 {
		t.Errorf("requests %q, typed %q, want %q and nothing typed", r.calls, r.m.sends, want)
	}
}

// The lead often runs sfy restart itself, so its turn is busy and the pane takes no typed note.
func TestRestartTypesNothingIntoARegisteredLeadPane(t *testing.T) {
	r := runningRig(t)
	if err := registration.Write(r.o.home, registration.Record{ID: "s-main"}); err != nil {
		t.Fatal(err)
	}

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	if len(r.m.sends) != 0 {
		t.Fatalf("typed %q", r.m.sends)
	}
	if want := []protocol.Request{noteRequest("sfy restarted.")}; !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("requests %q, want %q", r.calls, want)
	}
}

func TestRestartFailsWhenTheNoteRequestFails(t *testing.T) {
	r := runningRig(t)
	closed := errors.New("socket closed")
	r.callErr = closed

	err := restart(r.o)

	if !errors.Is(err, closed) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(r.out.String(), "Told the lead.") {
		t.Errorf("output %q", r.out.String())
	}
}

func TestRestartPrintsWhatTheEngineSaysWhenItHoldsTheNote(t *testing.T) {
	r := runningRig(t)
	r.callText = "The lead gets the note when it waits."

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	if want := "Stopped the sfy process.\nStarted the sfy process.\nThe lead gets the note when it waits.\n"; r.out.String() != want {
		t.Errorf("output %q, want %q", r.out.String(), want)
	}
}

func TestRestartNotesAChangedPrompt(t *testing.T) {
	r := runningRig(t)
	overrideLeadPrompt(t, r.o.home)
	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.o.home, "lead-prompt.md")
	want := []protocol.Request{noteRequest("sfy restarted. Your prompt changed. Read the new text in " + path + " with the Read tool.")}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("requests = %q, want %q", r.calls, want)
	}
	if b, _ := os.ReadFile(path); string(b) != "A new prompt.\n" {
		t.Errorf("%s holds %q", path, b)
	}
}

func TestRestartNotesAChangedPromptAddition(t *testing.T) {
	r := runningRig(t)
	dir := filepath.Join(r.o.home, "prompts", "lead.d")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("Extra rule.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.o.home, "lead-prompt.md")
	want := []protocol.Request{noteRequest("sfy restarted. Your prompt changed. Read the new text in " + path + " with the Read tool.")}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("requests = %q, want %q", r.calls, want)
	}
	if b, _ := os.ReadFile(path); !strings.HasSuffix(string(b), "\n\nExtra rule.\n") {
		t.Errorf("%s lacks the addition", path)
	}
}

func TestRestartNotesAChangedPromptOnce(t *testing.T) {
	r := runningRig(t)
	overrideLeadPrompt(t, r.o.home)
	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}
	r.calls = nil
	r.m.alive["sfy"] = true
	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}
	want := []protocol.Request{noteRequest("sfy restarted.")}
	if !reflect.DeepEqual(r.calls, want) {
		t.Errorf("second restart requests = %q, want %q", r.calls, want)
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
	if len(r.m.sends) != 0 || len(r.calls) != 0 {
		t.Errorf("sends = %q, requests %q, want none for a new lead", r.m.sends, r.calls)
	}
	if r.out.String() != "Started the sfy process and the lead (new conversation).\n" {
		t.Errorf("output %q", r.out.String())
	}
}

func TestRestartWithDeadLeadStartsLead(t *testing.T) {
	r := runningRig(t)
	r.m.alive["lead"] = false
	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.m.stops, []string{"sfy"}) {
		t.Errorf("stops = %v", r.m.stops)
	}
	if len(r.l.specs) != 1 || len(r.m.sends) != 0 || len(r.calls) != 0 {
		t.Errorf("starts %v, sends %q, requests %q", r.l.specs, r.m.sends, r.calls)
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
	if len(r.m.starts) != 0 || len(r.l.specs) != 0 || len(r.m.sends) != 0 || len(r.calls) != 0 {
		t.Error("restart started or sent something after a failed stop")
	}
}

func TestRestartRefusesWorkersAndReviewers(t *testing.T) {
	for _, agent := range []string{"worker-t1", "review-t1-1"} {
		env := func(key string) string {
			if key == "SFY_AGENT" {
				return agent
			}
			return ""
		}
		var out, errOut strings.Builder
		if err := Restart(nil, env, &out, &errOut, "dev", false, false); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("agent %s: err = %v", agent, err)
		}
	}
}

func TestLeadMayRestart(t *testing.T) {
	for agent, want := range map[string]bool{"": true, "lead": true, "main": true, "worker-t1": false, "review-t1-1": false} {
		if got := mayRestart(agent); got != want {
			t.Errorf("mayRestart(%q) = %v, want %v", agent, got, want)
		}
	}
}
