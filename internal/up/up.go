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
	"github.com/JacobJNilsson/simple-factory/internal/envrun"
	"github.com/JacobJNilsson/simple-factory/internal/launcher"
	"github.com/JacobJNilsson/simple-factory/internal/mux"
	"github.com/JacobJNilsson/simple-factory/internal/prompts"
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

// mainPrompt names the folder, so the main agent needs no command to find it.
func mainPrompt(home string) string {
	return "Your state folder is " + home + ". Read notes.md and status.md there with the Read tool. Then wait for the operator."
}

// resumePrompt is the first message of a resumed main agent. Its conversation already holds the full prompt.
func resumePrompt(home string) string {
	return "sfy started again. Read notes.md and status.md in " + home + " with the Read tool."
}

// mainPromptPath is the file that holds the prompt of the running main agent.
func mainPromptPath(home string) string { return filepath.Join(home, "main-prompt.md") }

// recordMainPrompt saves the main prompt that a build uses now, and reports whether it differs from the saved one.
// A missing file counts as a change, because the main agent may predate the file.
func recordMainPrompt(home string) (changed bool, err error) {
	text, err := prompts.For("main", home)
	if err != nil {
		return false, err
	}
	path := mainPromptPath(home)
	old, err := os.ReadFile(path)
	if err == nil && string(old) == text {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return true, os.WriteFile(path, []byte(text), 0o600)
}

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
	launcher launcher.Launcher
	// answers reports whether a sfy process answers on its socket.
	answers func() bool
	// wait limits how long run waits for the socket, and poll sets the pause between checks.
	wait, poll time.Duration
	out        io.Writer
	// warn receives warnings. They never stop up.
	warn io.Writer
	// brew runs brew with the given arguments and returns what brew printed.
	brew func(environ []string, args ...string) ([]byte, error)
	// whoami returns the address of the terminal that sfy runs in. An empty address means that the set has no whoami template.
	whoami func() (string, error)
	// exec replaces the sfy process with argv. It returns only when the replacement fails.
	exec func(argv []string) error
}

// mainAddressPath is the file that records the address of a main agent that runs in the operator's terminal.
func mainAddressPath(home string) string {
	return filepath.Join(home, "sessions", mainSession, "address")
}

// run starts what is missing: notes.md, the sfy process and the main agent.
// A main agent that is missing runs in this terminal when the set can name it.
// When the main agent already runs, run shows it again, so the operator gets it where they started sfy.
func run(o options) error {
	started, mainAlive, err := o.ensure(true)
	if err != nil {
		return err
	}
	if !mainAlive {
		return report(o.out, started)
	}
	if len(started) > 0 {
		if err := reportList(o.out, "Started", started); err != nil {
			return err
		}
	}
	if err := o.launcher.ShowMain(mainSession); err != nil {
		return err
	}
	_, err = fmt.Fprintln(o.out, "The main agent already runs and was shown.")
	return err
}

// ensure starts what is missing and returns what it started. It reports whether the main agent ran before.
// With inPlace set, a missing main agent replaces this process when the set names this terminal.
// Only the operator's own command may do that, so restart passes false.
func (o options) ensure(inPlace bool) (started []string, mainAlive bool, err error) {
	if err := os.MkdirAll(o.home, 0o700); err != nil {
		return nil, false, err
	}
	if err := ensureNotes(filepath.Join(o.home, "notes.md")); err != nil {
		return nil, false, err
	}
	serveStarted := false
	if !o.answers() {
		if err := o.startServe(); err != nil {
			return nil, false, err
		}
		serveStarted = true
		started = append(started, "the sfy process")
	}
	alive, err := o.launcher.Alive(mainSession)
	if err != nil {
		return nil, false, err
	}
	if !alive {
		if w := o.launcher.TrustWarning(o.home); w != "" {
			_, _ = fmt.Fprintln(o.warn, "sfy: warning: "+w)
		}
		if _, err := recordMainPrompt(o.home); err != nil {
			return nil, false, err
		}
		spec := engine.SessionSpec{
			Name: "main", Kind: "main", Dir: o.home, Model: o.config.Models.Main, Prompt: mainPrompt(o.home),
			ResumePrompt: resumePrompt(o.home),
		}
		if inPlace {
			placed, err := o.startInPlace(spec, started)
			if err != nil {
				return nil, false, err
			}
			if placed {
				return append(started, "the main agent (in this terminal)"), false, nil
			}
		}
		resumed, err := o.launcher.StartMain(spec)
		if err != nil {
			if serveStarted {
				return nil, false, &mainFailedError{err: err}
			}
			return nil, false, err
		}
		if resumed {
			started = append(started, "the main agent (resumed conversation)")
		} else {
			started = append(started, "the main agent (new conversation)")
		}
	}
	return started, alive, nil
}

// startInPlace turns this terminal into the main agent. It reports false when it did not, so the caller starts the main agent through the set.
// It does not when the set has no whoami template, when whoami fails or prints nothing, or when the replacement fails.
// A failed whoami goes to warn. The address is recorded before the replacement, and removed when the replacement fails.
// A real replacement never returns. A test replacement returns nil, and startInPlace then reports true.
func (o options) startInPlace(spec engine.SessionSpec, started []string) (bool, error) {
	if o.whoami == nil || o.exec == nil {
		return false, nil
	}
	address, err := o.whoami()
	if err != nil {
		_, _ = fmt.Fprintln(o.warn, "sfy: warning: whoami failed, so the main agent starts through the set: "+err.Error())
		return false, nil
	}
	if address == "" {
		return false, nil
	}
	argv, _, err := o.launcher.MainCommand(spec)
	if err != nil {
		return false, err
	}
	path := mainAddressPath(o.home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(address+"\n"), 0o600); err != nil {
		return false, err
	}
	if len(started) > 0 {
		if err := reportList(o.out, "Started", started); err != nil {
			return false, err
		}
	}
	_, _ = fmt.Fprintln(o.out, "Starting the main agent in this terminal.")
	if err := o.exec(argv); err != nil {
		_, _ = fmt.Fprintln(o.warn, "sfy: warning: could not start the main agent in this terminal, so it starts through the set: "+err.Error())
		return false, os.Remove(path)
	}
	return true, nil
}

func report(out io.Writer, started []string) error {
	if len(started) == 0 {
		_, err := fmt.Fprintln(out, "Everything already runs.")
		return err
	}
	return reportList(out, "Started", started)
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
		dir := filepath.Join(o.home, "sessions", serveSession)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		command, err := envrun.Wrap(dir, serveEnv(o.environ, o.home), true, o.bin, []string{o.bin, "serve"})
		if err != nil {
			return err
		}
		if err := o.mux.Start(serveSession, o.home, command); err != nil {
			// The env file holds secrets, and nothing will run it now.
			return errors.Join(err, envrun.Clean(dir))
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
