// Package identity issues the secret tokens of sessions and tells who runs an sfy command.
package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var sessionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Folder is the identity data of one state folder.
type Folder struct{ Home string }

func (f Folder) tokenPath(name string) string {
	return filepath.Join(f.Home, "sessions", name, "token")
}

func (f Folder) legacyPath(name string) string {
	return filepath.Join(f.Home, "sessions", name, "legacy-token")
}

// Issue writes a new random token for the session to a 0600 file and returns it.
// A session sends the token with every command, so sfy knows the caller no matter what the caller's environment says.
func (f Folder) Issue(name string) (string, error) {
	if !sessionName.MatchString(name) {
		return "", fmt.Errorf("identity: bad session name %q", name)
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	path := f.tokenPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("identity: %w", err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("identity: %w", err)
	}
	// A session that starts now has the token in its environment and needs no legacy token.
	if err := os.Remove(f.legacyPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("identity: %w", err)
	}
	return token, nil
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("identity: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// IssueLegacy gives each session folder that has no token a legacy token, with mode 0600.
// Such a session started under an older sfy and has no SFY_TOKEN, so its folder is the only place where it can learn a token.
func (f Folder) IssueLegacy() error {
	entries, err := os.ReadDir(filepath.Join(f.Home, "sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !sessionName.MatchString(name) || f.Token(name) != "" || readFile(f.legacyPath(name)) != "" {
			continue
		}
		token, err := randomToken()
		if err != nil {
			return err
		}
		if err := os.WriteFile(f.legacyPath(name), []byte(token+"\n"), 0o600); err != nil {
			return fmt.Errorf("identity: %w", err)
		}
	}
	return nil
}

// Token returns the token of a session, or an empty string when the session has none.
func (f Folder) Token(name string) string {
	if !sessionName.MatchString(name) {
		return ""
	}
	return readFile(f.tokenPath(name))
}

// LegacyToken returns the legacy token of a session that started before sessions had tokens, or an empty string.
// The main agent must also name its own conversation id, so a worker that sets SFY_AGENT=main gets no token.
func (f Folder) LegacyToken(name, sessionID string) string {
	if !sessionName.MatchString(name) {
		return ""
	}
	if name == "main" {
		recorded := readFile(filepath.Join(f.Home, "sessions", "main", "conversation-id"))
		if recorded == "" || sessionID != recorded {
			return ""
		}
	}
	return readFile(f.legacyPath(name))
}

// Owner returns the session that owns token. It reports false for an empty or unknown token.
func (f Folder) Owner(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	entries, err := os.ReadDir(filepath.Join(f.Home, "sessions"))
	if err != nil {
		return "", false
	}
	found := ""
	for _, e := range entries {
		for _, stored := range []string{f.Token(e.Name()), readFile(f.legacyPath(e.Name()))} {
			if stored != "" && subtle.ConstantTimeCompare([]byte(stored), []byte(token)) == 1 {
				found = e.Name()
			}
		}
	}
	return found, found != ""
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
