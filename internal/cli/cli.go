// Package cli turns sfy's command line into a request for the sfy process.
package cli

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude/hookcmd"
	"github.com/JacobJNilsson/simple-factory/internal/server"
)

// Exit codes of Run.
const (
	exitOK         = 0
	exitFailed     = 1
	exitUsage      = 2
	exitNotRunning = 3
)

const usage = `usage:
  sfy                  start sfy (operator only)
  sfy up [--headless] [--new-factory]
                       same as sfy (operator only). --headless starts only the sfy process, detached.
                       --new-factory makes a factory where the folder has factories of other name prefixes only.
  sfy register         make the running agent session the lead (not for started agents)
  sfy hooks install    add sfy's hooks to the user's Claude Code settings (operator only)
  sfy config           print the factory's merged settings and prompt files, with the file behind each
  sfy label [<text>]   show the factory as <text> in sfy status and sfy factories, with no text remove the label
                       (operator and lead). A label is for display only and never addresses a factory.
  sfy factories        list each factory's name, label and working folder (operator and lead)
  sfy help
  sfy task new [--factory <factory>] <file|->
  sfy task list [--archived]
  sfy task show <id>
  sfy task edit <id> <file>
  sfy task queue <id>
  sfy task cancel <id>
  sfy task resume <id>
  sfy task feedback <id> <text...>
  sfy task complete <id>
  sfy task integrate <id>
  sfy task release <id>
  sfy task push <id>   the old name of sfy task release
  sfy task archive <id>
  sfy send <agent> <text...>
  sfy report done|blocked|progress|pushed <text...>
  sfy verdict pass|fail <text...|->
  sfy inbox
  sfy escalate <id> <text...>
  sfy unstick <agent>  press Escape at a worker's or reviewer's permission prompt (lead only)
  sfy status
  sfy down             stop all agents and the sfy process (operator only)
  sfy restart [--update] [--headless]
                       restart the sfy process and keep the agents running (--update upgrades sfy first,
                       --headless starts the new process detached and no lead)
  sfy hook <event>
  sfy hook note        print the delivered notes to stderr and exit 2 (a hook of the session's settings)
  sfy hook note-watch  print the SessionStart output that names the note file (a hook of the session's settings)
  sfy hook merge-guard block a hand push, merge or rebase in a no-merge repo (a hook of the lead's settings)
  sfy hook --user <event>

settings:
  ~/.sfy/config.toml holds global keys, such as headless = true, max_workers and check_timeout.
  sfy config prints the merged settings and the file behind each key.
  README.md and docs/configuration.md describe every key:
  https://github.com/JacobJNilsson/simple-factory/blob/main/docs/configuration.md

factories:
  sfy runs the factory of the current folder. SFY_HOME=<folder> picks a factory folder by hand.
  A wrapper command sets SFY_NAME_PREFIX, such as ex-. It reaches its own factory, not the one of plain sfy.
  Run the wrapper's command to reach its factory. The first line of sfy config names the factory.
`

// PrintUsage writes the usage text to w.
func PrintUsage(w io.Writer) {
	_, _ = io.WriteString(w, usage)
}

// Misuse prints msg and the usage to stderr and returns the usage exit code.
func Misuse(stderr io.Writer, msg string) int {
	_, _ = fmt.Fprintf(stderr, "sfy: %s\n%s", msg, usage)
	return exitUsage
}

// usageError reports a command line that matches none of the forms.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// form describes the arguments after the command words.
type form struct {
	// words is the number of words in the command name.
	words int
	// ids is the number of fixed arguments after the command name.
	ids int
	// file is true when the last fixed argument is a file that becomes the Body.
	file bool
	// flags lists the flags that may follow the fixed arguments.
	flags []string
	// option is a flag that takes one value and may come before the fixed arguments.
	option string
	// text is true when the rest of the line is free text.
	text bool
	// verdict is true when the rest of the line is the Body text and "-" reads it from standard input.
	verdict bool
}

