package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// writeFile writes body to path and creates the folders above it.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFactoryReadsConfigDFilesInNameOrderBeforeConfigToml(t *testing.T) {
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "acme")
	writeFile(t, filepath.Join(state, "config.d", "20-b.toml"), "max_workers = 5\nallow = [\"b\"]\n")
	writeFile(t, filepath.Join(state, "config.d", "10-a.toml"), "max_workers = 4\nreview_rounds = 7\nallow = [\"a\", \"x\"]\n")
	writeFile(t, filepath.Join(state, "config.d", "notes.md"), "not toml\n")
	writeFile(t, filepath.Join(state, "config.toml"), "review_rounds = 9\n")

	got, err := config.LoadFactory(state, home)

	if err != nil {
		t.Fatal(err)
	}
	if got.MaxWorkers != 5 || got.ReviewRounds != 9 || !reflect.DeepEqual(got.Allow, []string{"b"}) {
		t.Fatalf("max_workers %d, review_rounds %d, allow %v, want 5, 9 and [b]", got.MaxWorkers, got.ReviewRounds, got.Allow)
	}
}

func TestLoadFactoryPutsAFactoryConfigDFileOverTheMachineConfigToml(t *testing.T) {
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "acme")
	writeFile(t, filepath.Join(home, ".sfy", "config.toml"), "[models]\nmain = \"m\"\nworker = \"w\"\n")
	writeFile(t, filepath.Join(state, "config.d", "50-shared.toml"), "[models]\nworker = \"shared\"\n")

	got, err := config.LoadFactory(state, home)

	if err != nil {
		t.Fatal(err)
	}
	if want := (config.Models{Main: "m", Worker: "shared", Reviewer: "sonnet"}); got.Models != want {
		t.Fatalf("models %+v, want %+v", got.Models, want)
	}
}

func TestLoadFactoryFollowsALinkInConfigD(t *testing.T) {
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "acme")
	shared := filepath.Join(home, "shared", "base.toml")
	writeFile(t, shared, "max_workers = 6\n")
	mkdir(t, state, "config.d")
	if err := os.Symlink(shared, filepath.Join(state, "config.d", "50-shared.toml")); err != nil {
		t.Fatal(err)
	}

	got, err := config.LoadFactory(state, home)

	if err != nil {
		t.Fatal(err)
	}
	if got.MaxWorkers != 6 {
		t.Fatalf("max_workers %d, want 6", got.MaxWorkers)
	}
}

func TestLoadFactoryNamesAConfigDFileThatDoesNotParse(t *testing.T) {
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "acme")
	bad := filepath.Join(state, "config.d", "10-bad.toml")
	writeFile(t, bad, "max_workers = \n")

	_, err := config.LoadFactory(state, home)

	var fileErr *config.FileError
	if !errors.As(err, &fileErr) || fileErr.Path != bad {
		t.Fatalf("got %v, want a FileError that names %s", err, bad)
	}
}

func TestLoadFactoryNamesABrokenLinkInConfigD(t *testing.T) {
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "acme")
	link := filepath.Join(mkdir(t, state, "config.d"), "50-gone.toml")
	if err := os.Symlink(filepath.Join(home, "gone.toml"), link); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadFactory(state, home)

	var fileErr *config.FileError
	if !errors.As(err, &fileErr) || fileErr.Path != link {
		t.Fatalf("got %v, want a FileError that names %s", err, link)
	}
}

func TestSourcesNamesTheConfigDFileBehindAKey(t *testing.T) {
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "acme")
	part := filepath.Join(state, "config.d", "50-shared.toml")
	writeFile(t, part, "max_workers = 3\nreview_rounds = 4\n")
	writeFile(t, filepath.Join(state, "config.toml"), "max_workers = 1\n")

	got, err := config.Sources(state, home)

	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"max_workers": filepath.Join(state, "config.toml"), "review_rounds": part}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sources %v, want %v", got, want)
	}
}

func TestFactoryFilesLeaveOutConfigD(t *testing.T) {
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "acme")
	mkdir(t, state, "config.d")
	mkdir(t, state, "checks")

	got, err := config.FactoryFiles(state, home)

	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(got, "config.d") || !slices.Contains(got, "checks") {
		t.Fatalf("factory files %v, want checks without config.d", got)
	}
}
