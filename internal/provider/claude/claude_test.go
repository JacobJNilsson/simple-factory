package claude_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/envrun/envruntest"
	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/provider"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
)

const bin = "/opt/sfy/sfy"

type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
	// AsyncRewake makes Claude Code wake the agent when the hook exits with code 2.
	AsyncRewake bool `json:"asyncRewake"`
}

type settings struct {
	Sandbox *struct {
		Enabled                  bool     `json:"enabled"`
		FailIfUnavailable        bool     `json:"failIfUnavailable"`
		AutoAllowBashIfSandboxed bool     `json:"autoAllowBashIfSandboxed"`
		AllowUnsandboxedCommands bool     `json:"allowUnsandboxedCommands"`
		ExcludedCommands         []string `json:"excludedCommands"`
		WeakerNetworkIsolation   bool     `json:"enableWeakerNetworkIsolation"`
		Network                  struct {
			AllowUnixSockets []string `json:"allowUnixSockets"`
		} `json:"network"`
		Filesystem struct {
			DenyRead   []string `json:"denyRead"`
			AllowWrite []string `json:"allowWrite"`
		} `json:"filesystem"`
	} `json:"sandbox"`
	Permissions struct {
		DefaultMode string   `json:"defaultMode"`
		Allow       []string `json:"allow"`
		Deny        []string `json:"deny"`
	} `json:"permissions"`
	Hooks map[string][]struct {
		Matcher string      `json:"matcher"`
		Hooks   []hookEntry `json:"hooks"`
	} `json:"hooks"`
}

func setup(t *testing.T, sfyBin string) (provider.Provider, string) {
	t.Helper()
	home := t.TempDir()
	return claude.New(config.Config{Allow: []string{"git", "make"}}, home, sfyBin, []string{home}), home
}

// setupSandbox gives the adapter sandbox lists and a fresh HOME that holds the allow_write folders.
func setupSandbox(t *testing.T) (provider.Provider, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	home := t.TempDir()
	cfg := config.Config{
		Allow:      []string{"git status", "make"},
		DenyRead:   []string{"~/.ssh", "~/.aws"},
		AllowWrite: []string{"~/Library/Caches", "~/go/pkg/mod"},
	}
	return claude.New(cfg, home, bin, []string{home}), home
}

func command(t *testing.T, p provider.Provider, spec engine.SessionSpec) []string {
	t.Helper()
	argv, err := p.Command(spec)
	if err != nil {
		t.Fatal(err)
	}
	return argv
}

func settingsPath(home, name string) string {
	return filepath.Join(home, "sessions", name, "settings.json")
}

func readSettings(t *testing.T, home, name string) settings {
	t.Helper()
	b, err := os.ReadFile(settingsPath(home, name))
	if err != nil {
		t.Fatal(err)
	}
	var s settings
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func worker() engine.SessionSpec {
	return engine.SessionSpec{
		Name: "worker-t1", Kind: "worker", Task: "t1", Dir: "/work/t1", Model: "sonnet",
		Prompt: "Do this task.", Protected: []string{"SPEC.md", "/abs/dir"}, Branch: "sfy/t1",
		TaskFile: "/state/tasks/t1/task.md",
	}
}

func mainSpec() engine.SessionSpec {
	return engine.SessionSpec{Name: "lead", Kind: "lead", Dir: "/state", Model: "opus", Prompt: "Start."}
}

func hookOf(t *testing.T, s settings, event string) hookEntry {
	t.Helper()
	groups := s.Hooks[event]
	if len(groups) != 1 || len(groups[0].Hooks) != 1 || groups[0].Hooks[0].Type != "command" {
		t.Fatalf("hook %s: %+v", event, groups)
	}
	return groups[0].Hooks[0]
}

func flagValue(t *testing.T, argv []string, flag string) string {
	t.Helper()
	i := slices.Index(argv, flag)
	if i < 0 || i+1 >= len(argv) {
		t.Fatalf("no %s in %v", flag, argv)
	}
	return argv[i+1]
}

func TestWorkerSettingsDenyQuestions(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())

	s := readSettings(t, home, "worker-t1")

	if !slices.Contains(s.Permissions.Deny, "AskUserQuestion") {
		t.Errorf("deny lacks AskUserQuestion: %v", s.Permissions.Deny)
	}
}

func TestWorkerSettingsDenyEditsToEachProtectedPathAndItsFiles(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())

	s := readSettings(t, home, "worker-t1")

	for _, want := range []string{"Edit(SPEC.md)", "Edit(SPEC.md/**)", "Edit(//abs/dir)", "Edit(//abs/dir/**)"} {
		if !slices.Contains(s.Permissions.Deny, want) {
			t.Errorf("deny lacks %s: %v", want, s.Permissions.Deny)
		}
	}
	if slices.Contains(s.Permissions.Deny, "Edit") {
		t.Errorf("a worker may edit: %v", s.Permissions.Deny)
	}
}

