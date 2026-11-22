package up

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// configFactory returns the env of a factory acme below a fresh user home, and the factory folder.
func configFactory(t *testing.T) (env func(string) string, userHome, home string) {
	t.Helper()
	userHome = t.TempDir()
	home = filepath.Join(config.FactoriesDir(userHome), "acme")
	writeTestFile(t, filepath.Join(home, "folder"), "/work/acme\n")
	env = func(k string) string {
		return map[string]string{"HOME": userHome, "SFY_HOME": home}[k]
	}
	return env, userHome, home
}

func showConfig(t *testing.T, env func(string) string) string {
	t.Helper()
	var out strings.Builder
	if err := ShowConfig(env, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestShowConfigNamesTheSourceOfEachKeyAndListsTheFactoryFiles(t *testing.T) {
	env, userHome, home := configFactory(t)
	machine := filepath.Join(config.DefaultHome(userHome), "config.toml")
	factory := filepath.Join(home, "config.toml")
	writeTestFile(t, machine, "review_rounds = 4\nmax_workers = 3\n")
	writeTestFile(t, factory, "max_workers = 1\n[session_settings.worker.env]\nTOKEN = \"secret\"\n")
	writeTestFile(t, filepath.Join(home, "checks", "lint.sh"), "true\n")
	writeTestFile(t, filepath.Join(home, "notes.md"), "notes\n")
	writeTestFile(t, filepath.Join(home, "status.md"), "status\n")

	got := showConfig(t, env)

	for _, want := range []string{
		"review_rounds = 4 # " + machine + "\n",
		"max_workers = 1 # " + factory + "\n",
		"resend_minutes = 5 # default\n",
		`session_settings.worker.env.TOKEN = "<set>" # ` + factory + "\n",
		"\n# Factory files\n# checks\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"secret", "# notes.md", "# status.md", "# folder"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("output holds %q:\n%s", unwanted, got)
		}
	}
}

func TestShowConfigListsTheSkillsAndNotTheSkillsFolderAsAFactoryFile(t *testing.T) {
	env, _, home := configFactory(t)
	writeTestFile(t, filepath.Join(home, "skills", "review", "SKILL.md"), "---\nname: review\n---\n")
	writeTestFile(t, filepath.Join(home, "skills", "draft", "notes.txt"), "no skill file\n")

	got := showConfig(t, env)

	if want := "\n# Skills\n# review\n\n# Factory files\n"; !strings.Contains(got, want) {
		t.Errorf("output lacks %q:\n%s", want, got)
	}
	if strings.Contains(got, "# draft") || strings.Contains(got, "# skills\n") {
		t.Errorf("output lists a folder without a skill file or the skills folder:\n%s", got)
	}
}

func TestShowConfigNamesTheConfigDFileBehindAKey(t *testing.T) {
	env, _, home := configFactory(t)
	part := filepath.Join(home, "config.d", "50-shared.toml")
	writeTestFile(t, part, "max_workers = 4\n")

	got := showConfig(t, env)

	if !strings.Contains(got, "max_workers = 4 # "+part+"\n") || strings.Contains(got, "# config.d\n") {
		t.Errorf("output does not name the config.d file, or lists config.d as a factory file:\n%s", got)
	}
}

func TestShowConfigPrintsValidTOML(t *testing.T) {
	env, _, home := configFactory(t)
	writeTestFile(t, filepath.Join(home, "config.toml"), "[session_settings.worker]\nlist = [1.5, \"a\\\"b\"]\nnested = { x = [{ y = 2 }] }\n")

	var decoded map[string]any
	if _, err := toml.Decode(showConfig(t, env), &decoded); err != nil {
		t.Fatalf("the output is not TOML: %v", err)
	}
}

func TestShowConfigListsAndWarnsOfEachOldName(t *testing.T) {
	env, _, home := configFactory(t)
	factory := filepath.Join(home, "config.toml")
	writeTestFile(t, factory, "[models]\nmain = \"m\"\n")
	writeTestFile(t, filepath.Join(home, "prompts", "main.d", "a.md"), "old a")
	var out, warn strings.Builder

	if err := ShowConfig(env, &out, &warn); err != nil {
		t.Fatal(err)
	}

	key := factory + " sets models.main. sfy reads it as models.lead. Rename the key."
	folder := filepath.Join(home, "prompts", "main.d") + " has the old name. sfy reads it as lead.d. Rename the folder."
	if want := "\n# Old names\n# " + key + "\n# " + folder + "\n"; !strings.HasSuffix(out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, out.String())
	}
	if !strings.Contains(out.String(), `models.lead = "m" # `+factory+"\n") {
		t.Errorf("output lacks models.lead:\n%s", out.String())
	}
	if want := "sfy: warning: " + key + "\nsfy: warning: " + folder + "\n"; warn.String() != want {
		t.Errorf("warnings %q, want %q", warn.String(), want)
	}
}

func TestShowConfigListsThePromptFilesOfEachKindInAppendOrder(t *testing.T) {
	env, userHome, home := configFactory(t)
	machine := config.DefaultHome(userHome)
	writeTestFile(t, filepath.Join(machine, "prompts", "worker.d", "a.md"), "machine a")
	writeTestFile(t, filepath.Join(machine, "prompts", "worker.d", "b.md"), "machine b")
	writeTestFile(t, filepath.Join(home, "prompts", "worker.d", "b.md"), "")
	writeTestFile(t, filepath.Join(home, "prompts", "worker.md"), "base")

	got := showConfig(t, env)

	want := "# worker: base " + filepath.Join(home, "prompts", "worker.md") + "\n" +
		"#   a.md: " + filepath.Join(machine, "prompts", "worker.d", "a.md") + "\n" +
		"#   b.md: removed by " + filepath.Join(home, "prompts", "worker.d", "b.md") + "\n"
	if !strings.Contains(got, want) || !strings.Contains(got, "# lead: base default\n") {
		t.Errorf("output lacks the prompt files:\n%s", got)
	}
}

func TestShowConfigFailsOnAFactoryConfigThatDoesNotParse(t *testing.T) {
	env, _, home := configFactory(t)
	writeTestFile(t, filepath.Join(home, "config.toml"), "max_workers = \n")

	err := ShowConfig(env, &strings.Builder{}, io.Discard)

	var bad *config.FileError
	if !errors.As(err, &bad) || bad.Path != filepath.Join(home, "config.toml") {
		t.Fatalf("got %v, want a FileError for the factory file", err)
	}
}
