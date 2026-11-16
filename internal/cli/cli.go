// Package cli turns sfy's command line into a request for the sfy process.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
	"github.com/JacobJNilsson/simple-factory/internal/registration"
	"github.com/JacobJNilsson/simple-factory/internal/server"
	"github.com/JacobJNilsson/simple-factory/internal/store"
)

// Exit codes of Run.
const (
	exitOK         = 0
	exitFailed     = 1
	exitUsage      = 2
	exitNotRunning = 3
	// exitWake is the code that makes Claude Code give a background hook's stderr to the agent.
	exitWake = 2
)

const usage = `usage:
  sfy                  start sfy (operator only)
  sfy up               same as sfy (operator only)
  sfy register         make the running agent session the main agent (not for started agents)
  sfy hooks install    add sfy's hooks to the user's Claude Code settings (operator only)
  sfy help
  sfy task new <file|->
  sfy task list [--archived]
  sfy task show <id>
  sfy task edit <id> <file>
  sfy task queue <id>
  sfy task cancel <id>
  sfy task resume <id>
  sfy task feedback <id> <text...>
  sfy task complete <id>
  sfy task archive <id>
  sfy send <agent> <text...>
  sfy report done|blocked|progress|pushed <text...>
  sfy verdict pass|fail <text...|->
  sfy inbox
  sfy escalate <id> <text...>
  sfy status
  sfy down             stop all agents and the sfy process (operator only)
  sfy restart          restart the sfy process and keep the agents running (--update runs brew upgrade first)
  sfy hook <event>
  sfy hook note        print the delivered notes to stderr and exit 2 (a hook of the session's settings)
  sfy hook note-watch  print the SessionStart output that names the note file (a hook of the session's settings)
  sfy hook --user <event>
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
	// text is true when the rest of the line is free text.
	text bool
	// stdin is true when the Body comes from standard input.
	stdin bool
	// verdict is true when the rest of the line is the Body text and "-" reads it from standard input.
	verdict bool
}

var forms = map[string]form{
	"task new":        {words: 2, ids: 1, file: true},
	"task list":       {words: 2, flags: []string{"--archived"}},
	"task show":       {words: 2, ids: 1},
	"task edit":       {words: 2, ids: 2, file: true},
	"task queue":      {words: 2, ids: 1},
	"task cancel":     {words: 2, ids: 1},
	"task resume":     {words: 2, ids: 1},
	"task feedback":   {words: 2, ids: 1, text: true},
	"task complete":   {words: 2, ids: 1},
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
	"status":          {words: 1},
	"down":            {words: 1},
	"hook":            {words: 1, ids: 1, stdin: true},
}

// Agent returns the session name of the caller. A started agent has it in SFY_AGENT.
// A registered agent has no such variable, so the provider names its session, and the registered id maps it to main.
// The operator and every other session get an empty name.
func Agent(env func(string) string) string {
	if agent := env("SFY_AGENT"); agent != "" {
		return agent
	}
	id := claude.CurrentSession(env)
	if id == "" {
		return ""
	}
	if record, ok := registration.Read(config.Home(env)); ok && record.ID == id {
		return "main"
	}
	return ""
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

// parse builds the request for a command line. It reads a file argument, and reads stdin for "-" and for hooks.
func parse(args []string, env func(string) string, stdin io.Reader) (protocol.Request, error) {
	req := protocol.Request{Caller: protocol.Caller{Agent: Agent(env), Task: env("SFY_TASK")}}
	name, f, ok := lookup(args)
	if !ok {
		return req, &usageError{"unknown or incomplete command"}
	}
	rest := args[f.words:]
	need := f.ids
	if f.verdict {
		return parseVerdict(req, name, rest, stdin)
	}
	if len(f.flags) > 0 && len(rest) == need+1 && slices.Contains(f.flags, rest[need]) {
		req.Command, req.Args = name, rest[need:]
		return req, nil
	}
	if len(rest) < need || (!f.text && len(rest) != need) || (f.text && len(rest) < need+1) {
		return req, &usageError{fmt.Sprintf("wrong arguments for %q", name)}
	}
	req.Command = name
	switch {
	case name == "hook":
		req.Command = "hook " + rest[0]
		body, err := io.ReadAll(stdin)
		if err != nil {
			return req, err
		}
		req.Body = string(body)
		return req, nil
	case f.file:
		// A worker or reviewer runs sfy outside the sandbox. A path would let it send any file it can read, so it passes text on stdin.
		if path := rest[need-1]; path != "-" && req.Caller.Agent != "" && req.Caller.Agent != "main" {
			return req, &usageError{"workers and reviewers pass the text on standard input as -, not as a file path"}
		}
		body, err := readFile(rest[need-1], stdin)
		if err != nil {
			return req, err
		}
		req.Body = body
		rest = rest[:need-1]
	}
	if len(rest) > 0 {
		req.Args = rest
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

// Run executes one sfy command and returns the exit code.
func Run(args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 2 && args[0] == "hook" && (args[1] == "note" || args[1] == "note-watch") {
		return noteHook(args[1], env, stdout, stderr)
	}
	var req protocol.Request
	var err error
	acts := true
	if len(args) == 3 && args[0] == "hook" && args[1] == "--user" {
		req, acts, err = userHookRequest(args[2], env, stdin)
	} else {
		req, err = parse(args, env, stdin)
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
	if !acts {
		return exitOK
	}
	return send(req, env, stdout, stderr)
}

// noteHook runs the two hooks that deliver notes through the session's note file. It needs no sfy process.
// note-watch prints the SessionStart output that names the file. note prints the file's text to stderr and exits with exitWake.
// A caller with no session name, such as a registered main agent, gets no output and exit code 0.
func noteHook(event string, env func(string) string, stdout, stderr io.Writer) int {
	path, ok := claude.NotePath(config.Home(env), env("SFY_AGENT"))
	if !ok {
		return exitOK
	}
	if event == "note-watch" {
		out, err := claude.WatchNote(path)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
			return exitFailed
		}
		_, _ = fmt.Fprintln(stdout, out)
		return exitOK
	}
	home, agent := config.Home(env), env("SFY_AGENT")
	entries, err := claude.DrainNote(path)
	if err == nil {
		var text string
		text, err = takeDelivered(store.Files(home), agent, entries)
		if text != "" {
			_, _ = fmt.Fprintln(stderr, text)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
			}
			return exitWake
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
		return exitFailed
	}
	return exitOK
}

// takeDelivered returns the text of the entries that still wait and marks their messages read.
// An entry for a message that sfy inbox already showed, or that an earlier entry already carried, adds nothing.
func takeDelivered(st store.Store, agent string, entries []claude.Entry) (string, error) {
	unread := map[string]bool{}
	if slices.ContainsFunc(entries, func(e claude.Entry) bool { return e.ID != "" }) {
		msgs, err := st.Unread(agent)
		if err != nil {
			return "", err
		}
		for _, m := range msgs {
			unread[m.ID] = true
		}
	}
	var texts, ids []string
	for _, e := range entries {
		if e.ID != "" {
			if !unread[e.ID] {
				continue
			}
			delete(unread, e.ID)
			ids = append(ids, e.ID)
		}
		texts = append(texts, e.Text)
	}
	return strings.Join(texts, "\n\n"), st.MarkRead(agent, ids)
}

// userHookEvents lists the events that the hooks in the user settings report.
var userHookEvents = []string{"turn-ended", "permission", "idle"}

// userHookRequest builds the request for a hook that comes from the user settings.
// The user settings reach every session of the user, so the hook acts only for the registered main agent.
// It acts for no other session and for no agent that sfy started. It makes no socket call then, and it prints nothing.
func userHookRequest(event string, env func(string) string, stdin io.Reader) (req protocol.Request, acts bool, err error) {
	if !slices.Contains(userHookEvents, event) {
		return req, false, &usageError{fmt.Sprintf("unknown hook event %q", event)}
	}
	if env("SFY_AGENT") != "" {
		return req, false, nil
	}
	record, ok := registration.Read(config.Home(env))
	if !ok {
		return req, false, nil
	}
	body, err := io.ReadAll(stdin)
	if err != nil || claude.HookSession(body) != record.ID {
		return req, false, err
	}
	return protocol.Request{Caller: protocol.Caller{Agent: "main"}, Command: "hook " + event, Body: string(body)}, true, nil
}

// send calls the sfy process and prints its answer.
func send(req protocol.Request, env func(string) string, stdout, stderr io.Writer) int {
	ans, err := server.Call(filepath.Join(config.Home(env), "sfy.sock"), req)
	var nr *server.NotRunningError
	if errors.As(err, &nr) && req.Command == "down" {
		_, _ = fmt.Fprintln(stdout, "sfy is not running.")
		return exitOK
	}
	if errors.As(err, &nr) {
		_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
		return exitNotRunning
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
		return exitFailed
	}
	if !ans.OK {
		_, _ = fmt.Fprintln(stderr, strings.TrimRight(ans.Error, "\n"))
		return exitFailed
	}
	text := strings.TrimRight(ans.Text, "\n")
	if req.Command == "task new" {
		// The reply holds the id, then any warning. The warning goes to stderr so $(sfy task new f) yields the id.
		var warning string
		text, warning, _ = strings.Cut(text, "\n")
		if warning != "" {
			_, _ = fmt.Fprintln(stderr, "sfy: "+strings.ToLower(warning[:1])+warning[1:])
		}
	}
	if text != "" {
		_, _ = fmt.Fprintln(stdout, text)
	}
	return exitOK
}
