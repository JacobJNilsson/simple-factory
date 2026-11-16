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

// conversationIDPath is the file that records the id of the main agent's conversation.
func (a adapter) conversationIDPath() string {
	return filepath.Join(a.home, "sessions", "main", "conversation-id")
}

var conversationID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// recordedConversation returns the recorded id of the main conversation, or false when the file is missing or holds no id.
func (a adapter) recordedConversation() (string, bool) {
	b, err := os.ReadFile(a.conversationIDPath())
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(string(b))
	return id, conversationID.MatchString(id)
}

// Resumes reports whether Command resumes the recorded conversation of the main agent.
func (a adapter) Resumes(spec engine.SessionSpec) bool {
	if spec.Kind != "main" || spec.Fresh {
		return false
	}
	_, ok := a.recordedConversation()
	return ok
}

// mainConversation returns the flags that continue the recorded conversation, or start and record a new one.
// A resumed conversation must run in the same folder, and the main agent always starts in the state folder.
func (a adapter) mainConversation(spec engine.SessionSpec) (flags []string, resumed bool, err error) {
	if a.Resumes(spec) {
		id, _ := a.recordedConversation()
		return []string{"--resume", id}, true, nil
	}
	id, err := newUUID()
	if err != nil {
		return nil, false, fmt.Errorf("claude: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(a.conversationIDPath()), 0o700); err != nil {
		return nil, false, fmt.Errorf("claude: %w", err)
	}
	if err := os.WriteFile(a.conversationIDPath(), []byte(id+"\n"), 0o600); err != nil {
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
