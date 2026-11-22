package identity

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

const mainConversation = "11111111-2222-4333-8444-555555555555"

// preTokenHome returns a factory folder with the session folders that an sfy without tokens left behind.
func preTokenHome(t *testing.T) Folder {
	t.Helper()
	home := t.TempDir()
	for _, name := range []string{"lead", "worker-t1", "worker-t2"} {
		if err := os.MkdirAll(filepath.Join(home, "sessions", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	id := filepath.Join(home, "sessions", "lead", "conversation-id")
	if err := os.WriteFile(id, []byte(mainConversation+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Folder{Home: home}
}

func TestIssueWritesAPrivateTokenForEachSession(t *testing.T) {
	f := Folder{Home: t.TempDir()}

	a, err := f.Issue("worker-t1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Issue("review-t1-1")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(f.tokenPath("worker-t1"))
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %v", info.Mode().Perm())
	}
	if len(a) < 32 || a == b {
		t.Errorf("tokens %q and %q are not distinct secrets", a, b)
	}
	if got := f.Token("worker-t1"); got != a {
		t.Errorf("Token = %q, want %q", got, a)
	}
}

func TestIssueRemovesTheLegacyTokenOfTheSession(t *testing.T) {
	f := preTokenHome(t)
	if err := f.IssueLegacy(); err != nil {
		t.Fatal(err)
	}

	if _, err := f.Issue("worker-t1"); err != nil {
		t.Fatal(err)
	}

	if got := f.legacyToken("worker-t1", ""); got != "" {
		t.Errorf("a session with a token kept the legacy token %q", got)
	}
}

func TestIssueRefusesABadSessionName(t *testing.T) {
	f := Folder{Home: t.TempDir()}

	if _, err := f.Issue("../x"); err == nil {
		t.Fatal("a name that leaves the sessions folder was accepted")
	}
}

func TestIssueLegacySkipsSessionsThatHaveATokenOrALegacyToken(t *testing.T) {
	f := preTokenHome(t)
	if _, err := f.Issue("worker-t1"); err != nil {
		t.Fatal(err)
	}
	if err := f.IssueLegacy(); err != nil {
		t.Fatal(err)
	}
	t2 := f.legacyToken("worker-t2", "")

	if err := f.IssueLegacy(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(f.legacyPath("worker-t1")); err == nil {
		t.Error("a session with a token got a legacy token")
	}
	if t2 == "" || f.legacyToken("worker-t2", "") != t2 {
		t.Errorf("legacy token changed from %q to %q", t2, f.legacyToken("worker-t2", ""))
	}
}

func TestIssueLegacyWithoutASessionsFolderIsNoError(t *testing.T) {
	if err := (Folder{Home: t.TempDir()}).IssueLegacy(); err != nil {
		t.Fatal(err)
	}
}

func TestALegacyTokenOpensOnlyItsOwnSession(t *testing.T) {
	f := preTokenHome(t)
	if err := f.IssueLegacy(); err != nil {
		t.Fatal(err)
	}
	t1 := f.legacyToken("worker-t1", "")
	t2 := f.legacyToken("worker-t2", "")

	if t1 == "" || t1 == t2 {
		t.Fatalf("tokens %q and %q are not distinct", t1, t2)
	}
	if got, err := f.Authenticate(protocol.Caller{Token: t1}); err != nil || got != "worker-t1" {
		t.Errorf("Authenticate = %q, %v", got, err)
	}
	if _, err := f.Authenticate(protocol.Caller{Agent: "worker-t2", Token: t1}); err == nil {
		t.Error("a legacy token opened another session")
	}
}

func authFolder(t *testing.T) Folder {
	t.Helper()
	f := Folder{Home: t.TempDir()}
	for _, name := range []string{"lead", "worker-t1"} {
		if _, err := f.Issue(name); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestAuthenticateRefusesAWorkerThatClaimsToBeTheLead(t *testing.T) {
	f := authFolder(t)

	for _, name := range []string{"lead", "main"} {
		_, err := f.Authenticate(protocol.Caller{Agent: name, Token: f.Token("worker-t1")})

		if !errors.Is(err, errBadToken) {
			t.Fatalf("%s: got %v", name, err)
		}
	}
}

func TestAuthenticateAcceptsTheOldNameOnlyForTheTokenOfTheLead(t *testing.T) {
	f := authFolder(t)

	got, err := f.Authenticate(protocol.Caller{Agent: "main", Token: f.Token("lead")})

	if err != nil || got != "lead" {
		t.Fatalf("got %q, %v, want lead", got, err)
	}
}

func TestOwnerSkipsTheLinkOfTheOldFolderName(t *testing.T) {
	f := authFolder(t)
	if err := os.Symlink("lead", filepath.Join(f.Home, "sessions", "main")); err != nil {
		t.Fatal(err)
	}

	for _, c := range []protocol.Caller{
		{Token: f.Token("lead")},
		{Agent: "lead", Token: f.Token("lead")},
		{Agent: "main", Token: f.Token("lead")},
	} {
		if got, err := f.Authenticate(c); err != nil || got != "lead" {
			t.Errorf("%+v: got %q, %v, want lead", c, got, err)
		}
	}
}

func TestAuthenticateRefusesANameWithoutAToken(t *testing.T) {
	f := authFolder(t)

	_, err := f.Authenticate(protocol.Caller{Agent: "worker-t1"})

	if !errors.Is(err, errMissingToken) {
		t.Fatalf("got %v", err)
	}
}

func TestAuthenticateRefusesATokenThatNoSessionOwns(t *testing.T) {
	f := authFolder(t)

	if _, err := f.Authenticate(protocol.Caller{Agent: "worker-t1", Token: "guess"}); err == nil {
		t.Fatal("an unknown token was accepted")
	}
}

func TestAuthenticateAcceptsTheOwnerAndTheOperator(t *testing.T) {
	f := authFolder(t)

	for name, c := range map[string]struct {
		caller protocol.Caller
		want   string
	}{
		"worker":                {protocol.Caller{Agent: "worker-t1", Token: f.Token("worker-t1")}, "worker-t1"},
		"lead":                  {protocol.Caller{Agent: "lead", Token: f.Token("lead")}, "lead"},
		"token without a name":  {protocol.Caller{Token: f.Token("lead")}, "lead"},
		"operator with no name": {protocol.Caller{}, ""},
	} {
		if got, err := f.Authenticate(c.caller); err != nil || got != c.want {
			t.Errorf("%s: got %q, %v, want %q", name, got, err, c.want)
		}
	}
}

func TestResolveAndAuthenticateAgree(t *testing.T) {
	started := authFolder(t)
	registeredMain := authFolder(t)
	registered(t, registeredMain)
	preToken := preTokenHome(t)
	if err := preToken.IssueLegacy(); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]struct {
		folder Folder
		claim  Claim
		want   string
	}{
		"started worker":  {started, Claim{Agent: "worker-t1", Token: started.Token("worker-t1")}, "worker-t1"},
		"registered lead": {registeredMain, Claim{Session: "s-main"}, "lead"},
		"pre-token lead":  {preToken, Claim{Agent: "lead", Session: mainConversation}, "lead"},
		"pre-token main":  {preToken, Claim{Agent: "main", Session: mainConversation}, "lead"},
		"pre-token agent": {preToken, Claim{Agent: "worker-t2"}, "worker-t2"},
		"operator":        {started, Claim{}, ""},
	} {
		got, err := c.folder.Authenticate(c.folder.Resolve(c.claim))
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v, want %q", name, got, err, c.want)
		}
	}
}

func registered(t *testing.T, f Folder) {
	t.Helper()
	if err := registration.Write(f.Home, registration.Record{ID: "s-main"}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveKeepsTheNameAndTokenOfAStartedAgent(t *testing.T) {
	f := Folder{Home: t.TempDir()}

	got := f.Resolve(Claim{Agent: "worker-t1", Token: "secret", Session: "s-main"})

	if want := (protocol.Caller{Agent: "worker-t1", Token: "secret"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestResolveMapsTheRegisteredSessionToMainWithItsToken(t *testing.T) {
	f := Folder{Home: t.TempDir()}
	registered(t, f)
	token, err := f.Issue("lead")
	if err != nil {
		t.Fatal(err)
	}

	got := f.Resolve(Claim{Session: "s-main"})

	if want := (protocol.Caller{Agent: "lead", Token: token}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestResolveLetsTheNameOfAStartedAgentWinOverTheRegistration(t *testing.T) {
	f := Folder{Home: t.TempDir()}
	registered(t, f)

	got := f.Resolve(Claim{Agent: "worker-t1", Token: "secret", Session: "s-main"})

	if got.Agent != "worker-t1" {
		t.Errorf("got %q", got.Agent)
	}
}

func TestResolveLeavesOtherSessionsAsTheOperator(t *testing.T) {
	f := Folder{Home: t.TempDir()}
	registered(t, f)
	none := Folder{Home: t.TempDir()}

	for name, got := range map[string]protocol.Caller{
		"another session":     f.Resolve(Claim{Session: "s-other"}),
		"no registration":     none.Resolve(Claim{Session: "s-main"}),
		"outside any session": f.Resolve(Claim{}),
	} {
		if got.Agent != "" || got.Token != "" {
			t.Errorf("%s got %+v", name, got)
		}
	}
}

func TestResolveGivesAPreTokenWorkerItsLegacyToken(t *testing.T) {
	f := preTokenHome(t)
	if err := f.IssueLegacy(); err != nil {
		t.Fatal(err)
	}

	got := f.Resolve(Claim{Agent: "worker-t1"})

	if got.Token == "" || got.Token != f.legacyToken("worker-t1", "") {
		t.Errorf("got %+v", got)
	}
}

func TestResolveGivesAPreTokenMainItsLegacyTokenOnlyInItsOwnConversation(t *testing.T) {
	f := preTokenHome(t)
	if err := f.IssueLegacy(); err != nil {
		t.Fatal(err)
	}

	own := f.Resolve(Claim{Agent: "main", Session: mainConversation})
	spoof := f.Resolve(Claim{Agent: "main", Session: "worker-session"})

	if own.Token == "" {
		t.Error("main got no token in its own conversation")
	}
	if spoof.Token != "" {
		t.Errorf("a worker that claims main got the token %q", spoof.Token)
	}
}
