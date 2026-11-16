package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

func writeRepoFile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.RepoFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAFileInAParentFolderCoversEveryRepoBelow(t *testing.T) {
	home := t.TempDir()
	writeRepoFile(t, filepath.Join(home, "work"), "no_merge = true\n")

	got, err := config.ForRepo(config.Config{}, home, filepath.Join(home, "work", "a", "repo"))

	if err != nil || !got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v", got.NoMerge, err)
	}
}

func TestTheNearestFileWinsAndTheFarFileStillApplies(t *testing.T) {
	home := t.TempDir()
	writeRepoFile(t, filepath.Join(home, "work"), "no_merge = true\n")
	writeRepoFile(t, filepath.Join(home, "work", "open"), "no_merge = false\n")
	repo := filepath.Join(home, "work", "open", "repo")
	other := filepath.Join(home, "work", "closed")

	near, err1 := config.ForRepo(config.Config{}, home, repo)
	far, err2 := config.ForRepo(config.Config{}, home, other)

	if err := errors.Join(err1, err2); err != nil {
		t.Fatal(err)
	}
	if near.NoMerge || !far.NoMerge {
		t.Errorf("nearest file: NoMerge = %v, other repo: NoMerge = %v", near.NoMerge, far.NoMerge)
	}
}

func TestAFileLeavesAnUnsetKeyToTheFarFileAndTheGlobalConfig(t *testing.T) {
	home := t.TempDir()
	writeRepoFile(t, filepath.Join(home, "work"), "no_merge = true\n")
	writeRepoFile(t, filepath.Join(home, "work", "repo"), "\n")
	global := config.Config{NoMerge: false, MaxWorkers: 7}

	got, err := config.ForRepo(global, home, filepath.Join(home, "work", "repo"))

	if want := (config.Config{NoMerge: true, MaxWorkers: 7}); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v, err %v", got, want, err)
	}
}

func TestARepoFileOverridesTheGlobalDefault(t *testing.T) {
	home := t.TempDir()
	writeRepoFile(t, filepath.Join(home, "repo"), "no_merge = false\n")

	got, err := config.ForRepo(config.Config{NoMerge: true}, home, filepath.Join(home, "repo"))

	if err != nil || got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v", got.NoMerge, err)
	}
}

func TestNoFileGivesTheGlobalConfig(t *testing.T) {
	home := t.TempDir()

	got, err := config.ForRepo(config.Config{NoMerge: true}, home, filepath.Join(home, "repo"))

	if err != nil || !got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v", got.NoMerge, err)
	}
}

func TestTheLookupStopsAtTheHomeFolder(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeRepoFile(t, root, "no_merge = true\n")

	got, err := config.ForRepo(config.Config{}, home, filepath.Join(home, "repo"))

	if err != nil || got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v", got.NoMerge, err)
	}
}

func TestAFolderOutsideHomeReadsOnlyItsOwnFile(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeRepoFile(t, root, "no_merge = true\n")
	outside := filepath.Join(root, "elsewhere", "repo")
	writeRepoFile(t, outside, "no_merge = false\n")

	got, err := config.ForRepo(config.Config{}, home, outside)

	if err != nil || got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v", got.NoMerge, err)
	}
}

func TestARepoFileRefusesAnUnknownKeyAndNamesTheFile(t *testing.T) {
	home := t.TempDir()
	writeRepoFile(t, filepath.Join(home, "work"), "no_mrge = true\n")
	file := filepath.Join(home, "work", config.RepoFile)

	_, err := config.ForRepo(config.Config{}, home, filepath.Join(home, "work", "repo"))

	var got *config.RepoFileError
	if !errors.As(err, &got) {
		t.Fatalf("err = %v", err)
	}
	if want := (config.RepoFileError{File: file, Setting: "no_mrge"}); *got != want {
		t.Errorf("got %+v, want %+v", *got, want)
	}
}

func TestARepoFileRefusesAGlobalOnlyKey(t *testing.T) {
	for _, c := range []struct{ body, setting string }{
		{"max_workers = 3", "max_workers"},
		{"multiplexer = \"tmux\"", "multiplexer"},
		{"[multiplexers.x]\nstart = \"y\"", "multiplexers.x"},
	} {
		home := t.TempDir()
		writeRepoFile(t, home, c.body+"\n")

		_, err := config.ForRepo(config.Config{}, home, home)

		var got *config.RepoFileError
		if !errors.As(err, &got) {
			t.Errorf("%q: err = %v", c.body, err)
			continue
		}
		want := config.RepoFileError{File: filepath.Join(home, config.RepoFile), Setting: c.setting, GlobalOnly: true}
		if *got != want {
			t.Errorf("%q: got %+v, want %+v", c.body, *got, want)
		}
	}
}

func TestARepoFileWithBrokenTOMLIsAnError(t *testing.T) {
	home := t.TempDir()
	writeRepoFile(t, home, "no_merge = \n")

	if _, err := config.ForRepo(config.Config{}, home, home); err == nil {
		t.Fatal("ForRepo accepted a broken file")
	}
}
