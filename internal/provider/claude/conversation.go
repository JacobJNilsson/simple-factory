package claude

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
)

// conversationIDPath is the file that records the id of the named session's conversation.
func (a adapter) conversationIDPath(name string) string {
	return filepath.Join(a.home, "sessions", name, "conversation-id")
}

// NoConversationError reports a resume of a session that has no recorded conversation.
type NoConversationError struct{ Name string }

func (e *NoConversationError) Error() string {
	return fmt.Sprintf("claude: %s has no recorded conversation", e.Name)
}

var conversationID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// recordedConversation returns the recorded id of the named session's conversation, or false when the file is missing or holds no id.
func (a adapter) recordedConversation(name string) (string, bool) {
	b, err := os.ReadFile(a.conversationIDPath(name))
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(string(b))
	return id, conversationID.MatchString(id)
}

// Resumes reports whether Command resumes the recorded conversation of the session.
// The lead resumes unless the spec is fresh. A worker or a reviewer resumes only for a spec with Resume.
func (a adapter) Resumes(spec engine.SessionSpec) bool {
	wants := spec.Resume
	if spec.Kind == "lead" {
		wants = !spec.Fresh
	}
	if !wants {
		return false
	}
	_, ok := a.recordedConversation(spec.Name)
	return ok
}

// conversation returns the flags that continue the recorded conversation, or start and record a new one.
// A resumed conversation must run in the same folder as its first start.
func (a adapter) conversation(spec engine.SessionSpec) (flags []string, resumed bool, err error) {
	if a.Resumes(spec) {
		id, _ := a.recordedConversation(spec.Name)
		return []string{"--resume", id}, true, nil
	}
	id, err := newUUID()
	if err != nil {
		return nil, false, fmt.Errorf("claude: %w", err)
	}
	path := a.conversationIDPath(spec.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, fmt.Errorf("claude: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return nil, false, fmt.Errorf("claude: %w", err)
	}
	return []string{"--session-id", id}, false, nil
}

// newUUID returns a random version 4 UUID in lower case.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
