// Package hookcmd runs the commands that Claude Code's hooks start, such as "sfy hook turn-ended".
// It sits beside the Claude provider, so a change to a Claude hook touches one place.
package hookcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
	"github.com/JacobJNilsson/simple-factory/internal/server"
	"github.com/JacobJNilsson/simple-factory/internal/store"
)

// Exit codes of Run.
const (
	exitOK     = 0
	exitFailed = 1
	// exitWake is the code that makes Claude Code give a background hook's stderr to the agent.
	exitWake = 2
)

// sessionEvents lists the hook events that the settings of a started session run.
var sessionEvents = []string{"turn-ended", "permission", "idle", "busy", "note", "note-watch"}

// userEvents lists the events that the hooks in the user settings report.
var userEvents = []string{"turn-ended", "permission", "idle", "busy"}

// UsageError reports a hook command line that matches none of the forms.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Sender sends a request to the sfy process of the factory folder home, prints the answer and returns the exit code.
// It passes the answer text through reply first. A nil reply keeps the text.
type Sender func(home string, req protocol.Request, reply func(string) string) int

// Own reports whether args, the words after "hook", are a form that a started session's own hooks run.
// The user settings run "hook --user <event>" in every session, and that form stays silent for a started agent.
func Own(args []string) bool {
	if len(args) == 1 {
		return slices.Contains(sessionEvents, args[0])
	}
	return len(args) == 2 && args[0] == "--user" && slices.Contains(userEvents, args[1])
}

// Run executes one hook command and returns the exit code. args are the words after "hook".
// caller names the session that runs the hook. send reports to the sfy process.
// A command line that matches no form returns a *UsageError.
func Run(args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer, caller func() protocol.Caller, send Sender) (int, error) {
	switch {
	case len(args) == 2 && args[0] == "--user":
		return userHook(args[1], env, stdin, send)
	case len(args) != 1:
		return exitFailed, &UsageError{`wrong arguments for "hook"`}
	case args[0] == "note" || args[0] == "note-watch":
		return noteHook(args[0], env, stdin, stdout, stderr), nil
	case args[0] == "merge-guard":
		return mergeGuardHook(env, stdin, stderr), nil
	case args[0] == "turn-ended":
		return turnEndedHook(env, stdin, stderr, caller, send)
	}
	req, err := request(args[0], stdin, caller())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
		return exitFailed, nil
	}
	return send(config.Home(env), req, replyFor(args[0])), nil
}

// request builds the request that reports a hook event to the sfy process. It reads all of stdin.
func request(event string, stdin io.Reader, caller protocol.Caller) (protocol.Request, error) {
	body, err := io.ReadAll(stdin)
	if err != nil {
		return protocol.Request{}, err
	}
	return protocol.Request{Caller: caller, Command: "hook " + event, Body: claude.HookBody(event, body)}, nil
}

// replyFor returns the function that turns the sfy process's answer to an event into the output that Claude Code reads.
func replyFor(event string) func(string) string {
	return func(text string) string { return claude.HookReply(event, text) }
}

// userHook reports an event that comes from the user settings.
// The user settings reach every session of the user, so the hook acts only for the registered lead.
// It acts for no other session and for no agent that sfy started. It makes no socket call then, and it prints nothing.
func userHook(event string, env func(string) string, stdin io.Reader, send Sender) (int, error) {
	if !slices.Contains(userEvents, event) {
		return exitFailed, &UsageError{fmt.Sprintf("unknown hook event %q", event)}
	}
	if env("SFY_AGENT") != "" {
		return exitOK, nil
	}
	body, err := io.ReadAll(stdin)
	if err != nil {
		return exitFailed, err
	}
	session := claude.HookSession(body)
	for _, home := range userHookHomes(env) {
		c := identity.Folder{Home: home}.Resolve(identity.Claim{Session: session})
		if c.Agent != protocol.Lead {
			continue
		}
		req := protocol.Request{Caller: c, Command: "hook " + event, Body: claude.HookBody(event, body)}
		return send(home, req, replyFor(event)), nil
	}
	return exitOK, nil
}

// userHookHomes returns the factory folders that the user hook tries, in order.
// SFY_HOME names one. Otherwise they are all the factory folders of the working folder, and the default factory when it has none.
func userHookHomes(env func(string) string) []string {
	if h := env("SFY_HOME"); h != "" {
		return []string{h}
	}
	if cwd, err := os.Getwd(); err == nil {
		if homes := config.FactoriesAt(env("HOME"), cwd); len(homes) > 0 {
			return homes
		}
	}
	return []string{config.DefaultHome(env("HOME"))}
}