func TestProtectedPathLosesItsLeadingDotSlash(t *testing.T) {
	p, home := setup(t, bin)
	spec := worker()
	spec.Protected = []string{"./docs"}
	command(t, p, spec)

	s := readSettings(t, home, "worker-t1")

	for _, want := range []string{"Edit(docs)", "Edit(docs/**)"} {
		if !slices.Contains(s.Permissions.Deny, want) {
			t.Errorf("deny lacks %s: %v", want, s.Permissions.Deny)
		}
	}
}

func TestWorkerSettingsSetEveryHookToTheSfyBinary(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())

	s := readSettings(t, home, "worker-t1")

	if got := hookOf(t, s, "Stop").Command; got != bin+" hook turn-ended" {
		t.Errorf("Stop runs %q", got)
	}
	if got := hookOf(t, s, "Notification").Command; got != bin+" hook idle" {
		t.Errorf("idle hook %q", got)
	}
	if got := hookOf(t, s, "PermissionRequest").Command; got != bin+" hook permission" {
		t.Errorf("PermissionRequest runs %q", got)
	}
}

func TestIdleHookMatchesOnlyIdlePrompts(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())

	s := readSettings(t, home, "worker-t1")

	if got := s.Hooks["Notification"][0].Matcher; got != "idle_prompt" {
		t.Errorf("matcher %q", got)
	}
}

func TestHooksHaveATenSecondTimeout(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())

	s := readSettings(t, home, "worker-t1")

	for _, event := range []string{"Stop", "PermissionRequest"} {
		if got := hookOf(t, s, event).Timeout; got != 10 {
			t.Errorf("%s timeout %d", event, got)
		}
	}
}

func TestHookQuotesABinaryPathWithASpace(t *testing.T) {
	p, home := setup(t, "/opt/my tools/sfy")
	command(t, p, worker())

	s := readSettings(t, home, "worker-t1")

	want := "'/opt/my tools/sfy' hook turn-ended"
	if got := hookOf(t, s, "Stop").Command; got != want {
		t.Errorf("Stop runs %q, want %q", got, want)
	}
}

func TestMainSettingsAllowTheConfiguredCommandsAndSfy(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, mainSpec())

	s := readSettings(t, home, "lead")

	for _, want := range []string{"Bash(git *)", "Bash(make *)", "Bash(" + bin + " *)", "Bash(sfy *)"} {
		if !slices.Contains(s.Permissions.Allow, want) {
			t.Errorf("allow lacks %s: %v", want, s.Permissions.Allow)
		}
	}
}

func TestReviewerSettingsDenyEveryEdit(t *testing.T) {
	p, home := setup(t, bin)
	spec := worker()
	spec.Name, spec.Kind = "review-t1-1", "reviewer"
	command(t, p, spec)

	s := readSettings(t, home, "review-t1-1")

	for _, want := range []string{"AskUserQuestion", "Edit", "Write", "NotebookEdit"} {
		if !slices.Contains(s.Permissions.Deny, want) {
			t.Errorf("deny lacks %s: %v", want, s.Permissions.Deny)
		}
	}
}

func TestMainSettingsDenyNothingAndGuardMergesWithAHook(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, mainSpec())

	s := readSettings(t, home, "lead")

	if len(s.Permissions.Deny) != 0 {
		t.Errorf("main deny rules = %v, want none", s.Permissions.Deny)
	}
	groups := s.Hooks["PreToolUse"]
	if len(groups) != 1 || groups[0].Matcher != "Bash" || len(groups[0].Hooks) != 1 || groups[0].Hooks[0].Command != bin+" hook merge-guard" {
		t.Errorf("PreToolUse hooks = %+v", groups)
	}
	if hookOf(t, s, "Stop").Command == "" {
		t.Error("main has no Stop hook")
	}
}

func TestMainSessionRulesAllowAddsMergeRules(t *testing.T) {
	home := t.TempDir()
	cfg := config.Config{SessionRules: map[string]config.SessionRule{"lead": {Allow: []string{"Bash(gh pr merge *)"}}}}
	p := claude.New(cfg, home, bin, []string{home})
	command(t, p, mainSpec())

	s := readSettings(t, home, "lead")

	if !slices.Contains(s.Permissions.Allow, "Bash(gh pr merge *)") {
		t.Errorf("allow lacks the extra rule: %v", s.Permissions.Allow)
	}
}

func TestWorkersGetNoMergeGuardHook(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())

	if _, ok := readSettings(t, home, "worker-t1").Hooks["PreToolUse"]; ok {
		t.Error("a worker has the main agent's merge guard hook")
	}
}

func TestWorkerAndReviewerArgvUseTheCleanFlag(t *testing.T) {
	for _, spec := range []engine.SessionSpec{worker(), reviewerSpec()} {
		p, _ := setup(t, bin)

		c := envruntest.Parse(t, command(t, p, spec))

		if c.Bin != bin || !c.Clean {
			t.Errorf("%s: bin %q, clean %v", spec.Kind, c.Bin, c.Clean)
		}
	}
}

func TestMainArgvKeepsTheInheritedEnvironment(t *testing.T) {
	p, _ := setup(t, bin)

	c := envruntest.Parse(t, command(t, p, mainSpec()))

	if c.Bin != bin || c.Clean {
		t.Errorf("bin %q, clean %v", c.Bin, c.Clean)
	}
}

