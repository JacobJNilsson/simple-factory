package config_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// walkFactory makes a factory folder that records the working folder work, and returns it.
func walkFactory(t *testing.T, home, work string) string {
	t.Helper()
	state := mkdir(t, home, ".sfy", "factories", "acme")
	writeFile(t, filepath.Join(state, "folder"), work+"\n")
	return state
}

func dirsOf(overrides []config.Override) []string {
	var out []string
	for _, o := range overrides {
		out = append(out, o.Dir)
	}
	return out
}

func TestOverridesOfAWorkingFolderAndItsParentGoFarthestFirst(t *testing.T) {
	home := userHome(t)
	parent := mkdir(t, home, "Developer")
	work := mkdir(t, parent, "acme")
	mkdir(t, home, ".sfy")
	mkdir(t, parent, ".sfy")
	mkdir(t, work, ".sfy")
	state := walkFactory(t, home, work)

	got, err := config.Overrides(state, home)

	want := []string{filepath.Join(home, ".sfy"), filepath.Join(parent, ".sfy"), filepath.Join(work, ".sfy")}
	if err != nil || !reflect.DeepEqual(dirsOf(got), want) {
		t.Fatalf("overrides %v, %v, want %v", dirsOf(got), err, want)
	}
	for _, o := range got {
		if o.Skip != "" {
			t.Errorf("%s is skipped: %s", o.Dir, o.Skip)
		}
	}
}

func TestOverridesWithNoOverrideOnTheWalkGiveTheMachineFolderOnly(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "Developer", "acme")
	mkdir(t, home, ".sfy")
	state := walkFactory(t, home, work)

	got, err := config.Overrides(state, home)

	if want := []string{filepath.Join(home, ".sfy")}; err != nil || !reflect.DeepEqual(dirsOf(got), want) {
		t.Fatalf("overrides %v, %v, want %v", dirsOf(got), err, want)
	}
}

func TestOverridesOfAWorkingFolderOutsideTheHomeFolderTakeNoParent(t *testing.T) {
	home := userHome(t)
	outside := userHome(t)
	work := mkdir(t, outside, "acme")
	mkdir(t, home, ".sfy")
	mkdir(t, outside, ".sfy")
	mkdir(t, work, ".sfy")
	state := walkFactory(t, home, work)

	got, err := config.Overrides(state, home)

	want := []string{filepath.Join(home, ".sfy"), filepath.Join(work, ".sfy")}
	if err != nil || !reflect.DeepEqual(dirsOf(got), want) {
		t.Fatalf("overrides %v, %v, want %v", dirsOf(got), err, want)
	}
}

func TestOverridesOfTheDefaultFactoryAndOfAFolderWithoutAFolderFileGiveTheMachineFolderOnly(t *testing.T) {
	home := userHome(t)
	machine := mkdir(t, home, ".sfy")
	bare := mkdir(t, machine, "factories", "bare")
	mkdir(t, home, "Developer", ".sfy")

	for _, factory := range []string{machine, bare} {
		got, err := config.Overrides(factory, home)
		if want := []string{machine}; err != nil || !reflect.DeepEqual(dirsOf(got), want) {
			t.Errorf("%s: overrides %v, %v, want %v", factory, dirsOf(got), err, want)
		}
	}
}

func TestOverridesSkipAnOverrideThatOthersCanWrite(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "acme")
	open := mkdir(t, work, ".sfy")
	if err := os.Chmod(open, 0o770); err != nil {
		t.Fatal(err)
	}
	state := walkFactory(t, home, work)

	got, err := config.Overrides(state, home)

	if err != nil || len(got) != 2 || got[0].Skip != "" || got[1].Dir != open || !strings.Contains(got[1].Skip, "0770") {
		t.Fatalf("overrides %+v, %v, want the machine folder read and %s skipped with its mode", got, err, open)
	}
}

func TestOverridesSkipAnOverrideOfAnotherUser(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "acme")
	own := mkdir(t, work, ".sfy")
	state := walkFactory(t, home, work)

	got, err := config.OverridesAs(state, home, os.Geteuid()+1)

	if err != nil || len(got) != 2 || got[1].Dir != own || !strings.Contains(got[1].Skip, "owned by uid") {
		t.Fatalf("overrides %+v, %v, want %s skipped for its owner", got, err, own)
	}
}

