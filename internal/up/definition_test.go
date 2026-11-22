package up

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
)

// recordDefinition writes definition.toml as a start of the sfy process with the rig's config and prompt files does.
func recordDefinition(t *testing.T, r *rig) {
	t.Helper()
	d, err := buildDefinition(r.o.config, r.o.layers)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDefinition(r.o.home, d); err != nil {
		t.Fatal(err)
	}
}

func TestRestartReportsAChangedKeyAndTellsTheLead(t *testing.T) {
	r := runningRig(t)
	recordDefinition(t, r)
	r.o.config.Models.Worker = "opus"

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	line := `Setting models.worker changed from "" to "opus".`
	if !strings.HasPrefix(r.out.String(), line+"\nStopped the sfy process.\n") {
		t.Errorf("output %q, want it to start with %q", r.out.String(), line)
	}
	if want := []protocol.Request{noteRequest("sfy restarted. " + line)}; !reflect.DeepEqual(r.calls, want) {
		t.Errorf("requests %q, want %q", r.calls, want)
	}
}

func TestRestartReportsARemovedPromptFile(t *testing.T) {
	r := runningRig(t)
	addition := filepath.Join(r.o.home, "prompts", "worker.d", "writing.md")
	writeTestFile(t, addition, "Write short sentences.\n")
	recordDefinition(t, r)
	if err := os.Remove(addition); err != nil {
		t.Fatal(err)
	}

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	line := "The worker prompt file worker.d/writing.md was removed."
	if !strings.HasPrefix(r.out.String(), line+"\n") {
		t.Errorf("output %q, want it to start with %q", r.out.String(), line)
	}
}

func TestRestartReportsAChangedPromptFile(t *testing.T) {
	r := runningRig(t)
	addition := filepath.Join(r.o.home, "prompts", "reviewer.d", "a.md")
	writeTestFile(t, addition, "one\n")
	recordDefinition(t, r)
	writeTestFile(t, addition, "two\n")
	writeTestFile(t, filepath.Join(r.o.home, "prompts", "reviewer.md"), "base\n")

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	want := "The reviewer prompt file reviewer.d/a.md was changed.\nThe reviewer prompt file reviewer.md was added.\n"
	if !strings.HasPrefix(r.out.String(), want) {
		t.Errorf("output %q, want it to start with %q", r.out.String(), want)
	}
}

func TestRestartSaysSoInOneLineWhenNothingChanged(t *testing.T) {
	r := runningRig(t)
	recordDefinition(t, r)

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	want := noChange + "\nStopped the sfy process.\nStarted the sfy process.\nTold the lead.\n"
	if r.out.String() != want {
		t.Errorf("output %q, want %q", r.out.String(), want)
	}
	if want := []protocol.Request{noteRequest("sfy restarted. " + noChange)}; !reflect.DeepEqual(r.calls, want) {
		t.Errorf("requests %q, want %q", r.calls, want)
	}
}

func TestRestartReportsNothingOnAFirstStart(t *testing.T) {
	r := runningRig(t)

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	if want := "Stopped the sfy process.\nStarted the sfy process.\nTold the lead.\n"; r.out.String() != want {
		t.Errorf("output %q, want %q", r.out.String(), want)
	}
}

func TestRestartWarnsAndGoesOnWhenTheDefinitionFileIsBroken(t *testing.T) {
	r := runningRig(t)
	writeTestFile(t, filepath.Join(r.o.home, definitionName), "settings = \n")

	if err := restart(r.o); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(r.err.String(), "no definition report") || !strings.HasPrefix(r.out.String(), "Stopped the sfy process.\n") {
		t.Errorf("warnings %q, output %q", r.err.String(), r.out.String())
	}
}

func TestTheStartOfTheSfyProcessWritesTheDefinition(t *testing.T) {
	// A short folder keeps the socket path within the system limit.
	home, err := os.MkdirTemp("", "fd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	userHome := t.TempDir()
	writeTestFile(t, filepath.Join(home, "config.toml"), "max_workers = 3\n")
	writeTestFile(t, filepath.Join(home, "prompts", "worker.d", "writing.md"), "Write short sentences.\n")
	env := func(k string) string { return map[string]string{"HOME": userHome, "SFY_HOME": home}[k] }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, env, io.Discard) }()
	waitAnswering(t, socket(home))
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(home, definitionName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
	cfg, err := config.LoadFactory(home, userHome)
	if err != nil {
		t.Fatal(err)
	}
	got, err := definitionReport(home, cfg, config.Layers(home, userHome))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{noChange}; !reflect.DeepEqual(got, want) {
		t.Errorf("report %q, want %q", got, want)
	}
}

func TestWriteDefinitionHidesEnvValuesWithMode0600(t *testing.T) {
	home := t.TempDir()
	cfg := config.Config{SessionSettings: map[string]config.SessionValues{"worker": {"env": map[string]any{"TOKEN": "secret"}}}}
	d, err := buildDefinition(cfg, []string{home})
	if err != nil {
		t.Fatal(err)
	}

	if err := writeDefinition(home, d); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(home, definitionName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || strings.Contains(string(b), "secret") || !strings.Contains(string(b), hiddenValue) {
		t.Errorf("mode %v, file:\n%s", info.Mode().Perm(), b)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the folder holds %v, want only %s", entries, definitionName)
	}
}