var forms = map[string]form{
	"task new":        {words: 2, ids: 1, file: true, option: "--factory"},
	"task list":       {words: 2, flags: []string{"--archived"}},
	"task show":       {words: 2, ids: 1},
	"task edit":       {words: 2, ids: 2, file: true},
	"task queue":      {words: 2, ids: 1},
	"task cancel":     {words: 2, ids: 1},
	"task resume":     {words: 2, ids: 1},
	"task feedback":   {words: 2, ids: 1, text: true},
	"task complete":   {words: 2, ids: 1},
	"task integrate":  {words: 2, ids: 1},
	"task release":    {words: 2, ids: 1},
	"task push":       {words: 2, ids: 1},
	"task archive":    {words: 2, ids: 1},
	"send":            {words: 1, ids: 1, text: true},
	"report done":     {words: 2, text: true},
	"report blocked":  {words: 2, text: true},
	"report progress": {words: 2, text: true},
	"report pushed":   {words: 2, text: true},
	"verdict pass":    {words: 2, verdict: true},
	"verdict fail":    {words: 2, verdict: true},
	"inbox":           {words: 1},
	"escalate":        {words: 1, ids: 1, text: true},
	"unstick":         {words: 1, ids: 1},
	"status":          {words: 1},
	"down":            {words: 1},
}

// Agent returns the session name of the caller. A started agent has it in SFY_AGENT.
// A registered agent has no such variable, so the provider names its session, and the registered id maps it to the lead.
// The operator and every other session get an empty name.
func Agent(env func(string) string) string {
	return caller(env).Agent
}

// caller returns the caller that the environment describes, with the secret token that proves its identity to the sfy process.
func caller(env func(string) string) protocol.Caller {
	c := identity.Folder{Home: config.Home(env)}.Resolve(identity.Claim{Agent: env("SFY_AGENT"), Token: env("SFY_TOKEN"), Session: claude.CurrentSession(env)})
	c.Task = env("SFY_TASK")
	return c
}

// Restricted reports whether SFY_AGENT names a session that sfy started for a task, such as worker-t4, review-t4-2 or loader-t4.
// Such a session may run only the sfy commands that its allow rules name. The lead is the one started session that is not restricted.
func Restricted(env func(string) string) bool {
	agent := env("SFY_AGENT")
	return agent != "" && protocol.AgentName(agent) != protocol.Lead
}

// WithIdentity returns env with SFY_AGENT set to the caller's session name, as Agent finds it.
func WithIdentity(env func(string) string) func(string) string {
	agent := Agent(env)
	return func(key string) string {
		if key == "SFY_AGENT" {
			return agent
		}
		return env(key)
	}
}

// parse builds the request for a command line. It reads a file argument, and reads stdin for "-".
func parse(args []string, env func(string) string, stdin io.Reader) (protocol.Request, error) {
	req := protocol.Request{Caller: caller(env)}
	name, f, ok := lookup(args)
	if !ok {
		return req, &usageError{"unknown or incomplete command"}
	}
	rest := args[f.words:]
	need := f.ids
	if f.verdict {
		return parseVerdict(req, name, rest, stdin)
	}
	var option []string
	if f.option != "" && len(rest) > 0 && rest[0] == f.option {
		if len(rest) < 2 {
			return req, &usageError{fmt.Sprintf("%s needs a value", f.option)}
		}
		option, rest = []string{rest[0], rest[1]}, rest[2:]
	}
	if len(f.flags) > 0 && len(rest) == need+1 && slices.Contains(f.flags, rest[need]) {
		req.Command, req.Args = name, rest[need:]
		return req, nil
	}
	if len(rest) < need || (!f.text && len(rest) != need) || (f.text && len(rest) < need+1) {
		return req, &usageError{fmt.Sprintf("wrong arguments for %q", name)}
	}
	req.Command = name
	if f.file {
		// A worker or reviewer runs sfy outside the sandbox. A path would let it send any file it can read, so it passes text on stdin.
		if path := rest[need-1]; path != "-" && req.Caller.Agent != "" && protocol.AgentName(req.Caller.Agent) != protocol.Lead {
			return req, &usageError{"workers and reviewers pass the text on standard input as -, not as a file path"}
		}
		body, err := readFile(rest[need-1], stdin)
		if err != nil {
			return req, err
		}
		req.Body = body
		rest = rest[:need-1]
	}
	if option = append(option, rest...); len(option) > 0 {
		req.Args = option
	}
	return req, nil
}

// parseVerdict reads the verdict from the words, or from standard input for "-". It never opens a path, so a reviewer cannot probe the file system.
func parseVerdict(req protocol.Request, name string, rest []string, stdin io.Reader) (protocol.Request, error) {
	if len(rest) == 0 {
		return req, &usageError{fmt.Sprintf("wrong arguments for %q", name)}
	}
	req.Command = name
	if len(rest) == 1 && rest[0] == "-" {
		body, err := io.ReadAll(stdin)
		if err != nil {
			return req, err
		}
		req.Body = string(body)
		return req, nil
	}
	req.Body = strings.Join(rest, " ")
	return req, nil
}

