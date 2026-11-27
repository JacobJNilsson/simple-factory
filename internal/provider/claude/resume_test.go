package claude_test

import (
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
)

// resumeSpec is the spec of a wake of the worker.
func resumeSpec() engine.SessionSpec {
	spec := worker()
	spec.Resume, spec.ResumePrompt = true, "sfy woke this session."
	return spec
}

func TestWorkerResumeContinuesTheRecordedConversation(t *testing.T) {
	p, home := setup(t, bin)
	first := command(t, p, worker())
	id := flagValue(t, first, "--session-id")

	if !p.Resumes(resumeSpec()) {
		t.Fatal("Resumes = false, want true")
	}
	argv := command(t, p, resumeSpec())

	if got := flagValue(t, argv, "--resume"); got != id {
		t.Errorf("resume id %q, want %q", got, id)
	}
	if slices.Contains(argv, "--session-id") {
		t.Errorf("resume also sets a session id: %v", argv)
	}
	if got := argv[len(argv)-1]; got != "sfy woke this session." {
		t.Errorf("prompt %q, want the wake note", got)
	}
	b, _ := os.ReadFile(sessionConversationFile(home, "worker-t1"))
	if strings.TrimSpace(string(b)) != id {
		t.Errorf("resume changed the recorded id to %q", b)
	}
}

func TestWorkerResumeWritesTheSameSettingsAsTheFirstStart(t *testing.T) {
	p, home := setupSandbox(t)
	first := command(t, p, worker())
	before, err := os.ReadFile(settingsPath(home, "worker-t1"))
	if err != nil {
		t.Fatal(err)
	}

	argv := command(t, p, resumeSpec())

	after, err := os.ReadFile(settingsPath(home, "worker-t1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("settings changed on resume:\nbefore %s\nafter  %s", before, after)
	}
	// The equal bytes carry the sandbox block too, so the check of one key shows that the file is a worker's.
	deny := readSettings(t, home, "worker-t1").Permissions.DenyInsteadOfPrompting
	if deny == nil || !*deny {
		t.Error("resumed worker lacks denyInsteadOfPrompting")
	}
	for _, a := range [][]string{first, argv} {
		if got := flagValue(t, a, "--permission-mode"); got != "acceptEdits" {
			t.Errorf("permission mode %q", got)
		}
	}
}

func TestWorkerResumeIssuesANewToken(t *testing.T) {
	p, home := setup(t, bin)
	command(t, p, worker())
	f := identity.Folder{Home: home}
	old := f.Token("worker-t1")

	command(t, p, resumeSpec())

	if _, err := f.Authenticate(protocol.Caller{Agent: "worker-t1", Token: old}); err == nil {
		t.Error("the old token still works")
	}
	if got, err := f.Authenticate(protocol.Caller{Agent: "worker-t1", Token: f.Token("worker-t1")}); err != nil || got != "worker-t1" {
		t.Errorf("the new token names %q, %v", got, err)
	}
}

func TestResumeWithoutARecordedConversationFails(t *testing.T) {
	p, home := setup(t, bin)

	if p.Resumes(resumeSpec()) {
		t.Error("Resumes = true with no recorded id")
	}
	_, err := p.Command(resumeSpec())

	var none *claude.NoConversationError
	if !errors.As(err, &none) || none.Name != "worker-t1" {
		t.Errorf("err = %v, want a NoConversationError", err)
	}
	if _, err := os.Stat(settingsPath(home, "worker-t1")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a failed resume wrote settings: %v", err)
	}
}

func TestWorkerWithoutResumeNeverResumes(t *testing.T) {
	p, home := setup(t, bin)
	writeConversation(t, sessionConversationFile(home, "worker-t1"))

	if p.Resumes(worker()) {
		t.Error("Resumes = true without Resume")
	}
}
