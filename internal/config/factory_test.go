package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

	state, err := config.CreateFactory(home, dir, "")

	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".sfy", "factories", "acme"); state != want {
		t.Errorf("factory folder %q, want %q", state, want)
	}
	if got := config.WorkFolder(state); got != dir {
		t.Errorf("recorded folder %q, want %q", got, dir)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the working folder holds %d entries, want none", len(entries))
	}
}

func TestSecondFolderWithTheSameBaseNameGetsASuffix(t *testing.T) {
	home := userHome(t)
	first := mkdir(t, home, "a", "web")
	second := mkdir(t, home, "b", "web")
	third := mkdir(t, home, "c", "web")

	var names []string
	for _, dir := range []string{first, second, third} {
		state, err := config.CreateFactory(home, dir, "")
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
	outerState, _ := config.CreateFactory(home, outer, "")

	got, ok := config.FindFactory(home, deep, "")
	if !ok || got != outerState {
		t.Fatalf("below the outer folder: got %q %v, want %q", got, ok, outerState)
	}

	innerState, _ := config.CreateFactory(home, inner, "")
	got, ok = config.FindFactory(home, deep, "")
	if !ok || got != innerState {
		t.Fatalf("below the inner folder: got %q %v, want %q", got, ok, innerState)
	}
	got, ok = config.FindFactory(home, outer, "")
	if !ok || got != outerState {
		t.Fatalf("in the outer folder: got %q %v, want %q", got, ok, outerState)
	}
}

func TestFindFactoryIgnoresUnrelatedFoldersAndTheHomeFolder(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work")
	other := mkdir(t, home, "other")
	if _, err := config.CreateFactory(home, dir, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := config.CreateFactory(home, home, ""); err != nil {
		t.Fatal(err)
	}

	for _, folder := range []string{other, home, filepath.Join(other, "x")} {
		if state, ok := config.FindFactory(home, folder, ""); ok {
			t.Errorf("FindFactory(%q) = %q, want none", folder, state)
		}
	}
}

func TestHomeOrdersSFYHOMEThenFactoryThenDefault(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work")
	state, _ := config.CreateFactory(home, dir, "")
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
		{"in the working folder", dir, "", state},
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

func TestLoadFactoryReadsTheOperatorsConfigOverTheFactoryFolder(t *testing.T) {
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

	write(filepath.Join(state, "config.toml"), "max_workers = 1\nresend_minutes = 7\n")
	got, err = config.LoadFactory(state, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxWorkers != 5 || got.ReviewRounds != 4 || got.ResendMinutes != 7 {
		t.Errorf("with a factory folder file: max_workers %d review_rounds %d resend_minutes %d, want 5, 4 and 7", got.MaxWorkers, got.ReviewRounds, got.ResendMinutes)
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
	for _, name := range []string{"tasks", "prompts", "local", "factories", "checks"} {
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

func TestSkillsListsTheFoldersWithASkillFileAndFollowsLinks(t *testing.T) {
	home := t.TempDir()
	if got, err := config.Skills([]string{home}); err != nil || got != nil {
		t.Fatalf("no skills folder: got %v, %v", got, err)
	}
	shared := mkdir(t, t.TempDir(), "shared")
	if err := os.WriteFile(filepath.Join(shared, "SKILL.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	skills := mkdir(t, home, "skills")
	mkdir(t, skills, "empty")
	if err := os.Symlink(shared, filepath.Join(skills, "linked")); err != nil {
		t.Fatal(err)
	}

	got, err := config.Skills([]string{home})

	if want := []config.Skill{{Name: "linked", Dir: filepath.Join(skills, "linked")}}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, %v, want %v", got, err, want)
	}
}

func TestSkillsTakeTheNearestLayerForAName(t *testing.T) {
	far, near := t.TempDir(), t.TempDir()
	for _, dir := range []string{
		filepath.Join(far, "skills", "review"), filepath.Join(far, "skills", "draft"), filepath.Join(near, "skills", "review"),
	} {
		writeFile(t, filepath.Join(dir, "SKILL.md"), "x\n")
	}

	got, err := config.Skills([]string{far, near})

	want := []config.Skill{
		{Name: "draft", Dir: filepath.Join(far, "skills", "draft")},
		{Name: "review", Dir: filepath.Join(near, "skills", "review")},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, %v, want %v", got, err, want)
	}
}

func TestHeaderNamesTheFactoryAndItsFolder(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work")
	state, _ := config.CreateFactory(home, dir, "")
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

func TestHeaderShowsTheLabelBesideTheName(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work")
	state, _ := config.CreateFactory(home, dir, "")
	if err := os.WriteFile(filepath.Join(state, "label"), []byte("Client work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string { return map[string]string{"HOME": home}[k] }

	if got, want := config.Header(state, env), "Factory: work, shown as Client work ("+dir+")"; got != want {
		t.Errorf("header %q, want %q", got, want)
	}
}

func TestLabelOfReadsTheTrimmedLabelFile(t *testing.T) {
	cases := []struct {
		name    string
		content *string
		want    string
	}{
		{"missing file", nil, ""},
		{"empty file", ptr(""), ""},
		{"set file", ptr("  Client work\n"), "Client work"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.content != nil {
				if err := os.WriteFile(filepath.Join(home, "label"), []byte(*tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if got := config.LabelOf(home); got != tc.want {
				t.Errorf("LabelOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestFactoryHomeFindsAFactoryByTheNameThatDescribeGives(t *testing.T) {
	home := userHome(t)
	web := mkdir(t, config.FactoriesDir(home), "web")

	if got, ok := config.FactoryHome(home, "web"); !ok || got != web {
		t.Errorf("FactoryHome(web) = %q, %v, want %q", got, ok, web)
	}
	if got, ok := config.FactoryHome(home, "default"); !ok || got != config.DefaultHome(home) {
		t.Errorf("FactoryHome(default) = %q, %v", got, ok)
	}
	for _, name := range []string{"api", "..", "../web", "web/x", "Web", ""} {
		if got, ok := config.FactoryHome(home, name); ok {
			t.Errorf("FactoryHome(%q) = %q, want no factory", name, got)
		}
	}
}

func TestSessionFoldersNamesTheDefaultFactoryAndEveryCreatedFactory(t *testing.T) {
	home := userHome(t)
	mkdir(t, config.FactoriesDir(home), "web")
	mkdir(t, config.FactoriesDir(home), "api")

	got := config.SessionFolders(home)

	want := []string{
		filepath.Join(home, ".sfy", "sessions"),
		filepath.Join(config.FactoriesDir(home), "api", "sessions"),
		filepath.Join(config.FactoriesDir(home), "web", "sessions"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("SessionFolders = %v, want %v", got, want)
	}
}

// twoFactoriesAt makes two factory folders that record the working folder dir, and returns them in name order.
func twoFactoriesAt(t *testing.T, home, dir string) []string {
	t.Helper()
	states := []string{filepath.Join(config.FactoriesDir(home), "ex-web"), filepath.Join(config.FactoriesDir(home), "web")}
	for _, state := range states {
		if err := os.MkdirAll(state, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, "folder"), []byte(dir+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return states
}

func TestFactoriesAtListsEveryFactoryFolderOfTheNearestWorkingFolder(t *testing.T) {
	home := userHome(t)
	dir := mkdir(t, home, "work", "web")
	want := twoFactoriesAt(t, home, dir)

	got := config.FactoriesAt(home, mkdir(t, dir, "src"))

	if !slices.Equal(got, want) {
		t.Fatalf("FactoriesAt = %v, want %v", got, want)
	}
	if first, ok := config.FindFactory(home, dir, ""); !ok || first != want[0] {
		t.Errorf("FindFactory = %q %v, want the first, %q", first, ok, want[0])
	}
	if other := config.FactoriesAt(home, mkdir(t, home, "other")); other != nil {
		t.Errorf("FactoriesAt in an unrelated folder = %v, want none", other)
	}
}
