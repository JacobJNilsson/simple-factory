package up

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRecordLeadPromptJoinsTheMachineAndTheFactoryAdditions(t *testing.T) {
	userHome := t.TempDir()
	home := filepath.Join(config.FactoriesDir(userHome), "acme")
	writeTestFile(t, filepath.Join(config.DefaultHome(userHome), "prompts", "lead.d", "a.md"), "machine addition")
	writeTestFile(t, filepath.Join(home, "prompts", "lead.d", "b.md"), "factory addition")

	layers, err := config.Layers(home, userHome)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recordLeadPrompt(home, layers); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(leadPromptPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\n\nmachine addition\n\nfactory addition\n\n## Your memory") {
		t.Fatalf("lead-prompt.md lacks the additions before the memory part: %q", b)
	}
}
