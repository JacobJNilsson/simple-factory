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

	got, err := config.ForRepo(config.Config{}, home, "", filepath.Join(home, "work", "a", "repo"))

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

	near, err1 := config.ForRepo(config.Config{}, home, "", repo)
	far, err2 := config.ForRepo(config.Config{}, home, "", other)

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

	got, err := config.ForRepo(global, home, "", filepath.Join(home, "work", "repo"))

	if want := (config.Config{NoMerge: true, MaxWorkers: 7}); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v, err %v", got, want, err)
	}
}

func TestARepoFileOverridesTheGlobalDefault(t *testing.T) {
	home := t.TempDir()
	writeRepoFile(t, filepath.Join(home, "repo"), "no_merge = false\n")

	got, err := config.ForRepo(config.Config{NoMerge: true}, home, "", filepath.Join(home, "repo"))

	if err != nil || got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v", got.NoMerge, err)
	}
}

func TestNoFileGivesTheGlobalConfig(t *testing.T) {
	home := t.TempDir()

	got, err := config.ForRepo(config.Config{NoMerge: true}, home, "", filepath.Join(home, "repo"))

	if err != nil || !got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v", got.NoMerge, err)
	}
}

func TestTheLookupStopsAtTheHomeFolder(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeRepoFile(t, root, "no_merge = true\n")

	got, err := config.ForRepo(config.Config{}, home, "", filepath.Join(home, "repo"))

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

	got, err := config.ForRepo(config.Config{}, home, "", outside)

	if err != nil || got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v", got.NoMerge, err)
	}
}

func TestARepoFileRefusesAnUnknownKeyAndNamesTheFile(t *testing.T) {
	home := t.TempDir()
	writeRepoFile(t, filepath.Join(home, "work"), "no_mrge = true\n")
	file := filepath.Join(home, "work", config.RepoFile)

	_, err := config.ForRepo(config.Config{}, home, "", filepath.Join(home, "work", "repo"))

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
		{"check_timeout = \"5m\"", "check_timeout"},
		{"queue_wait_note = \"5m\"", "queue_wait_note"},
		{"[multiplexers.x]\nstart = \"y\"", "multiplexers.x"},
	} {
		home := t.TempDir()
		writeRepoFile(t, home, c.body+"\n")

		_, err := config.ForRepo(config.Config{}, home, "", home)

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

func writeSfyFolder(t *testing.T, repo, body string) {
	t.Helper()
	dir := filepath.Join(repo, config.OverrideDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.RepoConfigFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTheSfyFolderOfALocalRepoSetsTheSettings(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	writeSfyFolder(t, repo, "no_merge = true\ndraft_pr = true\n")

	got, err := config.ForRepo(config.Config{}, home, "", repo)

	if want := (config.Config{NoMerge: true, DraftPR: true}); err != nil || got.NoMerge != want.NoMerge || got.DraftPR != want.DraftPR {
		t.Fatalf("got %+v, err = %v", got, err)
	}
}

func TestTheSfyFolderOfAURLCloneSetsTheSettings(t *testing.T) {
	home := t.TempDir()
	clone := filepath.Join(home, ".sfy", "factories", "x", "repos", "some-repo-1234")
	writeSfyFolder(t, clone, "draft_pr = true\n")

	got, err := config.ForRepo(config.Config{}, home, "", clone)

	if err != nil || !got.DraftPR {
		t.Fatalf("DraftPR = %v, err = %v", got.DraftPR, err)
	}
}

func TestThePrecedenceRunsFromTheSfyFolderToTheGlobalConfigKeyByKey(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "work", "repo")
	writeRepoFile(t, filepath.Join(home, "work"), "no_merge = true\ndraft_pr = true\n")
	writeRepoFile(t, repo, "no_merge = false\n")
	writeSfyFolder(t, repo, "no_merge = true\n")

	got, err := config.ForRepo(config.Config{MaxWorkers: 4}, home, "", repo)

	// no_merge comes from .sfy/config.toml, draft_pr from the parent .sfy.toml, max_workers from the base config.
	if err != nil || !got.NoMerge || !got.DraftPR || got.MaxWorkers != 4 {
		t.Fatalf("got %+v, err = %v", got, err)
	}
}

func TestTheSfyFolderOverridesASfyTomlThatSaysOtherwise(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	writeRepoFile(t, repo, "no_merge = true\n")
	writeSfyFolder(t, repo, "no_merge = false\n")

	got, err := config.ForRepo(config.Config{NoMerge: true}, home, "", repo)

	if err != nil || got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v", got.NoMerge, err)
	}
}

func TestASfyFolderInAWorktreeOrAParentFolderIsIgnored(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	writeSfyFolder(t, filepath.Join(home, "work", "t1"), "no_merge = true\n")
	writeSfyFolder(t, home, "no_merge = true\n")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := config.ForRepo(config.Config{}, home, "", repo)

	if err != nil || got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v", got.NoMerge, err)
	}
}

func TestTheSfyFolderRejectsUnknownKeysGlobalKeysAndBrokenTOML(t *testing.T) {
	for _, body := range []string{"nope = 1\n", "max_workers = 3\n", "no_merge = \n"} {
		home := t.TempDir()
		repo := filepath.Join(home, "repo")
		writeSfyFolder(t, repo, body)

		if _, err := config.ForRepo(config.Config{}, home, "", repo); err == nil {
			t.Errorf("ForRepo accepted %q", body)
		}
	}
}

func TestARepoFileWithBrokenTOMLIsAnError(t *testing.T) {
	home := t.TempDir()
	writeRepoFile(t, home, "no_merge = \n")

	if _, err := config.ForRepo(config.Config{}, home, "", home); err == nil {
		t.Fatal("ForRepo accepted a broken file")
	}
}
