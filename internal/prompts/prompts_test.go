package prompts_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/prompts"
)

func TestForReturnsTheEmbeddedWorkerPrompt(t *testing.T) {
	got, err := prompts.For("worker", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "sfy report done") {
		t.Fatalf("got %q", got)
	}
}

func TestForPrefersAFileInTheHomePromptsFolder(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "prompts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "prompts", "worker.md"), []byte("custom"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := prompts.For("worker", home)

	if err != nil || got != "custom" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForRejectsAnUnknownKind(t *testing.T) {
	if _, err := prompts.For("../x", t.TempDir()); err == nil {
		t.Fatal("want an error")
	}
}

// allowed lists the commands that each kind may run, from the spec's command table.
var allowed = map[string][]string{
	"worker":   {"report done", "report blocked", "report progress", "inbox", "task list", "task show", "status"},
	"reviewer": {"verdict pass", "verdict fail", "report blocked", "inbox", "task list", "task show", "status"},
	"main": {"task new", "task edit", "task queue", "task list", "task show", "task cancel", "task resume",
		"send", "escalate", "inbox", "status"},
}

var command = regexp.MustCompile("`sfy ([^`]*)`")

func TestEachPromptNamesOnlyCommandsOfItsKind(t *testing.T) {
	for kind, cmds := range allowed {
		text, err := prompts.For(kind, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		found := command.FindAllStringSubmatch(text, -1)
		if len(found) == 0 {
			t.Fatalf("%s prompt names no command", kind)
		}
		for _, m := range found {
			if !permitted(cmds, m[1]) {
				t.Errorf("%s prompt names `sfy %s`, which the kind may not run", kind, m[1])
			}
		}
	}
}

func permitted(cmds []string, args string) bool {
	for _, c := range cmds {
		if args == c || strings.HasPrefix(args, c+" ") {
			return true
		}
	}
	return false
}

func TestEachPromptNamesItsRequiredCommands(t *testing.T) {
	required := map[string][]string{
		"worker":   {"report done", "report blocked", "report progress", "inbox"},
		"reviewer": {"verdict pass", "verdict fail", "inbox"},
		"main":     {"task new", "task edit", "task queue", "task resume", "send", "escalate", "inbox"},
	}
	for kind, cmds := range required {
		text, err := prompts.For(kind, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cmds {
			if !strings.Contains(text, "`sfy "+c) {
				t.Errorf("%s prompt lacks `sfy %s`", kind, c)
			}
		}
	}
}

func TestReviewerPromptGivesTheVerdictAsAnArgument(t *testing.T) {
	text, err := prompts.For("reviewer", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"`sfy verdict pass '", "`sfy verdict fail '"} {
		if !strings.Contains(text, want) {
			t.Errorf("reviewer prompt lacks %s", want)
		}
	}
	if strings.Contains(text, "<<") {
		t.Error("reviewer prompt names a here-document")
	}
	if strings.Contains(text, "/tmp") {
		t.Error("reviewer prompt names /tmp")
	}
}
