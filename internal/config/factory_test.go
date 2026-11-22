package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// userHome returns a fresh user home folder, resolved so that it matches what the lookup sees.
func userHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFactoryNameUsesTheBaseNameInLowerCase(t *testing.T) {
	cases := map[string]string{
		"/h/acme":       "acme",
		"/h/Acme Works": "acme-works",
		"/h/team_A.b":   "team-a-b",
		"/h/.hidden":    "hidden",
		"/":             "factory",
	}
	for folder, want := range cases {
		if got := config.FactoryName(folder); got != want {
			t.Errorf("FactoryName(%q) = %q, want %q", folder, got, want)
		}
	}
}

func TestCreateFactoryRecordsTheFolderAndWritesNothingThere(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "Developer", "acme")

	state, err := config.CreateFactory(home, dir)

	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".sfy", "factories", "acme"); state != want {
		t.Errorf("state folder %q, want %q", state, want)
	}
	if got := config.FactoryFolder(state); got != dir {
		t.Errorf("recorded folder %q, want %q", got, dir)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the factory folder holds %d entries, want none", len(entries))
	}
}

func TestSecondFolderWithTheSameBaseNameGetsASuffix(t *testing.T) {
	home := userHome(t)
	first := mkdir(t, home, "a", "web")
	second := mkdir(t, home, "b", "web")
	third := mkdir(t, home, "c", "web")

	var names []string
	for _, dir := range []string{first, second, third} {
		state, err := config.CreateFactory(home, dir)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, filepath.Base(state))
	}

	if want := []string{"web", "web-2", "web-3"}; !equal(names, want) {
		t.Errorf("names %v, want %v", names, want)
	}
	if got := config.Prefix(filepath.Join(home, ".sfy", "factories", "web-2"), func(string) string { return home }); got != "web-2-" {
		t.Errorf("prefix %q, want web-2-", got)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFindFactoryTakesTheNearestFolderAbove(t *testing.T) {
	home := userHome(t)
	outer := mkdir(t, home, "Developer", "acme")
	inner := mkdir(t, outer, "web")
	deep := mkdir(t, inner, "src")
	outerState, _ := config.CreateFactory(home, outer)

	got, ok := config.FindFactory(home, deep)
	if !ok || got != outerState {
		t.Fatalf("below the outer folder: got %q %v, want %q", got, ok, outerState)
	}

	innerState, _ := config.CreateFactory(home, inner)
	got, ok = config.FindFactory(home, deep)
	if !ok || got != innerState {
		t.Fatalf("below the inner folder: got %q %v, want %q", got, ok, innerState)
	}
	got, ok = config.FindFactory(home, outer)
	if !ok || got != outerState {
		t.Fatalf("in the outer folder: got %q %v, want %q", got, ok, outerState)
	}
}

func TestFindFactoryIgnoresUnrelatedFoldersAndTheHomeFolder(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work")
	other := mkdir(t, home, "other")
	if _, err := config.CreateFactory(home, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := config.CreateFactory(home, home); err != nil {
		t.Fatal(err)
	}

	for _, folder := range []string{other, home, filepath.Join(other, "x")} {
		if state, ok := config.FindFactory(home, folder); ok {
			t.Errorf("FindFactory(%q) = %q, want none", folder, state)
		}
	}
}

func TestHomeOrdersSFYHOMEThenFactoryThenDefault(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work")
	state, _ := config.CreateFactory(home, dir)
	other := mkdir(t, home, "other")
	envOf := func(sfyHome string) func(string) string {
		return func(k string) string {
			switch k {
			case "HOME":
				return home
			case "SFY_HOME":
				return sfyHome
			}
			return ""
		}
	}
	cases := []struct{ name, cwd, sfyHome, want string }{
		{"override", dir, "/custom", "/custom"},
		{"in the factory folder", dir, "", state},
		{"in a subfolder", mkdir(t, dir, "sub"), "", state},
		{"in the home folder", home, "", filepath.Join(home, ".sfy")},
		{"in an unrelated folder", other, "", filepath.Join(home, ".sfy")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			old, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(c.cwd); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chdir(old) })
			if got := config.Home(envOf(c.sfyHome)); got != c.want {
				t.Errorf("Home = %q, want %q", got, c.want)
			}
		})
	}
}

func TestLoadFactoryReadsTheOperatorsConfigAndTheFactoryOverrides(t *testing.T) {
	home := userHome(t)
	state := mkdir(t, home, ".sfy", "factories", "work")
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".sfy", "config.toml"), "max_workers = 5\nreview_rounds = 4\n")

	got, err := config.LoadFactory(state, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxWorkers != 5 || got.ReviewRounds != 4 {
		t.Errorf("without an override: max_workers %d review_rounds %d, want 5 and 4", got.MaxWorkers, got.ReviewRounds)
	}

	write(filepath.Join(state, "config.toml"), "max_workers = 1\n")
	got, err = config.LoadFactory(state, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxWorkers != 1 || got.ReviewRounds != 4 {
		t.Errorf("with an override: max_workers %d review_rounds %d, want 1 and 4", got.MaxWorkers, got.ReviewRounds)
	}
}

func TestLoadFactoryOfTheDefaultFolderReadsOneFile(t *testing.T) {
	home := userHome(t)
	def := mkdir(t, home, ".sfy")
	if err := os.WriteFile(filepath.Join(def, "config.toml"), []byte("max_workers = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := config.LoadFactory(def, home)

	if err != nil || got.MaxWorkers != 3 {
		t.Errorf("got max_workers %d, err %v, want 3", got.MaxWorkers, err)
	}
}

func TestFactoryFilesOfTheDefaultFactoryLeaveOutStateDefinitionAndFactories(t *testing.T) {
	home := userHome(t)
	def := mkdir(t, home, ".sfy")
	for _, name := range []string{"tasks", "prompts", "factories", "checks"} {
		mkdir(t, def, name)
	}
	for _, name := range []string{"config.toml", "notes.md", "sfy.log", "knowledge.md"} {
		if err := os.WriteFile(filepath.Join(def, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := config.FactoryFiles(def, home)

	if err != nil || !equal(got, []string{"checks", "knowledge.md"}) {
		t.Fatalf("got %v, %v, want checks and knowledge.md", got, err)
	}
}

func TestHeaderNamesTheFactoryAndItsFolder(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work")
	state, _ := config.CreateFactory(home, dir)
	env := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}

	if got, want := config.Header(state, env), "Factory: work ("+dir+")"; got != want {
		t.Errorf("header %q, want %q", got, want)
	}
	def := filepath.Join(home, ".sfy")
	if got, want := config.Header(def, env), "Factory: default ("+def+")"; got != want {
		t.Errorf("default header %q, want %q", got, want)
	}
}
