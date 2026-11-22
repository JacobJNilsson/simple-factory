package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// writeFactory writes config.d/50-gate.toml and config.toml into a factory folder below a fresh user home.
func writeFactory(t *testing.T, part, main string) (home, userHome string) {
	t.Helper()
	userHome = t.TempDir()
	home = filepath.Join(userHome, ".sfy", "factories", "web")
	if err := os.MkdirAll(filepath.Join(home, "config.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.d", "50-gate.toml"), []byte(part), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, userHome
}

const gate = `
[agents.gate]
harness = "shell"
run = ["sh {factory}/checks/gate.sh {task_file}"]
show = "{from} (gate runs)"

[lists.build]
agents = ["worker", "gate", "check", "reviewer"]

[lists.build.then]
fail.gate = { list = "build", round = true }
`

func TestLoadProcessAddsAGateFromConfigD(t *testing.T) {
	home, userHome := writeFactory(t, gate, "review_rounds = 4\n")
	cfg, p, err := config.LoadProcess(home, userHome)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReviewRounds != 4 {
		t.Fatalf("review_rounds = %d, want 4", cfg.ReviewRounds)
	}
	agents, err := p.Flatten("build")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"worker", "gate", "check", "reviewer"}; !slices.Equal(agents, want) {
		t.Fatalf("build = %v, want %v", agents, want)
	}
}

func TestLoadProcessLetsConfigTomlReplaceAList(t *testing.T) {
	home, userHome := writeFactory(t, gate, "[lists.build]\nagents = [\"worker\", \"reviewer\"]\n")
	_, p, err := config.LoadProcess(home, userHome)
	if err != nil {
		t.Fatal(err)
	}
	if agents, _ := p.Flatten("build"); !slices.Equal(agents, []string{"worker", "reviewer"}) {
		t.Fatalf("build = %v, want config.toml's list", agents)
	}
}

func TestLoadRefusesAProcessThatDoesNotValidate(t *testing.T) {
	home, userHome := writeFactory(t, "[agents.worker]\nrun = [\"true\"]\n", "")
	if _, err := config.LoadFactory(home, userHome); err == nil {
		t.Fatal("a run key on a claude agent loaded")
	}
}

func TestSessionRulesTakeAnAgentsAllowList(t *testing.T) {
	home, userHome := writeFactory(t, "[agents.worker.permissions]\nallow = [\"Bash(gh pr view *)\"]\n", "[session_rules.worker]\nallow = [\"Bash(git fetch *)\"]\n")
	cfg, err := config.LoadFactory(home, userHome)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.SessionRules["worker"].Allow, []string{"Bash(git fetch *)", "Bash(gh pr view *)"}; !slices.Equal(got, want) {
		t.Fatalf("worker allow = %v, want %v", got, want)
	}
}
