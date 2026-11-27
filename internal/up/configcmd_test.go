package up

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/JacobJNilsson/simple-factory/internal/agentproc"
	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/identity"
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
		"max_workers = 3 # " + machine + "\n",
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

	if want := "\n# Skills\n# review: " + filepath.Join(home, "skills", "review") + "\n\n# Factory files\n"; !strings.Contains(got, want) {
		t.Errorf("output lacks %q:\n%s", want, got)
	}
	if strings.Contains(got, "# draft") || strings.Contains(got, "# skills\n") {
		t.Errorf("output lists a folder without a skill file or the skills folder:\n%s", got)
	}
}

func TestShowConfigNamesTheLayerOfASkillThatAnOverrideReplaces(t *testing.T) {
	env, userHome, home := configFactory(t)
	work := filepath.Join(userHome, "Developer", "acme")
	writeTestFile(t, filepath.Join(home, "folder"), work+"\n")
	writeTestFile(t, filepath.Join(home, "skills", "review", "SKILL.md"), "factory\n")
	writeTestFile(t, filepath.Join(home, "skills", "plain", "SKILL.md"), "factory\n")
	writeTestFile(t, filepath.Join(work, config.OverrideDir, "skills", "review", "SKILL.md"), "override\n")

	got := showConfig(t, env)

	want := "\n# Skills\n# plain: " + filepath.Join(home, "skills", "plain") + "\n# review: " + filepath.Join(work, config.OverrideDir, "skills", "review") + "\n"
	if !strings.Contains(got, want) {
		t.Errorf("output lacks %q:\n%s", want, got)
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

// The alive template answers that worker-t1 and worker-t2 run, that worker-t3 is gone, and fails for worker-t4.
const envTokenMux = `multiplexer = "probe"
[multiplexers.probe]
start = "true"
send = "true"
stop = "true"
alive = "case {name} in *worker-t1|*worker-t2) exit 0 ;; *worker-t3) exit 1 ;; *) exit 3 ;; esac"
`

func TestShowConfigListsAndWarnsOfEachRunningSessionWithATokenInTheEnvironment(t *testing.T) {
	env, _, home := configFactory(t)
	writeTestFile(t, filepath.Join(home, "config.toml"), envTokenMux)
	folder := identity.Folder{Home: home}
	for _, name := range []string{"worker-t1", "worker-t2", "worker-t3", "worker-t4"} {
		if _, err := folder.Issue(name); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(home, "sessions", "worker-t2", agentproc.ProcessFile), `{"pid":1,"start":"any"}`)
	var out, warn strings.Builder

	if err := ShowConfig(env, &out, &warn); err != nil {
		t.Fatal(err)
	}

	runs := "Session worker-t1 runs with a token file and no process record. An older sfy started it with its token in SFY_TOKEN. Restart it."
	unknown := "Session worker-t4 has a token file and no process record (sfy cannot tell whether it runs). An older sfy started it with its token in SFY_TOKEN. Restart it if it still runs."
	if want := "\n# Sessions with a token in the environment\n# " + runs + "\n# " + unknown + "\n\n# Old names\n"; !strings.Contains(out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, out.String())
	}
	if want := "sfy: warning: " + runs + "\nsfy: warning: " + unknown + "\n"; warn.String() != want {
		t.Errorf("warnings %q, want %q", warn.String(), want)
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
	writeTestFile(t, filepath.Join(home, "prompts", "worker.d", "a.md"), "factory a")
	writeTestFile(t, filepath.Join(home, "prompts", "worker.d", "b.md"), "factory b")
	writeTestFile(t, filepath.Join(machine, "prompts", "worker.d", "b.md"), "")
	writeTestFile(t, filepath.Join(home, "prompts", "worker.md"), "base")

	got := showConfig(t, env)

	want := "# worker: base " + filepath.Join(home, "prompts", "worker.md") + "\n" +
		"#   a.md: " + filepath.Join(home, "prompts", "worker.d", "a.md") + "\n" +
		"#   b.md: removed by " + filepath.Join(machine, "prompts", "worker.d", "b.md") + "\n"
	if !strings.Contains(got, want) || !strings.Contains(got, "# lead: base default\n") {
		t.Errorf("output lacks the prompt files:\n%s", got)
	}
}

func TestShowConfigListsTheLayersWithTheirRolesAndEachSkippedOverrideWithItsReason(t *testing.T) {
	env, userHome, home := configFactory(t)
	work := filepath.Join(userHome, "Developer", "acme")
	writeTestFile(t, filepath.Join(home, "folder"), work+"\n")
	good := filepath.Join(userHome, "Developer", config.OverrideDir)
	bad := filepath.Join(work, config.OverrideDir)
	writeTestFile(t, filepath.Join(good, "config.toml"), "max_workers = 5\n")
	writeTestFile(t, filepath.Join(bad, "config.toml"), "max_workers = 9\n")
	if err := os.Chmod(bad, 0o770); err != nil {
		t.Fatal(err)
	}

	got := showConfig(t, env)

	want := "# Layers, lowest first:\n# - built-in defaults\n# - " + home + " (factory folder)\n# - " + config.DefaultHome(userHome) + " (override)\n# - " + good + " (override)\n" +
		"# Skipped overrides, which sfy does not read:\n# - " + bad + ": mode 0770 lets group or others write\n"
	if !strings.Contains(got, want) || !strings.Contains(got, "max_workers = 5 # "+filepath.Join(good, "config.toml")+"\n") {
		t.Errorf("output lacks the layers or the override setting:\n%s", got)
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
