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

	"github.com/JacobJNilsson/simple-factory/internal/agentproc"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

var sessionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Folder is the identity data of one factory folder.
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
	// A session that starts now gets its token from the token file and needs no legacy token.
	// The old process record goes too, so it never names a process of an earlier start.
	for _, old := range []string{f.legacyPath(name), f.processPath(name)} {
		if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("identity: %w", err)
		}
	}
	return token, nil
}

func (f Folder) processPath(name string) string {
	return filepath.Join(f.Home, "sessions", name, agentproc.ProcessFile)
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

// Claim is what the CLI side knows about the caller.
// Agent is SFY_AGENT. Token is SFY_TOKEN, which only a session that an older sfy started has.
// Session is the provider's id of the conversation that runs the command, or empty outside one.
type Claim struct{ Agent, Token, Session string }

// Resolve returns the caller that a claim stands for, with its name and token set.
// A started agent names itself. A session that matches the registration is the lead. Every other claim comes from the operator.
func (f Folder) Resolve(c Claim) protocol.Caller {
	if c.Agent != "" {
		if c.Token != "" {
			return protocol.Caller{Agent: c.Agent, Token: c.Token}
		}
		if token := f.ownToken(c.Agent); token != "" {
			return protocol.Caller{Agent: c.Agent, Token: token}
		}
		// A session that started before sessions had tokens has no SFY_TOKEN, and sfy serve left it a legacy token.
		return protocol.Caller{Agent: c.Agent, Token: f.legacyToken(c.Agent, c.Session)}
	}
	if record, ok := registration.Read(f.Home); ok && c.Session != "" && record.ID == c.Session {
		token := c.Token
		if token == "" {
			token = f.Token(protocol.Lead)
		}
		return protocol.Caller{Agent: protocol.Lead, Token: token}
	}
	return protocol.Caller{Token: c.Token}
}

// ownToken returns the token of the named session when its process record names the caller or one of its ancestors.
// It never reads a token by the name alone, so a worker that names another session gets an empty string.
func (f Folder) ownToken(name string) string {
	name = protocol.AgentName(name)
	if !sessionName.MatchString(name) {
		return ""
	}
	r, ok, err := agentproc.Read(f.processPath(name))
	if err != nil || !ok {
		return ""
	}
	if runs, err := agentproc.Runs(r); err != nil || !runs {
		return ""
	}
	return f.Token(name)
}

// legacyToken returns the legacy token of a session that started before sessions had tokens, or an empty string.
// The lead must also name its own conversation id, so a worker that sets SFY_AGENT=lead or SFY_AGENT=main gets no token.
func (f Folder) legacyToken(name, sessionID string) string {
	name = protocol.AgentName(name)
	if !sessionName.MatchString(name) {
		return ""
	}
	if name == protocol.Lead {
		recorded := readFile(filepath.Join(f.Home, "sessions", protocol.Lead, "conversation-id"))
		if recorded == "" || sessionID != recorded {
			return ""
		}
	}
	return readFile(f.legacyPath(name))
}

var (
	errMissingToken = errors.New("missing session token. Run sfy from the session that sfy started for this name. A session that started before sessions had tokens needs a restart, or run sfy restart")
	errBadToken     = errors.New("bad session token")
)

// Authenticate returns the name that the server takes as the caller of a request.
// A named caller needs the token of that session. A request with neither name nor token comes from the operator and gets an empty name.
func (f Folder) Authenticate(c protocol.Caller) (string, error) {
	if c.Token == "" {
		if c.Agent != "" {
			return "", errMissingToken
		}
		return "", nil
	}
	name, ok := f.owner(c.Token)
	// A lead that started before the rename still claims the old name, and the token of the lead proves it.
	if !ok || (c.Agent != "" && protocol.AgentName(c.Agent) != name) {
		return "", errBadToken
	}
	return name, nil
}

// owner returns the session that owns token. It reports false for an empty or unknown token.
// It skips links, because the link of an old folder name to its new folder must not make one token match two names.
func (f Folder) owner(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	entries, err := os.ReadDir(filepath.Join(f.Home, "sessions"))
	if err != nil {
		return "", false
	}
	found := ""
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
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
