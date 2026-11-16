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
	"worker":   {"report done", "report blocked", "report progress", "report pushed", "inbox", "task list", "task show", "status"},
	"reviewer": {"verdict pass", "verdict fail", "report blocked", "inbox", "task list", "task show", "status"},
	"main": {"task new", "task edit", "task queue", "task list", "task show", "task cancel", "task resume", "task feedback", "task complete", "task archive",
		"send", "escalate", "inbox", "status", "restart"},
}

var command = regexp.MustCompile("`sfy ([^`]*)`")

func TestEachPromptNamesOnlyCommandsOfItsKind(t *testing.T) {
	for kind, cmds := range allowed {
		text, err := prompts.Role(kind, t.TempDir(), true)
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
		"worker":   {"report done", "report blocked", "report progress", "report pushed", "inbox"},
		"reviewer": {"verdict pass", "verdict fail", "inbox"},
		"main":     {"task new", "task edit", "task queue", "task resume", "task feedback", "task complete", "send", "escalate", "inbox", "restart"},
	}
	for kind, cmds := range required {
		text, err := prompts.Role(kind, t.TempDir(), true)
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

func TestWorkerPromptWithoutARepoHasNoGitSteps(t *testing.T) {
	text, err := prompts.Role("worker", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"commit", "push", "branch", "rebase", "pull request"} {
		if strings.Contains(strings.ToLower(text), word) {
			t.Errorf("no-repo worker prompt contains %q", word)
		}
	}
	if !strings.Contains(text, "`sfy report done") {
		t.Error("no-repo worker prompt lacks `sfy report done`")
	}
}

func TestWorkerPromptWithARepoHasBothParts(t *testing.T) {
	home := t.TempDir()
	base, err := prompts.Role("worker", home, false)
	if err != nil {
		t.Fatal(err)
	}

	text, err := prompts.Role("worker", home, true)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(text, strings.TrimRight(base, "\n")) {
		t.Error("repo prompt does not start with the common part")
	}
	for _, want := range []string{"git push -u origin", "`sfy report pushed", "gh pr create"} {
		if !strings.Contains(text, want) {
			t.Errorf("repo prompt lacks %q", want)
		}
	}
}

func TestOnlyTheWorkerGetsTheRepoPart(t *testing.T) {
	for _, kind := range []string{"main", "reviewer"} {
		withRepo, err := prompts.Role(kind, t.TempDir(), true)
		if err != nil {
			t.Fatal(err)
		}
		plain, _ := prompts.For(kind, t.TempDir())
		if withRepo != plain {
			t.Errorf("%s prompt changes with a repo", kind)
		}
	}
}

func TestMainPromptTellsHowToCompactNotes(t *testing.T) {
	text, err := prompts.For("main", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"longer than 150 lines", "older than 7 days", "`notes-archive.md`", "Do not read `notes-archive.md` at start"} {
		if !strings.Contains(text, want) {
			t.Errorf("main prompt lacks %q", want)
		}
	}
}

func TestEachWorkerPartCanBeOverridden(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "prompts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"worker.md": "common", "worker-repo.md": "repo"} {
		if err := os.WriteFile(filepath.Join(home, "prompts", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	with, _ := prompts.Role("worker", home, true)
	without, _ := prompts.Role("worker", home, false)

	if with != "common\n\nrepo" || without != "common" {
		t.Fatalf("with %q, without %q", with, without)
	}
}