func TestWorkerEnvKeepsListedVariablesAndDropsSecrets(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret")
	t.Setenv("ANTHROPIC_API_KEY", "secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("MY_PASS", "kept")
	t.Setenv("LC_ALL", "C")
	t.Setenv("CLAUDE_CONFIG_DIR", "/cfg")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	t.Setenv("HOME", "/h")
	p := claude.New(config.Config{PassEnv: []string{"MY_PASS"}}, t.TempDir(), bin, nil)

	for _, spec := range []engine.SessionSpec{worker(), reviewerSpec()} {
		c := envruntest.Parse(t, command(t, p, spec))

		for _, e := range c.Env {
			if strings.HasSuffix(e, "=secret") {
				t.Errorf("%s env holds %q", spec.Kind, e)
			}
		}
		for _, want := range []string{"MY_PASS=kept", "LC_ALL=C", "CLAUDE_CONFIG_DIR=/cfg", "SSH_AUTH_SOCK=/tmp/agent.sock", "HOME=/h"} {
			if !slices.Contains(c.Env, want) {
				t.Errorf("%s env lacks %s: %v", spec.Kind, want, c.Env)
			}
		}
	}
}

func TestArgvRunsClaude(t *testing.T) {
	p, _ := setup(t, bin)

	c := envruntest.Parse(t, command(t, p, worker()))

	if c.Cmd[0] != "claude" {
		t.Errorf("command %q", c.Cmd[0])
	}
}

func TestArgvHoldsNoEnvironmentValue(t *testing.T) {
	p, _ := setup(t, bin)

	for _, a := range command(t, p, worker()) {
		if strings.HasPrefix(a, "SFY_") || strings.HasPrefix(a, "PATH=") {
			t.Errorf("argv holds a value: %s", a)
		}
	}
}

func TestEnvHoldsTheAgentIdentity(t *testing.T) {
	p, home := setup(t, bin)

	c := envruntest.Parse(t, command(t, p, worker()))

	for _, want := range []string{"SFY_AGENT=worker-t1", "SFY_TASK=t1", "SFY_HOME=" + home} {
		if !slices.Contains(c.Env, want) {
			t.Errorf("env lacks %s: %v", want, c.Env)
		}
	}
}

func TestEnvFileIsPrivate(t *testing.T) {
	p, _ := setup(t, bin)

	c := envruntest.Parse(t, command(t, p, worker()))

	info, err := os.Stat(c.File)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("env file %v, %v", info, err)
	}
}

func TestEnvFileIsInsideTheSessionFolder(t *testing.T) {
	p, home := setup(t, bin)

	c := envruntest.Parse(t, command(t, p, worker()))

	if filepath.Dir(c.File) != filepath.Join(home, "sessions", "worker-t1") {
		t.Errorf("env file %s", c.File)
	}
}

func TestEnvPutsTheSfyBinaryFolderFirstOnThePath(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	p, _ := setup(t, bin)

	c := envruntest.Parse(t, command(t, p, worker()))

	if !slices.Contains(c.Env, "PATH=/opt/sfy:/usr/bin:/bin") {
		t.Errorf("env %v", c.Env)
	}
}

func TestArgvNamesTheSessionAndSetsTheModel(t *testing.T) {
	p, _ := setup(t, bin)

	argv := command(t, p, worker())

	if got := flagValue(t, argv, "--name"); got != "worker-t1" {
		t.Errorf("--name %q", got)
	}
	if got := flagValue(t, argv, "--model"); got != "sonnet" {
		t.Errorf("--model %q", got)
	}
}

func TestArgvAcceptsEditsAndPointsAtTheSettingsFile(t *testing.T) {
	p, home := setup(t, bin)

	argv := command(t, p, worker())

	if got := flagValue(t, argv, "--permission-mode"); got != "acceptEdits" {
		t.Errorf("--permission-mode %q", got)
	}
	if got := flagValue(t, argv, "--settings"); got != settingsPath(home, "worker-t1") {
		t.Errorf("--settings %q", got)
	}
}

func TestArgvAppendsTheRolePrompt(t *testing.T) {
	p, _ := setup(t, bin)

	argv := command(t, p, worker())

	if got := flagValue(t, argv, "--append-system-prompt"); !strings.HasPrefix(got, "# Worker") {
		t.Errorf("system prompt starts %.20q", got)
	}
}

func TestArgvAddsTheRepoPartOnlyForATaskWithARepo(t *testing.T) {
	p, _ := setup(t, bin)
	plain := worker()
	withRepo := worker()
	withRepo.HasRepo = true

	got := flagValue(t, command(t, p, plain), "--append-system-prompt")
	gotRepo := flagValue(t, command(t, p, withRepo), "--append-system-prompt")

	if strings.Contains(got, "git push") {
		t.Error("a task with no repo gets git steps")
	}
	if !strings.Contains(gotRepo, "git push") {
		t.Error("a task with a repo lacks git steps")
	}
}

