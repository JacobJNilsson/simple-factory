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
	got, err := prompts.For("worker", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "sfy report done") {
		t.Fatalf("got %q", got)
	}
}

func TestEveryRolePromptSaysThatSfyDeliversNotesAsHookFeedback(t *testing.T) {
	for _, kind := range []string{"lead", "worker", "reviewer"} {
		got, err := prompts.For(kind, []string{t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"hook feedback", "not from the operator", "typed note"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s prompt lacks %q", kind, want)
			}
		}
	}
}

func TestWorkerAndReviewerPromptsKeepBackticksOutOfSfyCommands(t *testing.T) {
	for _, kind := range []string{"worker", "reviewer"} {
		got, err := prompts.Role(kind, []string{t.TempDir()}, false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "Never put a backtick") {
			t.Errorf("%s prompt does not name the backtick", kind)
		}
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

	got, err := prompts.For("worker", []string{home})

	if err != nil || got != "custom" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// writePrompt writes body to <home>/prompts/<rel>.
func writePrompt(t *testing.T, home, rel, body string) {
	t.Helper()
	path := filepath.Join(home, "prompts", rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestForWithoutAdditionsFolderReturnsTheDefault(t *testing.T) {
	home := t.TempDir()
	writePrompt(t, home, "other.d/x.md", "unrelated")

	got, err := prompts.For("worker", []string{home})
	want, _ := prompts.For("worker", []string{t.TempDir()})

	if err != nil || got != want {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForAppendsAdditionsInNameOrder(t *testing.T) {
	home := t.TempDir()
	writePrompt(t, home, "worker.d/20-b.md", "second")
	writePrompt(t, home, "worker.d/10-a.md", "first\n")
	base, _ := prompts.For("worker", []string{t.TempDir()})

	got, err := prompts.For("worker", []string{home})

	want := strings.TrimRight(base, "\n") + "\n\nfirst\n\nsecond"
	if err != nil || got != want {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForSkipsFilesWithoutTheMdSuffix(t *testing.T) {
	home := t.TempDir()
	writePrompt(t, home, "worker.d/a.txt", "skipped-txt")
	writePrompt(t, home, "worker.d/b.md.bak", "backup")
	writePrompt(t, home, "worker.d/c.md", "kept")

	got, err := prompts.For("worker", []string{home})

	if err != nil || !strings.HasSuffix(got, "\n\nkept") || strings.Contains(got, "skipped-txt") || strings.Contains(got, "backup") {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestForAppendsAdditionsAfterAReplacingPrompt(t *testing.T) {
	home := t.TempDir()
	writePrompt(t, home, "reviewer.md", "custom\n")
	writePrompt(t, home, "reviewer.d/a.md", "extra")

	got, err := prompts.For("reviewer", []string{home})

	if err != nil || got != "custom\n\nextra" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRoleAppendsAdditionsToTheWorkerRepoPart(t *testing.T) {
	home := t.TempDir()
	writePrompt(t, home, "worker.md", "common")
	writePrompt(t, home, "worker-repo.md", "repo")
	writePrompt(t, home, "worker.d/a.md", "A")
	writePrompt(t, home, "worker-repo.d/a.md", "R")

	with, err := prompts.Role("worker", []string{home}, true)
	if err != nil {
		t.Fatal(err)
	}
	without, _ := prompts.Role("worker", []string{home}, false)

	if with != "common\n\nA\n\nrepo\n\nR" || without != "common\n\nA" {
		t.Fatalf("with %q, without %q", with, without)
	}
}

func TestForFailsOnAnUnreadableAddition(t *testing.T) {
	home := t.TempDir()
	writePrompt(t, home, "lead.d/a.md", "x")
	if err := os.Chmod(filepath.Join(home, "prompts", "lead.d", "a.md"), 0); err != nil {
		t.Fatal(err)
	}

	if _, err := prompts.For("lead", []string{home}); err == nil {
		t.Fatal("want an error")
	}
}

func TestForRejectsAnUnknownKind(t *testing.T) {
	if _, err := prompts.For("../x", []string{t.TempDir()}); err == nil {
		t.Fatal("want an error")
	}
}

// allowed lists the commands that each kind may run, from the spec's command table.
var allowed = map[string][]string{
	"worker":   {"report done", "report blocked", "report progress", "report pushed", "inbox", "task list", "task show", "status"},
	"reviewer": {"verdict pass", "verdict fail", "report blocked", "inbox", "task list", "task show", "status"},
	"lead": {"task new", "task edit", "task queue", "task list", "task show", "task cancel", "task resume", "task feedback", "task complete", "task integrate", "task push", "task archive",
		"send", "escalate", "inbox", "status", "restart"},
}

var command = regexp.MustCompile("`sfy ([^`]*)`")

func TestEachPromptNamesOnlyCommandsOfItsKind(t *testing.T) {
	for kind, cmds := range allowed {
		text, err := prompts.Role(kind, []string{t.TempDir()}, true)
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
		"lead":     {"task new", "task edit", "task queue", "task resume", "task feedback", "task complete", "task integrate", "send", "escalate", "inbox", "restart"},
	}
	for kind, cmds := range required {
		text, err := prompts.Role(kind, []string{t.TempDir()}, true)
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
	text, err := prompts.For("reviewer", []string{t.TempDir()})
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
	text, err := prompts.Role("worker", []string{t.TempDir()}, false)
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
	base, err := prompts.Role("worker", []string{home}, false)
	if err != nil {
		t.Fatal(err)
	}

	text, err := prompts.Role("worker", []string{home}, true)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(text, strings.TrimRight(base, "\n")) {
		t.Error("repo prompt does not start with the common part")
	}
	for _, want := range []string{"git push -u origin", "`sfy report pushed", "no remote"} {
		if !strings.Contains(text, want) {
			t.Errorf("repo prompt lacks %q", want)
		}
	}
}

func TestDefaultPromptsNeedNeitherGhNorPullRequests(t *testing.T) {
	for _, kind := range []string{"lead", "worker", "reviewer", prompts.WorkerRepoPart} {
		text, err := prompts.For(kind, []string{t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if regexp.MustCompile(`(?i)\bgh\b|pull request`).MatchString(text) {
			t.Errorf("%s prompt names gh or pull requests", kind)
		}
	}
}

func TestLeadPromptIntegratesWithTheSfyCommandAndHonorsNoMerge(t *testing.T) {
	text, err := prompts.For("lead", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sfy task integrate <id>", "Never merge, rebase or push by hand", "no-merge", ".sfy.toml", "tell the operator its name"} {
		if !strings.Contains(text, want) {
			t.Errorf("lead prompt lacks %q", want)
		}
	}
}

func TestOnlyTheWorkerGetsTheRepoPart(t *testing.T) {
	for _, kind := range []string{"lead", "reviewer"} {
		withRepo, err := prompts.Role(kind, []string{t.TempDir()}, true)
		if err != nil {
			t.Fatal(err)
		}
		plain, _ := prompts.For(kind, []string{t.TempDir()})
		if withRepo != plain {
			t.Errorf("%s prompt changes with a repo", kind)
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

	with, _ := prompts.Role("worker", []string{home}, true)
	without, _ := prompts.Role("worker", []string{home}, false)

	if with != "common\n\nrepo" || without != "common" {
		t.Fatalf("with %q, without %q", with, without)
	}
}