// mergeGuardHook is the PreToolUse hook of the lead. It exits with exitWake, which blocks the tool call, for a hand push, merge or rebase in a no-merge repo.
// It reads the repo config on each call. A config that cannot be read, and a folder that a shell expansion hides, count as no-merge.
func mergeGuardHook(env func(string) string, stdin io.Reader, stderr io.Writer) int {
	raw, _ := io.ReadAll(stdin)
	in, err := claude.ParseHookInput(raw)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sfy: merge guard cannot read the hook input: %v\n", err)
		return exitWake
	}
	targets := claude.MergeTargets(in.ToolInput.Command, in.Cwd, env("HOME"))
	if len(targets) == 0 {
		return exitOK
	}
	home := config.Home(env)
	cfg, err := config.LoadFactory(home, env("HOME"))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sfy: merge guard cannot read the config, so it blocks %s: %v\n", targets[0].Command, err)
		return exitWake
	}
	for _, t := range targets {
		if t.Unknown {
			_, _ = fmt.Fprintf(stderr, "sfy: blocked %s, because its folder comes from a shell expansion and sfy cannot check whether the repo is no-merge. Write the folder out.\n", t.Command)
			return exitWake
		}
		repo, err := config.ForRepo(cfg, env("HOME"), config.WorkFolder(home), t.Folder)
		if err != nil || repo.NoMerge {
			_, _ = fmt.Fprintf(stderr, "sfy: blocked %s in %s. The repo is set to no-merge, or its per-repo settings cannot be read. Leave the merge to the operator and tell the operator.\n", t.Command, t.Folder)
			return exitWake
		}
	}
	return exitOK
}

// hookEventName reads the hook input JSON and returns its hook_event_name, or "" when the input has none.
func hookEventName(stdin io.Reader) string {
	var in struct {
		Name string `json:"hook_event_name"`
	}
	_ = json.NewDecoder(stdin).Decode(&in)
	return in.Name
}

// agentName returns the session name in SFY_AGENT. A lead that started before the rename has the old name there.
func agentName(env func(string) string) string { return protocol.AgentName(env("SFY_AGENT")) }

// noteHook runs the two hooks that deliver notes through the session's note file. It needs no sfy process.
// note-watch prints the SessionStart or CwdChanged output that names the file. note prints the file's text to stderr and exits with exitWake.
// A caller with no session name, such as a registered lead, gets no output and exit code 0.
func noteHook(event string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	path, ok := claude.NotePath(config.Home(env), agentName(env))
	if !ok {
		return exitOK
	}
	if event == "note-watch" {
		out, err := claude.WatchNote(path, hookEventName(stdin))
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
			return exitFailed
		}
		_, _ = fmt.Fprintln(stdout, out)
		return exitOK
	}
	text, err := drainNoteText(env, path)
	if text != "" {
		_, _ = fmt.Fprintln(stderr, text)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
		}
		return exitWake
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
		return exitFailed
	}
	return exitOK
}

// drainNoteText empties the note file at path and returns the text that still waits.
// It marks the delivered messages read, even when it returns an error together with text.
func drainNoteText(env func(string) string, path string) (string, error) {
	entries, err := claude.DrainNote(path)
	if err != nil {
		return "", err
	}
	return takeDelivered(store.Files(config.Home(env)), agentName(env), entries)
}

// turnEndedHook is the Stop hook of a started session. It drains the note file first, so a message that came during the turn reaches the agent at the turn end at the latest.
// When text waits, it reports "busy" to sfy, prints the text to stderr and exits with exitWake, which keeps the turn going.
// Otherwise it reports the turn end. A failed drain never blocks the turn end.
func turnEndedHook(env func(string) string, stdin io.Reader, stderr io.Writer, caller func() protocol.Caller, send Sender) (int, error) {
	req, err := request("turn-ended", stdin, caller())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
		return exitFailed, nil
	}
	if path, ok := claude.NotePath(config.Home(env), agentName(env)); ok {
		text, err := drainNoteText(env, path)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
		}
		if text != "" {
			// The turn goes on, so sfy must not count the session as waiting. A failed report must not hide the text.
			busy := req
			busy.Command = "hook busy"
			_, _ = server.Call(filepath.Join(config.Home(env), "sfy.sock"), busy)
			_, _ = fmt.Fprintln(stderr, text)
			return exitWake, nil
		}
	}
	return send(config.Home(env), req, replyFor("turn-ended")), nil
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