func TestWorkerPromptAndPushRulesNameTheTaskBranch(t *testing.T) {
	p, home := setup(t, bin)
	spec := worker()
	spec.HasRepo, spec.Branch = true, "fix/refuse-packaging-writes"

	prompt := flagValue(t, command(t, p, spec), "--append-system-prompt")

	if !strings.Contains(prompt, "git push -u origin fix/refuse-packaging-writes`") || strings.Contains(prompt, "<branch>") {
		t.Errorf("the prompt does not name the branch:\n%s", prompt)
	}
	rules := bashRules(readSettings(t, home, spec.Name).Permissions.Allow)
	for _, want := range []string{"Bash(git push -u origin fix/refuse-packaging-writes)", "Bash(git push --force-with-lease origin fix/refuse-packaging-writes)"} {
		if !slices.Contains(rules, want) {
			t.Errorf("missing rule %s in %q", want, rules)
		}
	}
}

func TestAWorkerOnAnExistingBranchMayPushOnlyWithALeaseOnTheCheckoutSha(t *testing.T) {
	p, home := setup(t, bin)
	spec := worker()
	spec.HasRepo, spec.Branch, spec.Lease = true, "chore/pr", "23da1c9"

	command(t, p, spec)

	rules := bashRules(readSettings(t, home, spec.Name).Permissions.Allow)
	for _, want := range []string{"Bash(git push -u origin chore/pr)", "Bash(git push --force-with-lease=chore/pr:23da1c9 origin HEAD:chore/pr)"} {
		if !slices.Contains(rules, want) {
			t.Errorf("missing rule %s in %q", want, rules)
		}
	}
	if slices.Contains(rules, "Bash(git push --force-with-lease origin chore/pr)") {
		t.Errorf("a push with a bare lease is allowed: %q", rules)
	}
}

func TestAHeldWorkerOnAnExistingBranchHasNoPushAllowRule(t *testing.T) {
	p, home := setup(t, bin)
	spec := worker()
	spec.HasRepo, spec.Branch, spec.Lease, spec.NoPush = true, "chore/pr", "23da1c9", true

	command(t, p, spec)

	perms := readSettings(t, home, spec.Name).Permissions
	for _, r := range perms.Allow {
		if strings.Contains(r, "git push") {
			t.Errorf("held worker allows %s", r)
		}
	}
	if !slices.Contains(perms.Deny, "Bash(git push*)") {
		t.Errorf("deny rules %q lack the push rule", perms.Deny)
	}
}

func TestAHeldWorkerHasNoPushAllowRuleAndEveryPushIsDenied(t *testing.T) {
	p, home := setup(t, bin)
	spec := worker()
	spec.HasRepo, spec.Branch, spec.NoPush = true, "fix/hold", true

	command(t, p, spec)

	perms := readSettings(t, home, spec.Name).Permissions
	for _, r := range perms.Allow {
		if strings.Contains(r, "git push") {
			t.Errorf("held worker allows %s", r)
		}
	}
	if !slices.Contains(perms.Deny, "Bash(git push*)") {
		t.Errorf("deny rules %q lack the push rule", perms.Deny)
	}
}

func TestArgvEndsWithThePrompt(t *testing.T) {
	p, _ := setup(t, bin)

	argv := command(t, p, worker())

	if got := argv[len(argv)-1]; got != "Do this task." {
		t.Errorf("last element %q", got)
	}
}

func TestMainEnvOmitsTheTask(t *testing.T) {
	p, _ := setup(t, bin)

	c := envruntest.Parse(t, command(t, p, mainSpec()))

	for _, a := range c.Env {
		if strings.HasPrefix(a, "SFY_TASK=") {
			t.Errorf("env has %s", a)
		}
	}
}

func TestSettingsFilePermissions(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())

	dir, err := os.Stat(filepath.Join(home, "sessions", "worker-t1"))
	if err != nil || dir.Mode().Perm() != 0o700 {
		t.Errorf("folder %v, %v", dir, err)
	}
	f, err := os.Stat(settingsPath(home, "worker-t1"))
	if err != nil || f.Mode().Perm() != 0o600 {
		t.Errorf("file %v, %v", f, err)
	}
}

func TestCommandRejectsAnUnknownKind(t *testing.T) {
	p, _ := setup(t, bin)

	if _, err := p.Command(engine.SessionSpec{Name: "x", Kind: "boss"}); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestCommandRejectsAPathInTheName(t *testing.T) {
	p, _ := setup(t, bin)

	if _, err := p.Command(engine.SessionSpec{Name: "../x", Kind: "worker"}); err == nil {
		t.Error("path in name accepted")
	}
}

func TestMainSettingsAllowRules(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, mainSpec())
	s := readSettings(t, home, "lead")

	for _, want := range []string{"Bash(sfy *)", "Read(/" + home + "/**)", "Bash(ls *)"} {
		t.Run(want, func(t *testing.T) {
			if !slices.Contains(s.Permissions.Allow, want) {
				t.Errorf("allow lacks %s: %v", want, s.Permissions.Allow)
			}
		})
	}
}

