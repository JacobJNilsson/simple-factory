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
	"github.com/JacobJNilsson/simple-factory/internal/provider"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
)

const bin = "/opt/sfy/sfy"

type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type settings struct {
	Sandbox *struct {
		Enabled                  bool `json:"enabled"`
		FailIfUnavailable        bool `json:"failIfUnavailable"`
		AutoAllowBashIfSandboxed bool `json:"autoAllowBashIfSandboxed"`
		AllowUnsandboxedCommands bool `json:"allowUnsandboxedCommands"`
		Filesystem               struct {
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
	return claude.New(config.Config{Allow: []string{"git", "make"}}, home, sfyBin), home
}

// setupSandbox gives the adapter sandbox lists and a fresh HOME that holds the allow_write folders.
func setupSandbox(t *testing.T) (provider.Provider, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	home := t.TempDir()
	cfg := config.Config{
		Allow:      []string{"git", "make"},
		DenyRead:   []string{"~/.ssh", "~/.aws"},
		AllowWrite: []string{"~/Library/Caches", "~/go/pkg/mod"},
	}
	return claude.New(cfg, home, bin), home
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
		Prompt: "Do this task.", Protected: []string{"SPEC.md", "/abs/dir"},
	}
}

func mainSpec() engine.SessionSpec {
	return engine.SessionSpec{Name: "main", Kind: "main", Dir: "/state", Model: "opus", Prompt: "Start."}
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

func TestSettingsAllowTheConfiguredCommandsAndSfy(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())

	s := readSettings(t, home, "worker-t1")

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

func TestMainSettingsKeepQuestionsAndEdits(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, mainSpec())

	s := readSettings(t, home, "main")

	if len(s.Permissions.Deny) != 0 {
		t.Errorf("main denies %v", s.Permissions.Deny)
	}
	if hookOf(t, s, "Stop").Command == "" {
		t.Error("main has no Stop hook")
	}
}

func TestArgvIsASfyRunCommandWithoutTheCleanFlag(t *testing.T) {
	p, _ := setup(t, bin)

	c := envruntest.Parse(t, command(t, p, worker()))

	if c.Bin != bin || c.Clean {
		t.Errorf("bin %q, clean %v", c.Bin, c.Clean)
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

func TestArgvEndsWithThePrompt(t *testing.T) {
	p, _ := setup(t, bin)

	argv := command(t, p, worker())

	if got := argv[len(argv)-1]; got != "Do this task." {
		t.Errorf("last element %q", got)
	}
}

func TestMainArgvOmitsTheTask(t *testing.T) {
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
	s := readSettings(t, home, "main")

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

	s := readSettings(t, home, "main")

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

func TestMainKeepsAcceptEdits(t *testing.T) {
	p, _ := setup(t, bin)

	argv := command(t, p, mainSpec())

	if got := flagValue(t, argv, "--permission-mode"); got != "acceptEdits" {
		t.Errorf("--permission-mode %q", got)
	}
}

func TestWorkerSettingsHaveNoSfyHomeReadRule(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())

	s := readSettings(t, home, "worker-t1")

	for _, a := range s.Permissions.Allow {
		if strings.HasPrefix(a, "Read(") || a == "Bash(ls *)" {
			t.Errorf("worker allows %s", a)
		}
	}
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
		if !slices.Equal(s.Sandbox.Filesystem.DenyRead, []string{"~/.ssh", "~/.aws"}) {
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

func TestMainSettingsHaveNoSandbox(t *testing.T) {
	p, home := setupSandbox(t)
	command(t, p, mainSpec())

	s := readSettings(t, home, "main")

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
