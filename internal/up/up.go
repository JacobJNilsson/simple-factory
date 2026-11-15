// Package up starts the sfy process and the main agent, and runs the sfy process.
package up

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/mux"
)

// ErrNotAnswering reports that the sfy process did not answer in time after it started.
var ErrNotAnswering = errors.New("the sfy process did not answer on its socket in time")

// ErrServeExited reports that the sfy process's session ended while up waited for it.
var ErrServeExited = errors.New("the sfy process exited, see sfy.log in the sfy home")

// mainFailedError reports a main agent that failed to start after the sfy process started.
type mainFailedError struct{ err error }

func (e *mainFailedError) Error() string {
	return "sfy process started, main agent failed: " + e.err.Error()
}

func (e *mainFailedError) Unwrap() error { return e.err }

// serveSession is the multiplexer session that runs the sfy process.
const serveSession = "sfy"

const mainPrompt = "Read notes.md and status.md in your folder. Then wait for the operator."

// options holds what run needs, so tests can replace each part.
type options struct {
	home   string
	config config.Config
	// bin is the absolute path of the sfy binary.
	bin string
	// environ is the environment of the host, as KEY=VALUE entries. The sfy process gets part of it.
	environ []string
	// mux runs the sfy process, and launcher starts the main agent.
	mux      mux.Mux
	launcher engine.Launcher
	// answers reports whether a sfy process answers on its socket.
	answers func() bool
	// wait limits how long run waits for the socket, and poll sets the pause between checks.
	wait, poll time.Duration
	out        io.Writer
}

// run starts what is missing: notes.md, the sfy process and the main agent.
func run(o options) error {
	if err := os.MkdirAll(o.home, 0o700); err != nil {
		return err
	}
	if err := ensureNotes(filepath.Join(o.home, "notes.md")); err != nil {
		return err
	}
	var started []string
	serveStarted := false
	if !o.answers() {
		if err := o.startServe(); err != nil {
			return err
		}
		serveStarted = true
		started = append(started, "the sfy process")
	}
	alive, err := o.launcher.Alive("main")
	if err != nil {
		return err
	}
	if !alive {
		spec := engine.SessionSpec{
			Name: "main", Kind: "main", Dir: o.home, Model: o.config.Models.Main, Prompt: mainPrompt,
		}
		if err := o.launcher.Start(spec); err != nil {
			if serveStarted {
				return &mainFailedError{err: err}
			}
			return err
		}
		started = append(started, "the main agent")
	}
	return report(o.out, started)
}

func report(out io.Writer, started []string) error {
	var err error
	switch len(started) {
	case 0:
		_, err = fmt.Fprintln(out, "Everything already runs.")
	case 1:
		_, err = fmt.Fprintf(out, "Started %s.\n", started[0])
	default:
		_, err = fmt.Fprintf(out, "Started %s and %s.\n", started[0], started[1])
	}
	return err
}

// ensureNotes creates an empty notes.md that only the operator's user can read.
func ensureNotes(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}

// startServe starts the sfy process in its session, unless the session exists, and waits for the socket.
func (o options) startServe() error {
	alive, err := o.mux.Alive(serveSession)
	if err != nil {
		return err
	}
	if !alive {
		command := append([]string{"env", "-i"}, serveEnv(o.environ, o.home)...)
		command = append(command, o.bin, "serve")
		if err := o.mux.Start(serveSession, o.home, command); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(o.wait)
	for !o.answers() {
		alive, err := o.mux.Alive(serveSession)
		if err != nil {
			return err
		}
		if !alive {
			return ErrServeExited
		}
		if time.Now().After(deadline) {
			return ErrNotAnswering
		}
		time.Sleep(o.poll)
	}
	return nil
}

// serveEnv picks the entries of environ that the sfy process may see, and sets SFY_HOME.
// It passes the identity, locale, ssh and token variables, every ANTHROPIC_, CLAUDE_, LC_ and XDG_ variable, and the proxy variables.
func serveEnv(environ []string, home string) []string {
	out := []string{"SFY_HOME=" + home}
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || value == "" || !passes(key) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func passes(key string) bool {
	switch key {
	case "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "PATH", "LANG", "SSH_AUTH_SOCK", "GH_TOKEN", "GITHUB_TOKEN":
		return true
	}
	switch strings.ToUpper(key) {
	case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY":
		return true
	}
	for _, prefix := range []string{"ANTHROPIC_", "CLAUDE_", "LC_", "XDG_"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