func TestMainBashRulesAreTheConfigSfyAndLsRules(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, mainSpec())

	s := readSettings(t, home, "lead")

	var got []string
	for _, a := range s.Permissions.Allow {
		if strings.HasPrefix(a, "Bash") {
			got = append(got, a)
		}
	}
	want := []string{"Bash(git *)", "Bash(make *)", "Bash(" + bin + " *)", "Bash(sfy *)", "Bash(ls *)"}
	if !slices.Equal(got, want) {
		t.Errorf("Bash rules %v, want %v", got, want)
	}
}

func TestMainHasNoPermissionModeFlag(t *testing.T) {
	p, _ := setup(t, bin)

	argv := command(t, p, mainSpec())

	if slices.Contains(argv, "--permission-mode") {
		t.Errorf("main argv has --permission-mode: %v", argv)
	}
}

func TestReviewerKeepsAcceptEditsFlag(t *testing.T) {
	p, _ := setup(t, bin)

	argv := command(t, p, reviewerSpec())

	if got := flagValue(t, argv, "--permission-mode"); got != "acceptEdits" {
		t.Errorf("--permission-mode %q", got)
	}
}

func TestWorkerAndReviewerMayReadOnlyTheirOwnTaskFile(t *testing.T) {
	for _, spec := range []engine.SessionSpec{worker(), reviewerSpec()} {
		p, home := setup(t, bin)
		command(t, p, spec)

		s := readSettings(t, home, spec.Name)

		var reads []string
		for _, a := range s.Permissions.Allow {
			if strings.HasPrefix(a, "Read(") {
				reads = append(reads, a)
			}
			if a == "Bash(ls *)" {
				t.Errorf("%s allows %s", spec.Name, a)
			}
		}
		if want := []string{"Read(//state/tasks/t1/task.md)"}; !slices.Equal(reads, want) {
			t.Errorf("%s read rules = %v, want %v", spec.Name, reads, want)
		}
		for _, r := range reads {
			if strings.Contains(r, "*") || strings.Contains(r, "tasks/t2/") || strings.Contains(r, "sessions") {
				t.Errorf("%s read rule %s is too wide", spec.Name, r)
			}
		}
	}
}

func TestWorkerAndReviewerKeepSecretsHiddenFromRead(t *testing.T) {
	for _, spec := range []engine.SessionSpec{worker(), reviewerSpec()} {
		p, home := setupSandbox(t)
		command(t, p, spec)

		s := readSettings(t, home, spec.Name)

		deny := s.Sandbox.Filesystem.DenyRead
		if len(deny) == 0 || deny[0] != filepath.Join(home, "sessions") {
			t.Errorf("%s DenyRead = %v, want it to start with the sessions folder", spec.Name, deny)
		}
		for _, secret := range []string{"/.ssh", "/.aws"} {
			if !slices.ContainsFunc(deny, func(d string) bool { return strings.HasSuffix(d, secret) }) {
				t.Errorf("%s DenyRead %v lacks %s", spec.Name, deny, secret)
			}
			for _, a := range s.Permissions.Allow {
				if strings.HasPrefix(a, "Read(") && strings.Contains(a, secret) {
					t.Errorf("%s allows %s", spec.Name, a)
				}
			}
		}
	}
}

func TestCommandRefusesAWorkerOrReviewerWithoutATaskFile(t *testing.T) {
	for _, spec := range []engine.SessionSpec{worker(), reviewerSpec()} {
		spec.TaskFile = ""
		p, home := setup(t, bin)

		if _, err := p.Command(spec); err == nil {
			t.Errorf("%s: Command accepted a spec with no task file", spec.Name)
		}
		if _, err := os.Stat(settingsPath(home, spec.Name)); err == nil {
			t.Errorf("%s: Command wrote settings", spec.Name)
		}
	}
	p, _ := setup(t, bin)
	command(t, p, mainSpec())
}

// sandboxed returns the settings of a worker and a reviewer, keyed by session name.
func sandboxed(t *testing.T) map[string]settings {
	t.Helper()
	out := map[string]settings{}
	for _, spec := range []engine.SessionSpec{worker(), reviewerSpec()} {
		p, home := setupSandbox(t)
		command(t, p, spec)
		out[spec.Name] = readSettings(t, home, spec.Name)
	}
	return out
}

func TestWorkerAndReviewerSandboxFlagsFailClosed(t *testing.T) {
	for name, s := range sandboxed(t) {
		sb := s.Sandbox
		if sb == nil || !sb.Enabled || !sb.FailIfUnavailable || !sb.AutoAllowBashIfSandboxed || sb.AllowUnsandboxedCommands {
			t.Errorf("%s sandbox = %+v", name, sb)
		}
	}
}

func TestWorkerAndReviewerSandboxPathsComeFromConfig(t *testing.T) {
	for name, s := range sandboxed(t) {
		// The first entry hides the session folders, which hold every session's token.
		deny := s.Sandbox.Filesystem.DenyRead
		if len(deny) == 0 || filepath.Base(deny[0]) != "sessions" || !slices.Equal(deny[1:], []string{"~/.ssh", "~/.aws"}) {
			t.Errorf("%s denyRead = %v", name, s.Sandbox.Filesystem.DenyRead)
		}
		if !slices.Equal(s.Sandbox.Filesystem.AllowWrite, []string{"~/Library/Caches", "~/go/pkg/mod"}) {
			t.Errorf("%s allowWrite = %v", name, s.Sandbox.Filesystem.AllowWrite)
		}
	}
}

