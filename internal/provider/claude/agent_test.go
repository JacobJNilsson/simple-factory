package claude_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
)

// loader returns the spec of a session of an agent named loader, whose prompt is prompts/loader.md.
func loader() engine.SessionSpec {
	spec := worker()
	spec.Name, spec.Agent, spec.Role, spec.RepoRole = "loader-t1", "loader", "loader", "worker-repo"
	return spec
}

func TestAnAgentSessionTakesThePromptOfItsRole(t *testing.T) {
	p, home := setup(t, bin)
	if err := os.MkdirAll(filepath.Join(home, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "prompts", "loader.md"), []byte("Load the source.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := loader()
	spec.HasRepo = true

	role := flagValue(t, command(t, p, spec), "--append-system-prompt")

	if !strings.HasPrefix(role, "Load the source.\n\n## Your task has a repo") {
		t.Fatalf("role = %q, want the loader prompt and then the repo part", role)
	}
}

func TestAnAgentSessionWithoutAPromptFileFails(t *testing.T) {
	p, _ := setup(t, bin)

	if _, err := p.Command(loader()); err == nil {
		t.Fatal("a session with no prompts/loader.md started")
	}
}

func TestAnAgentSessionTakesTheRulesOfItsAgent(t *testing.T) {
	home := t.TempDir()
	cfg := config.Config{SessionRules: map[string]config.SessionRule{"loader": {Allow: []string{"Bash(dbt run *)"}}}}
	p := claude.New(cfg, home, bin, []string{home})
	spec := loader()
	spec.Role = ""

	command(t, p, spec)

	if allow := readSettings(t, home, "loader-t1").Permissions.Allow; !slices.Contains(allow, "Bash(dbt run *)") {
		t.Fatalf("allow = %v, want the rule of session_rules.loader", allow)
	}
}

func TestAReadOnlyAgentGetsNoEditTools(t *testing.T) {
	p, home := setup(t, bin)
	spec := worker()
	spec.ReadOnly = true

	command(t, p, spec)

	deny := readSettings(t, home, "worker-t1").Permissions.Deny
	for _, tool := range []string{"Edit", "Write", "NotebookEdit"} {
		if !slices.Contains(deny, tool) {
			t.Errorf("deny = %v, want %s", deny, tool)
		}
	}
}

func TestAnAgentsOwnListsReplaceTheFactoryLists(t *testing.T) {
	p, home := setupSandbox(t)
	spec := worker()
	spec.DenyRead, spec.AllowWrite = &[]string{"~/.secret"}, &[]string{}

	command(t, p, spec)

	fs := readSettings(t, home, "worker-t1").Sandbox.Filesystem
	if !slices.Contains(fs.DenyRead, "~/.secret") || slices.Contains(fs.DenyRead, "~/.ssh") || len(fs.AllowWrite) != 0 {
		t.Fatalf("filesystem = %+v, want the agent's own lists", fs)
	}
}

func TestAnUnsandboxedAgentGetsNoSandbox(t *testing.T) {
	p, home := setup(t, bin)
	spec := worker()
	spec.Unsandboxed = true

	command(t, p, spec)

	if s := readSettings(t, home, "worker-t1"); s.Sandbox != nil {
		t.Fatalf("sandbox = %+v, want none", s.Sandbox)
	}
}
