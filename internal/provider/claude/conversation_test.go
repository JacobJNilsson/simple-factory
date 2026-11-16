package claude_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const recordedID = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"

func conversationFile(home string) string {
	return filepath.Join(home, "sessions", "main", "conversation-id")
}

func TestFirstMainStartCreatesAndRecordsTheConversationID(t *testing.T) {
	p, home := setup(t, bin)

	argv := command(t, p, mainSpec())

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

func TestMainStartResumesTheRecordedConversation(t *testing.T) {
	p, home := setup(t, bin)
	if err := os.MkdirAll(filepath.Dir(conversationFile(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conversationFile(home), []byte(recordedID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := mainSpec()
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

func TestMainStartIsFreshWhenTheIDFileIsMissingOrInvalid(t *testing.T) {
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
			spec := mainSpec()
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
	spec := mainSpec()
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

func TestWorkersAndReviewersNeverResume(t *testing.T) {
	p, home := setup(t, bin)
	if err := os.MkdirAll(filepath.Dir(conversationFile(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conversationFile(home), []byte(recordedID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	spec := worker()
	if p.Resumes(spec) {
		t.Error("worker resumes")
	}
	argv := command(t, p, spec)

	for _, flag := range []string{"--resume", "--session-id"} {
		if slices.Contains(argv, flag) {
			t.Errorf("worker argv has %s: %v", flag, argv)
		}
	}
}

func ptr(s string) *string { return &s }
