package claude_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
)

const recordedID = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"

func conversationFile(home string) string {
	return filepath.Join(home, "sessions", "lead", "conversation-id")
}

func TestFirstLeadStartCreatesAndRecordsTheConversationID(t *testing.T) {
	p, home := setup(t, bin)

	argv := command(t, p, leadSpec())

	id := flagValue(t, argv, "--session-id")
	if slices.Contains(argv, "--resume") {
		t.Errorf("first start resumes: %v", argv)
	}
	b, err := os.ReadFile(conversationFile(home))
	if err != nil || strings.TrimSpace(string(b)) != id {
		t.Errorf("recorded %q, %v, want %q", b, err, id)
	}
	if info, err := os.Stat(conversationFile(home)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("id file mode: %v %v", info, err)
	}
	if got := argv[len(argv)-1]; got != "Start." {
		t.Errorf("prompt %q, want the full first prompt", got)
	}
}

func TestLeadStartResumesTheRecordedConversation(t *testing.T) {
	p, home := setup(t, bin)
	if err := os.MkdirAll(filepath.Dir(conversationFile(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conversationFile(home), []byte(recordedID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := leadSpec()
	spec.ResumePrompt = "sfy started again."

	if !p.Resumes(spec) {
		t.Fatal("Resumes = false, want true")
	}
	argv := command(t, p, spec)

	if got := flagValue(t, argv, "--resume"); got != recordedID {
		t.Errorf("resume id %q", got)
	}
	if slices.Contains(argv, "--session-id") {
		t.Errorf("resume also sets a session id: %v", argv)
	}
	if got := argv[len(argv)-1]; got != "sfy started again." {
		t.Errorf("prompt %q, want the short note", got)
	}
	for _, flag := range []string{"--settings", "--append-system-prompt", "--model", "--name"} {
		flagValue(t, argv, flag)
	}
}

func TestLeadStartIsFreshWhenTheIDFileIsMissingOrInvalid(t *testing.T) {
	for name, content := range map[string]*string{"missing": nil, "invalid": ptr("not-a-uuid\n"), "empty": ptr("")} {
		t.Run(name, func(t *testing.T) {
			p, home := setup(t, bin)
			if content != nil {
				if err := os.MkdirAll(filepath.Dir(conversationFile(home)), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(conversationFile(home), []byte(*content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			spec := leadSpec()
			spec.ResumePrompt = "sfy started again."

			if p.Resumes(spec) {
				t.Error("Resumes = true")
			}
			argv := command(t, p, spec)

			id := flagValue(t, argv, "--session-id")
			if slices.Contains(argv, "--resume") || id == "" {
				t.Errorf("argv %v", argv)
			}
			if got := argv[len(argv)-1]; got != "Start." {
				t.Errorf("prompt %q, want the full first prompt", got)
			}
		})
	}
}

func TestFreshSpecReplacesTheRecordedConversation(t *testing.T) {
	p, home := setup(t, bin)
	if err := os.MkdirAll(filepath.Dir(conversationFile(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conversationFile(home), []byte(recordedID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := leadSpec()
	spec.Fresh = true

	if p.Resumes(spec) {
		t.Error("Resumes = true for a fresh spec")
	}
	argv := command(t, p, spec)

	id := flagValue(t, argv, "--session-id")
	if id == recordedID {
		t.Error("fresh start reused the old id")
	}
	b, _ := os.ReadFile(conversationFile(home))
	if strings.TrimSpace(string(b)) != id {
		t.Errorf("recorded %q, want %q", b, id)
	}
}

// sessionConversationFile is the id file of any session.
func sessionConversationFile(home, name string) string {
	return filepath.Join(home, "sessions", name, "conversation-id")
}

func TestWorkerAndReviewerStartsRecordANewConversation(t *testing.T) {
	for _, spec := range []engine.SessionSpec{worker(), reviewerSpec()} {
		t.Run(spec.Kind, func(t *testing.T) {
			p, home := setup(t, bin)
			argv := command(t, p, spec)

			id := flagValue(t, argv, "--session-id")
			if slices.Contains(argv, "--resume") {
				t.Errorf("first start resumes: %v", argv)
			}
			b, err := os.ReadFile(sessionConversationFile(home, spec.Name))
			if err != nil || strings.TrimSpace(string(b)) != id {
				t.Errorf("recorded %q, %v, want %q", b, err, id)
			}
			if got := argv[len(argv)-1]; got != spec.Prompt {
				t.Errorf("prompt %q, want %q", got, spec.Prompt)
			}
		})
	}
}

func TestWorkerStartWithoutResumeReplacesTheRecordedConversation(t *testing.T) {
	p, home := setup(t, bin)
	writeConversation(t, sessionConversationFile(home, "worker-t1"))

	spec := worker()
	spec.ResumePrompt = "sfy woke this session."
	if p.Resumes(spec) {
		t.Error("worker resumes")
	}
	argv := command(t, p, spec)

	if id := flagValue(t, argv, "--session-id"); id == recordedID {
		t.Error("new start reused the old id")
	}
	if slices.Contains(argv, "--resume") {
		t.Errorf("worker argv resumes: %v", argv)
	}
}

func TestTheLeadIDDoesNotLeakToAWorker(t *testing.T) {
	p, home := setup(t, bin)
	writeConversation(t, conversationFile(home))

	argv := command(t, p, worker())

	if id := flagValue(t, argv, "--session-id"); id == recordedID {
		t.Error("worker took the lead's id")
	}
	b, _ := os.ReadFile(conversationFile(home))
	if strings.TrimSpace(string(b)) != recordedID {
		t.Errorf("lead id changed to %q", b)
	}
}

func writeConversation(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(recordedID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func ptr(s string) *string { return &s }
