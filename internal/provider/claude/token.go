package claude

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
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
	// A session that starts now has the token in its environment and needs no legacy token.
	if err := os.Remove(LegacyTokenPath(home, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("claude: %w", err)
	}
	return token, nil
}

// LegacyTokenPath returns the file that holds the token of a session that started before sessions had tokens.
func LegacyTokenPath(home, name string) string {
	return filepath.Join(home, "sessions", name, "legacy-token")
}

// IssueLegacyTokens gives each session folder that has no token a legacy token, with mode 0600.
// Such a session started under an older sfy and has no SFY_TOKEN, so its folder is the only place where it can learn a token.
func IssueLegacyTokens(home string) error {
	entries, err := os.ReadDir(filepath.Join(home, "sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !sessionName.MatchString(name) || ReadToken(home, name) != "" || readFile(LegacyTokenPath(home, name)) != "" {
			continue
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("claude: %w", err)
		}
		if err := os.WriteFile(LegacyTokenPath(home, name), []byte(hex.EncodeToString(raw)+"\n"), 0o600); err != nil {
			return fmt.Errorf("claude: %w", err)
		}
	}
	return nil
}

// ReadLegacyToken returns the legacy token of a session that started before sessions had tokens, or an empty string.
// The main agent must also name its own conversation id, so a worker that sets SFY_AGENT=main gets no token.
func ReadLegacyToken(home, name, sessionID string) string {
	if !sessionName.MatchString(name) {
		return ""
	}
	if name == "main" {
		recorded := readFile(filepath.Join(home, "sessions", "main", "conversation-id"))
		if recorded == "" || sessionID != recorded {
			return ""
		}
	}
	return readFile(LegacyTokenPath(home, name))
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ReadToken returns the token of a session, or an empty string when the session has none.
func ReadToken(home, name string) string {
	if !sessionName.MatchString(name) {
		return ""
	}
	return readFile(TokenPath(home, name))
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
		for _, stored := range []string{ReadToken(home, e.Name()), readFile(LegacyTokenPath(home, e.Name()))} {
			if stored != "" && subtle.ConstantTimeCompare([]byte(stored), []byte(token)) == 1 {
				found = e.Name()
			}
		}
	}
	return found, found != ""
}