func TestWorkerAndReviewerAcceptEditsAndFetchAnyDomain(t *testing.T) {
	for name, s := range sandboxed(t) {
		if s.Permissions.DefaultMode != "acceptEdits" {
			t.Errorf("%s defaultMode = %q", name, s.Permissions.DefaultMode)
		}
		if !slices.Contains(s.Permissions.Allow, "WebFetch(domain:*)") {
			t.Errorf("%s allow lacks WebFetch: %v", name, s.Permissions.Allow)
		}
	}
}

func TestSandboxReachesTLSTrustServiceOnMacOnly(t *testing.T) {
	for goos, want := range map[string]bool{"darwin": true, "linux": false} {
		for _, spec := range []engine.SessionSpec{worker(), reviewerSpec()} {
			t.Setenv("HOME", t.TempDir())
			home := t.TempDir()
			p := claude.NewForOS(config.Config{}, home, bin, goos, 501)
			command(t, p, spec)
			s := readSettings(t, home, spec.Name)
			if s.Sandbox.WeakerNetworkIsolation != want {
				t.Errorf("%s on %s: enableWeakerNetworkIsolation = %v, want %v", spec.Name, goos, s.Sandbox.WeakerNetworkIsolation, want)
			}
		}
	}
}

func TestSandboxAllowsUnixSocketsInItsTempFolderOnMacOnly(t *testing.T) {
	want := map[string][]string{
		"darwin": {"/tmp/claude-501", "/private/tmp/claude-501"},
		"linux":  nil,
	}
	for goos, sockets := range want {
		for _, spec := range []engine.SessionSpec{worker(), reviewerSpec()} {
			t.Setenv("HOME", t.TempDir())
			home := t.TempDir()
			p := claude.NewForOS(config.Config{}, home, bin, goos, 501)
			command(t, p, spec)
			s := readSettings(t, home, spec.Name)
			if !slices.Equal(s.Sandbox.Network.AllowUnixSockets, sockets) {
				t.Errorf("%s on %s: allowUnixSockets = %v, want %v", spec.Name, goos, s.Sandbox.Network.AllowUnixSockets, sockets)
			}
		}
	}
}

func TestMainSettingsHaveNoSandbox(t *testing.T) {
	p, home := setupSandbox(t)
	command(t, p, mainSpec())

	s := readSettings(t, home, "lead")

	if s.Sandbox != nil || slices.Contains(s.Permissions.Allow, "WebFetch(domain:*)") {
		t.Fatalf("main got sandbox settings: %+v", s)
	}
}

func TestCommandCreatesEachAllowWriteFolder(t *testing.T) {
	p, _ := setupSandbox(t)
	command(t, p, worker())

	for _, rel := range []string{"Library/Caches", "go/pkg/mod"} {
		home := os.Getenv("HOME")
		if st, err := os.Stat(filepath.Join(home, rel)); err != nil || !st.IsDir() {
			t.Errorf("%s not created: %v", rel, err)
		}
	}
}

func reviewerSpec() engine.SessionSpec {
	spec := worker()
	spec.Name, spec.Kind = "review-t1-1", "reviewer"
	return spec
}

func bashRules(allow []string) []string {
	var out []string
	for _, r := range allow {
		if strings.HasPrefix(r, "Bash(") {
			out = append(out, r)
		}
	}
	slices.Sort(out)
	return out
}

// TestWorkerAndReviewerBashRulesAreExact pins the whole Bash allow set. A broad git rule, a rule from config allow,
// or a second push form would fail it.
func TestWorkerAndReviewerBashRulesAreExact(t *testing.T) {
	workerWant := []string{"Bash(sfy report *)", "Bash(sfy inbox)", "Bash(git push -u origin sfy/t1)", "Bash(git push --force-with-lease origin sfy/t1)"}
	reviewerWant := []string{"Bash(sfy verdict *)", "Bash(sfy report blocked *)", "Bash(sfy inbox)"}
	cases := map[string]struct {
		spec engine.SessionSpec
		want []string
	}{
		"worker":   {worker(), workerWant},
		"reviewer": {reviewerSpec(), reviewerWant},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			home := t.TempDir()
			p := claude.New(config.Config{Allow: []string{"git", "make"}}, home, bin, []string{home})
			command(t, p, c.spec)

			got := bashRules(readSettings(t, home, c.spec.Name).Permissions.Allow)

			want := slices.Clone(c.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("Bash rules\n got %v\nwant %v", got, want)
			}
		})
	}
}

