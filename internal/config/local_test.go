package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/prompts"
)

// The local layer is the folder local/ in the factory folder. It is the nearest layer, so it wins over every folder override.

func TestLayersListTheLocalLayerLastAfterTheOverrides(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "acme")
	workOverride := mkdir(t, work, ".sfy")
	state := walkFactory(t, home, work)
	local := mkdir(t, state, config.LocalLayer)

	got, err := config.Layers(state, home)

	if want := []string{state, filepath.Join(home, ".sfy"), workOverride, local}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("layers %v, %v, want %v", got, err, want)
	}
}

func TestLayersLeaveOutAMissingLocalLayer(t *testing.T) {
	home := userHome(t)
	state := walkFactory(t, home, mkdir(t, home, "acme"))

	got, err := config.Layers(state, home)

	if want := []string{state, filepath.Join(home, ".sfy")}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("layers %v, %v, want %v", got, err, want)
	}
}

func TestLayersSkipALocalLayerThatOthersCanWriteAndSkippedReportsIt(t *testing.T) {
	home := userHome(t)
	state := walkFactory(t, home, mkdir(t, home, "acme"))
	local := mkdir(t, state, config.LocalLayer)
	if err := os.Chmod(local, 0o777); err != nil {
		t.Fatal(err)
	}

	layers, err := config.Layers(state, home)
	if err != nil {
		t.Fatal(err)
	}
	skipped, err := config.Skipped(state, home)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{state, filepath.Join(home, ".sfy")}; !reflect.DeepEqual(layers, want) {
		t.Errorf("layers %v, want %v", layers, want)
	}
	if len(skipped) != 1 || skipped[0].Dir != local || !strings.Contains(skipped[0].Skip, "0777") {
		t.Errorf("skipped %+v, want %s with its mode", skipped, local)
	}
}

func TestLoadFactoryLetsTheLocalLayerWinOverTheMachineFolder(t *testing.T) {
	home := userHome(t)
	state := walkFactory(t, home, mkdir(t, home, "acme"))
	writeFile(t, filepath.Join(home, ".sfy", "config.d", "50-machine.toml"), "multiplexer = \"rex\"\n")
	writeFile(t, filepath.Join(state, config.LocalLayer, "config.toml"), "multiplexer = \"tmux\"\n")

	cfg, err := config.LoadFactory(state, home)

	if err != nil || cfg.Multiplexer != "tmux" {
		t.Fatalf("multiplexer %q, %v, want tmux", cfg.Multiplexer, err)
	}
}

func TestAnEmptyLocalAdditionRemovesTheFactoryFoldersAdditionOfItsName(t *testing.T) {
	home := userHome(t)
	state := walkFactory(t, home, mkdir(t, home, "acme"))
	writeFile(t, filepath.Join(state, "prompts", "lead.d", "notes.md"), "Keep notes.md.\n")
	writeFile(t, filepath.Join(state, config.LocalLayer, "prompts", "lead.d", "notes.md"), "")
	layers, err := config.Layers(state, home)
	if err != nil {
		t.Fatal(err)
	}

	got, err := prompts.For("lead", layers)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(got, "Keep notes.md.") {
		t.Error("the lead prompt holds the addition that the local layer removes")
	}
}
