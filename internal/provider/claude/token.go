package claude

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TokenPath returns the file that holds the secret token of a session.
func TokenPath(home, name string) string {
	return filepath.Join(home, "sessions", name, "token")
}

// IssueToken writes a new random token for the session to a 0600 file and returns it.
// A session sends the token with every command, so sfy knows the caller no matter what the caller's environment says.
func IssueToken(home, name string) (string, error) {
	if !sessionName.MatchString(name) {
		return "", fmt.Errorf("claude: bad session name %q", name)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	token := hex.EncodeToString(raw)
	path := TokenPath(home, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	return token, nil
}

// ReadToken returns the token of a session, or an empty string when the session has none.
func ReadToken(home, name string) string {
	if !sessionName.MatchString(name) {
		return ""
	}
	b, err := os.ReadFile(TokenPath(home, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// AgentForToken returns the session that owns token. It reports false for an empty or unknown token.
func AgentForToken(home, token string) (string, bool) {
	if token == "" {
		return "", false
	}
	entries, err := os.ReadDir(filepath.Join(home, "sessions"))
	if err != nil {
		return "", false
	}
	found := ""
	for _, e := range entries {
		stored := ReadToken(home, e.Name())
		if stored != "" && subtle.ConstantTimeCompare([]byte(stored), []byte(token)) == 1 {
			found = e.Name()
		}
	}
	return found, found != ""
}
