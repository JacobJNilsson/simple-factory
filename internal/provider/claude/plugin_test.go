package claude_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/envrun/envruntest"
)

// withSkills gives the factory folder home a skills folder with one skill.
func withSkills(t *testing.T, home string) string {
	t.Helper()
	skills := filepath.Join(home, "skills")
	if err := os.MkdirAll(filepath.Join(skills, "review"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "review", "SKILL.md"), []byte("---\nname: review\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return skills
}

func pluginDir(home, name string) string {
	return filepath.Join(home, "sessions", name, "plugin")
}

func TestEachSessionKindLoadsTheFactorySkillsAsAPlugin(t *testing.T) {
	for _, spec := range []engine.SessionSpec{worker(), reviewerSpec(), mainSpec()} {
		t.Run(spec.Kind, func(t *testing.T) {
			p, home := setup(t, bin)
			withSkills(t, home)

			cmd := envruntest.Parse(t, command(t, p, spec)).Cmd

			if got := flagValue(t, cmd, "--plugin-dir"); got != pluginDir(home, spec.Name) {
				t.Errorf("--plugin-dir %q, want %q", got, pluginDir(home, spec.Name))
			}
		})
	}
}

func TestThePluginHoldsAManifestAndALinkToTheSkills(t *testing.T) {
	p, home := setup(t, bin)
	skills := withSkills(t, home)

	command(t, p, worker())

	b, err := os.ReadFile(filepath.Join(pluginDir(home, "worker-t1"), ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &m); err != nil || m.Name != "factory" {
		t.Errorf("manifest %s: %v", b, err)
	}
	if got, err := os.Readlink(filepath.Join(pluginDir(home, "worker-t1"), "skills")); err != nil || got != skills {
		t.Errorf("link %q, %v, want %q", got, err, skills)
	}
}

func TestAFactoryWithoutSkillsAddsNoPluginAndRemovesAnEarlierOne(t *testing.T) {
	p, home := setup(t, bin)
	skills := withSkills(t, home)
	command(t, p, worker())
	if err := os.RemoveAll(skills); err != nil {
		t.Fatal(err)
	}

	cmd := envruntest.Parse(t, command(t, p, worker())).Cmd

	if slices.Contains(cmd, "--plugin-dir") {
		t.Errorf("argv holds --plugin-dir: %v", cmd)
	}
	if _, err := os.Lstat(pluginDir(home, "worker-t1")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("plugin folder stays: %v", err)
	}
}