func TestWorkerAndReviewerSandboxExcludesGitPushAndSfy(t *testing.T) {
	for name, s := range sandboxed(t) {
		want := []string{"git push *", "sfy report *", "sfy inbox"}
		if name == "review-t1-1" {
			want = []string{"git push *", "sfy verdict *", "sfy report blocked *", "sfy inbox"}
		}
		if got := s.Sandbox.ExcludedCommands; !slices.Equal(got, want) {
			t.Errorf("%s excludedCommands = %v, want %v", name, got, want)
		}
		if slices.Contains(s.Sandbox.ExcludedCommands, "sfy *") || slices.Contains(s.Permissions.Allow, "Bash(sfy *)") {
			t.Errorf("%s has the wide sfy rule", name)
		}
	}
}

func TestEachSessionGetsItsOwnTokenFile(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())
	command(t, p, reviewerSpec())
	f := identity.Folder{Home: home}

	for _, name := range []string{"worker-t1", "review-t1-1"} {
		if got, err := f.Authenticate(protocol.Caller{Agent: name, Token: f.Token(name)}); err != nil || got != name {
			t.Errorf("the token of %s names %q, %v", name, got, err)
		}
	}
}

func TestSessionRulesAddAllowRulesAndExcludedCommandsForOneKind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home := t.TempDir()
	cfg := config.Config{SessionRules: map[string]config.SessionRule{
		"worker": {Allow: []string{"Bash(tool open *)"}, ExcludedCommands: []string{"tool *"}},
	}}
	p := claude.New(cfg, home, bin, []string{home})
	w, r := worker(), reviewerSpec()
	command(t, p, w)
	command(t, p, r)

	ws, rs := readSettings(t, home, w.Name), readSettings(t, home, r.Name)

	if !slices.Contains(ws.Permissions.Allow, "Bash(tool open *)") {
		t.Errorf("worker allow lacks the extra rule: %v", ws.Permissions.Allow)
	}
	if got := ws.Sandbox.ExcludedCommands; !slices.Equal(got, []string{"git push *", "sfy report *", "sfy inbox", "tool *"}) {
		t.Errorf("worker excludedCommands = %v", got)
	}
	if slices.Contains(rs.Permissions.Allow, "Bash(tool open *)") || slices.Contains(rs.Sandbox.ExcludedCommands, "tool *") {
		t.Errorf("reviewer got the worker rules: %+v", rs)
	}
}

// A push with a git option before the subcommand, such as git -C . push or git -c a=b push, is not an
// excluded command. It stays sandboxed and auto-approved unless a deny rule matches it.
func TestWorkerAndReviewerDenyAPushThatStartsWithAGitOption(t *testing.T) {
	for name, s := range sandboxed(t) {
		if !slices.Contains(s.Permissions.Deny, "Bash(git -* push*)") {
			t.Errorf("%s deny lacks the git-option push rule: %v", name, s.Permissions.Deny)
		}
		for _, r := range s.Permissions.Allow {
			if strings.Contains(r, "git -") {
				t.Errorf("%s allows %s, which the deny rule would override", name, r)
			}
		}
	}
}

func TestReviewerHasNoPushAllowRule(t *testing.T) {
	for _, r := range sandboxed(t)["review-t1-1"].Permissions.Allow {
		if strings.Contains(r, "push") {
			t.Errorf("reviewer allows %s", r)
		}
	}
}

