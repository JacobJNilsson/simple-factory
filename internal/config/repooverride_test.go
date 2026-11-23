package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

func TestForRepoReadsTheOverrideOfAFolderBetweenTheRepoAndTheWalk(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "Developer", "acme")
	between := mkdir(t, work, "group")
	repo := mkdir(t, between, "web")
	writeFile(t, filepath.Join(between, config.OverrideDir, "config.toml"), "no_merge = true\n")

	got, err := config.ForRepo(config.Config{}, home, work, repo)

	if err != nil || !got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v, want true", got.NoMerge, err)
	}
}

func TestForRepoDoesNotReadTheOverrideOfTheWorkingFolderAsAPerRepoFile(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "Developer", "acme")
	repo := mkdir(t, work, "web")
	// The override of the working folder is a layer, so it may hold a global key. The repo reads none of it.
	writeFile(t, filepath.Join(work, config.OverrideDir, "config.toml"), "max_workers = 3\nno_merge = true\n")

	got, err := config.ForRepo(config.Config{}, home, work, repo)

	if err != nil || got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v, want the override left to the layers", got.NoMerge, err)
	}
}

func TestForRepoStopsBeforeTheFirstFolderOfTheWalk(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "Developer", "acme")
	other := mkdir(t, home, "Developer", "other", "web")
	writeFile(t, filepath.Join(home, "Developer", config.OverrideDir, "config.toml"), "max_workers = 3\n")
	writeFile(t, filepath.Join(home, "Developer", "other", config.OverrideDir, "config.toml"), "no_merge = true\n")

	got, err := config.ForRepo(config.Config{}, home, work, other)

	if err != nil || !got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v, want the folder below the walk read and the walk folder left out", got.NoMerge, err)
	}
}

func TestForRepoFailsOnAGlobalKeyOfAPerRepoOverride(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "Developer", "acme")
	repo := mkdir(t, home, "elsewhere", "web")
	file := filepath.Join(repo, config.OverrideDir, "config.toml")
	writeFile(t, file, "max_workers = 3\n")

	_, err := config.ForRepo(config.Config{}, home, work, repo)

	var bad *config.RepoFileError
	if !errors.As(err, &bad) || !bad.GlobalOnly || bad.Setting != "max_workers" || bad.File != file {
		t.Fatalf("got %v, want a RepoFileError for max_workers in %s", err, file)
	}
}

func TestForRepoStillReadsAnOldSfyToml(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "Developer", "acme")
	repo := mkdir(t, work, "web")
	writeRepoFile(t, work, "no_merge = true\n")

	got, err := config.ForRepo(config.Config{}, home, work, repo)

	if err != nil || !got.NoMerge {
		t.Fatalf("NoMerge = %v, err = %v, want true", got.NoMerge, err)
	}
}

func TestForRepoFailsOnAPerRepoOverrideThatOthersCanWrite(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "Developer", "acme")
	repo := mkdir(t, home, "elsewhere", "web")
	override := filepath.Join(repo, config.OverrideDir)
	writeFile(t, filepath.Join(override, "config.toml"), "no_merge = false\n")
	if err := os.Chmod(override, 0o770); err != nil {
		t.Fatal(err)
	}

	_, err := config.ForRepo(config.Config{NoMerge: true}, home, work, repo)

	var unsafe *config.OverrideError
	if !errors.As(err, &unsafe) || unsafe.Dir != override || unsafe.Reason == "" {
		t.Fatalf("got %v, want an OverrideError for %s", err, override)
	}
}

func TestForRepoOfARepoOutsideTheHomeFolderReadsItsOwnOverrideOnly(t *testing.T) {
	home := userHome(t)
	outside := userHome(t)
	work := mkdir(t, home, "Developer", "acme")
	repo := mkdir(t, outside, "group", "web")
	writeFile(t, filepath.Join(outside, "group", config.OverrideDir, "config.toml"), "no_merge = true\n")
	writeFile(t, filepath.Join(repo, config.OverrideDir, "config.toml"), "draft_pr = true\n")

	got, err := config.ForRepo(config.Config{}, home, work, repo)

	if err != nil || got.NoMerge || !got.DraftPR {
		t.Fatalf("NoMerge = %v, DraftPR = %v, err = %v, want only the repo's own override", got.NoMerge, got.DraftPR, err)
	}
}

func TestOldNamesListsAnOldSfyTomlOnTheWalk(t *testing.T) {
	home := userHome(t)
	work := mkdir(t, home, "Developer", "acme")
	state := walkFactory(t, home, work)
	file := filepath.Join(work, config.RepoFile)
	writeRepoFile(t, work, "no_merge = true\n")

	got, err := config.OldNames(state, home)

	want := file + " has the old name. sfy still reads it for the repos below it. Move it to " + filepath.Join(work, config.OverrideDir, "config.toml") + "."
	if err != nil || len(got) != 1 || got[0] != want {
		t.Fatalf("got %q, %v, want %q", got, err, want)
	}
}