func TestOverridesFailOnAFolderThatCannotBeRead(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "acme")
	state := walkFactory(t, home, work)
	// A path with a file in the middle gives an error other than "not exist".
	writeFile(t, filepath.Join(work, "x"), "")
	if err := os.Symlink(filepath.Join(work, "x", "y"), filepath.Join(work, ".sfy")); err != nil {
		t.Fatal(err)
	}

	_, err := config.Overrides(state, home)

	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || !strings.HasSuffix(pathErr.Path, config.OverrideDir) {
		t.Fatalf("got %v, want a path error for the .sfy folder", err)
	}
}

func loadWalk(t *testing.T, factoryBody, machineBody, workBody string) (config.Config, string, string, error) {
	t.Helper()
	home := userHome(t)
	work := mkdir(t, home, "Developer", "acme")
	state := walkFactory(t, home, work)
	writeFile(t, filepath.Join(state, "config.toml"), factoryBody)
	writeFile(t, filepath.Join(home, ".sfy", "config.toml"), machineBody)
	writeFile(t, filepath.Join(work, ".sfy", "config.toml"), workBody)
	cfg, err := config.LoadFactory(state, home)
	return cfg, state, home, err
}

func TestLoadFactoryTakesTheNearestOverrideKeyByKey(t *testing.T) {
	cfg, _, _, err := loadWalk(t, "max_workers = 4\nreview_rounds = 6\n", "max_workers = 3\n", "max_workers = 5\n")

	if err != nil || cfg.MaxWorkers != 5 || cfg.ReviewRounds != 6 {
		t.Fatalf("max_workers %d review_rounds %d, %v, want 5 and 6", cfg.MaxWorkers, cfg.ReviewRounds, err)
	}
}

func TestLoadFactoryPutsTheMachineFolderOverTheFactoryFolder(t *testing.T) {
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "acme")
	writeFile(t, filepath.Join(state, "config.toml"), "max_workers = 4\n")
	writeFile(t, filepath.Join(home, ".sfy", "config.toml"), "max_workers = 3\n")

	cfg, err := config.LoadFactory(state, home)

	if err != nil || cfg.MaxWorkers != 3 {
		t.Fatalf("max_workers %d, %v, want 3", cfg.MaxWorkers, err)
	}
}

func TestLoadFactoryFailsOnAnUnknownKeyOfAnOverrideAndNamesTheFile(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "acme")
	state := walkFactory(t, home, work)
	writeFile(t, filepath.Join(work, ".sfy", "config.toml"), "max_workerz = 2\n")

	_, err := config.LoadFactory(state, home)

	var bad *config.SettingError
	if !errors.As(err, &bad) || bad.Setting != "max_workerz" {
		t.Fatalf("got %v, want a SettingError for max_workerz", err)
	}
}

func TestLoadFactoryLeavesOutTheKeysOfASkippedOverride(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "acme")
	state := walkFactory(t, home, work)
	override := mkdir(t, work, ".sfy")
	writeFile(t, filepath.Join(override, "config.toml"), "max_workers = 9\n")
	if err := os.Chmod(override, 0o777); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFactory(state, home)

	if err != nil || cfg.MaxWorkers != 2 {
		t.Fatalf("max_workers %d, %v, want the default 2", cfg.MaxWorkers, err)
	}
	skipped, err := config.Skipped(state, home)
	if err != nil || len(skipped) != 1 || skipped[0].Dir != override {
		t.Fatalf("skipped %+v, %v, want %s", skipped, err, override)
	}
}

func TestSourcesNamesTheOverrideFileThatSetsAKey(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "acme")
	state := walkFactory(t, home, work)
	file := filepath.Join(work, ".sfy", "config.toml")
	writeFile(t, file, "max_workers = 5\n")

	got, err := config.Sources(state, home)

	if err != nil || got["max_workers"] != file {
		t.Fatalf("sources %v, %v, want max_workers from %s", got, err, file)
	}
}

func TestSkippedLeavesOutTheFactoryFolderOfTheDefaultFactory(t *testing.T) {
	home := userHome(t)
	machine := mkdir(t, home, ".sfy")
	if err := os.Chmod(machine, 0o777); err != nil {
		t.Fatal(err)
	}

	skipped, err := config.Skipped(machine, home)
	layers, lerr := config.Layers(machine, home)

	if err != nil || lerr != nil || len(skipped) != 0 || !reflect.DeepEqual(layers, []string{machine}) {
		t.Fatalf("skipped %v, %v, layers %v, %v, want none and the folder", skipped, err, layers, lerr)
	}
}