func extraSettings(t *testing.T, kind string, cfg config.Config) config.SessionValues {
	t.Helper()
	home := t.TempDir()
	p := claude.New(cfg, home, bin, []string{home})
	spec := map[string]engine.SessionSpec{"lead": mainSpec(), "worker": worker(), "reviewer": reviewerSpec()}[kind]
	command(t, p, spec)
	b, err := os.ReadFile(settingsPath(home, spec.Name))
	if err != nil {
		t.Fatal(err)
	}
	var m config.SessionValues
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSessionSettingsReachOnlyTheirKind(t *testing.T) {
	cfg := config.Config{SessionSettings: map[string]config.SessionValues{
		"worker":   {"extraKey": "w"},
		"reviewer": {"extraKey": "r", "n": 3},
	}}

	w := extraSettings(t, "worker", cfg)
	if w["extraKey"] != "w" {
		t.Errorf("worker: %v", w["extraKey"])
	}
	if _, ok := w["permissions"]; !ok {
		t.Error("worker lost its permissions")
	}
	r := extraSettings(t, "reviewer", cfg)
	if r["extraKey"] != "r" || r["n"] != float64(3) {
		t.Errorf("reviewer: %v", r)
	}
	if _, ok := extraSettings(t, "lead", cfg)["extraKey"]; ok {
		t.Error("main got a key from another kind")
	}
}

func TestSessionSettingsWriteNestedValuesAsJSON(t *testing.T) {
	cfg := config.Config{SessionSettings: map[string]config.SessionValues{
		"lead": {"attribution": map[string]any{"commit": "", "pr": ""}, "list": []any{"a", "b"}},
	}}

	got := extraSettings(t, "lead", cfg)

	if obj, ok := got["attribution"].(map[string]any); !ok || obj["commit"] != "" || obj["pr"] != "" {
		t.Errorf("attribution: %v", got["attribution"])
	}
	if arr, ok := got["list"].([]any); !ok || len(arr) != 2 || arr[0] != "a" {
		t.Errorf("list: %v", got["list"])
	}
}

func TestSessionSettingsLeaveTheFileUnchangedWithoutTables(t *testing.T) {
	home := t.TempDir()
	read := func(cfg config.Config) []byte {
		command(t, claude.New(cfg, home, bin, []string{home}), worker())
		b, err := os.ReadFile(settingsPath(home, worker().Name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	plain := read(config.Config{})
	empty := read(config.Config{SessionSettings: map[string]config.SessionValues{"worker": {}}})

	if string(plain) != string(empty) {
		t.Errorf("files differ:\n%s\n%s", plain, empty)
	}
}

func TestMainSettingsKeepTheFieldOrderAndNoSuggestionKey(t *testing.T) {
	home := t.TempDir()
	command(t, claude.New(config.Config{}, home, bin, []string{home}), mainSpec())
	b, err := os.ReadFile(settingsPath(home, mainSpec().Name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "{\n  \"permissions\"") || strings.Contains(string(b), "promptSuggestionEnabled") {
		t.Errorf("main settings changed: %.60s", b)
	}
}

func TestWorkerAndReviewerTurnOffPromptSuggestions(t *testing.T) {
	for _, kind := range []string{"worker", "reviewer"} {
		if got := extraSettings(t, kind, config.Config{})["promptSuggestionEnabled"]; got != false {
			t.Errorf("%s: promptSuggestionEnabled = %v, want false", kind, got)
		}
	}
	if _, ok := extraSettings(t, "lead", config.Config{})["promptSuggestionEnabled"]; ok {
		t.Error("main got promptSuggestionEnabled")
	}
}

// settingsWith writes a worker's settings with one session_settings.worker table and reads them back.
func settingsWith(t *testing.T, table config.SessionValues) (settings, config.SessionValues) {
	t.Helper()
	home := t.TempDir()
	cfg := config.Config{SessionSettings: map[string]config.SessionValues{"worker": table}}
	command(t, claude.New(cfg, home, bin, []string{home}), worker())
	b, err := os.ReadFile(settingsPath(home, "worker-t1"))
	if err != nil {
		t.Fatal(err)
	}
	var raw config.SessionValues
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	return readSettings(t, home, "worker-t1"), raw
}

func TestSessionSettingsDenyAddsARuleAndKeepsSfysDenyRules(t *testing.T) {
	plain, _ := settingsWith(t, config.SessionValues{})
	s, _ := settingsWith(t, config.SessionValues{
		"permissions": map[string]any{"deny": []any{"Bash(curl *)"}},
	})

	if want := slices.Concat(plain.Permissions.Deny, []string{"Bash(curl *)"}); !slices.Equal(s.Permissions.Deny, want) {
		t.Errorf("deny %v, want %v", s.Permissions.Deny, want)
	}
}

func TestSessionSettingsKeepSfysValueOnAConflict(t *testing.T) {
	s, _ := settingsWith(t, config.SessionValues{
		"permissions": map[string]any{"defaultMode": "plan"},
	})

	if s.Permissions.DefaultMode != "acceptEdits" {
		t.Errorf("defaultMode %q, want sfy's acceptEdits", s.Permissions.DefaultMode)
	}
}

func TestSessionSettingsStopHookFollowsSfysStopHook(t *testing.T) {
	extra := map[string]any{"type": "command", "command": "echo done"}
	s, _ := settingsWith(t, config.SessionValues{
		"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{extra}}}},
	})

	groups := s.Hooks["Stop"]
	if len(groups) != 2 || groups[0].Hooks[0].Command != bin+" hook turn-ended" || groups[1].Hooks[0].Command != "echo done" {
		t.Fatalf("Stop hooks %+v, want sfy's hook and then the extra hook", groups)
	}
}

func TestSessionSettingsSandboxAddsAKeyAndKeepsSfysSandbox(t *testing.T) {
	s, raw := settingsWith(t, config.SessionValues{
		"sandbox": map[string]any{"enabled": false, "network": map[string]any{"allowedDomains": []any{"example.com"}}},
	})

	if !s.Sandbox.Enabled || !s.Sandbox.FailIfUnavailable {
		t.Errorf("sandbox %+v, want sfy's sandbox on", s.Sandbox)
	}
	box, _ := raw["sandbox"].(map[string]any)
	network, _ := box["network"].(map[string]any)
	if domains, ok := network["allowedDomains"].([]any); !ok || len(domains) != 1 || domains[0] != "example.com" {
		t.Errorf("sandbox.network %v, want the extra allowedDomains", network)
	}
}

func TestConfigOverridesPromptSuggestionDefault(t *testing.T) {
	cfg := config.Config{SessionSettings: map[string]config.SessionValues{
		"worker":   {"promptSuggestionEnabled": true},
		"reviewer": {"promptSuggestionEnabled": true},
	}}
	for _, kind := range []string{"worker", "reviewer"} {
		if got := extraSettings(t, kind, cfg)["promptSuggestionEnabled"]; got != true {
			t.Errorf("%s: promptSuggestionEnabled = %v, want true", kind, got)
		}
	}
}