// lookup finds the form of a command line. A command name has one or two words.
func lookup(args []string) (string, form, bool) {
	if len(args) >= 2 {
		if f, ok := forms[args[0]+" "+args[1]]; ok && f.words == 2 {
			return args[0] + " " + args[1], f, true
		}
	}
	if len(args) >= 1 {
		if f, ok := forms[args[0]]; ok && f.words == 1 {
			return args[0], f, true
		}
	}
	return "", form{}, false
}

func readFile(path string, stdin io.Reader) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(stdin)
	} else {
		b, err = os.ReadFile(filepath.Clean(path))
	}
	return string(b), err
}

// ErrRestricted reports that a worker or a reviewer ran a command that only the operator and the lead run.
var ErrRestricted = errors.New("a worker or a reviewer may not run this command")

// Run executes one sfy command and returns the exit code.
func Run(args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	var code int
	var err error
	if len(args) >= 1 && args[0] == "hook" {
		code, err = runHook(args[1:], env, stdin, stdout, stderr)
	} else {
		code, err = runCommand(args, env, stdin, stdout, stderr)
	}
	var ue *usageError
	if errors.As(err, &ue) {
		_, _ = fmt.Fprintf(stderr, "sfy: %s\n%s", ue.msg, usage)
		return exitUsage
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
		return exitFailed
	}
	return code
}

// runHook runs "sfy hook" with the words after it. A worker or a reviewer runs only the forms that its own settings hold.
func runHook(args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if Restricted(env) && !hookcmd.Own(args) {
		return exitFailed, ErrRestricted
	}
	sender := func(home string, req protocol.Request, reply func(string) string) int {
		return sendTo(home, req, env, stdout, stderr, reply)
	}
	code, err := hookcmd.Run(args, env, stdin, stdout, stderr, func() protocol.Caller { return caller(env) }, sender)
	var ue *hookcmd.UsageError
	if errors.As(err, &ue) {
		return code, &usageError{ue.Msg}
	}
	return code, err
}

// runCommand runs every command except a hook.
func runCommand(args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	req, err := parse(args, env, stdin)
	if err != nil {
		return exitFailed, err
	}
	return send(req, env, stdout, stderr, nil), nil
}

// send calls the sfy process and prints its answer. It passes the answer text through reply first, and a nil reply keeps the text.
func send(req protocol.Request, env func(string) string, stdout, stderr io.Writer, reply func(string) string) int {
	return sendTo(config.Home(env), req, env, stdout, stderr, reply)
}

// sandboxHint tells an agent how to run an sfy command outside the sandbox, which blocks the socket.
const sandboxHint = "Run the sfy command alone, with no pipe, redirection, cd, && or $( ), so it runs outside the sandbox."

// dialFailed reports whether the call failed before it reached the socket. A failure to read or decode the answer is not one.
func dialFailed(err error) bool {
	var nr *server.NotRunningError
	if errors.As(err, &nr) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// sendTo is send for the sfy process of the factory folder home.
func sendTo(home string, req protocol.Request, env func(string) string, stdout, stderr io.Writer, reply func(string) string) int {
	ans, err := server.Call(filepath.Join(home, "sfy.sock"), req)
	var nr *server.NotRunningError
	if errors.As(err, &nr) && req.Command == "down" {
		_, _ = fmt.Fprintln(stdout, "sfy is not running.")
		return exitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
		// A hook does not run in the sandbox, so only an agent's own command gets the hint.
		if env("SFY_AGENT") != "" && dialFailed(err) && !strings.HasPrefix(req.Command, "hook ") {
			_, _ = fmt.Fprintln(stderr, sandboxHint)
		}
		if errors.As(err, &nr) {
			return exitNotRunning
		}
		return exitFailed
	}
	if ans.Warning != "" {
		_, _ = fmt.Fprintln(stderr, ans.Warning)
	}
	if !ans.OK {
		_, _ = fmt.Fprintln(stderr, strings.TrimRight(ans.Error, "\n"))
		return exitFailed
	}
	text := strings.TrimRight(ans.Text, "\n")
	if reply != nil {
		text = reply(text)
	}
	// The operator may run several factories, so these two commands say which one answered.
	if req.Command == "status" || req.Command == "task list" {
		_, _ = fmt.Fprintf(stdout, "%s\n\n", config.Header(home, env))
	}
	if text != "" {
		_, _ = fmt.Fprintln(stdout, text)
	}
	return exitOK
}
