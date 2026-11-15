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
  sfy report done|blocked|progress <text...>
  sfy verdict pass|fail <text...|->
  sfy inbox
  sfy escalate <id> <text...>
  sfy status
  sfy hook <event>
`

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
	"verdict pass":    {words: 2, verdict: true},
	"verdict fail":    {words: 2, verdict: true},
	"inbox":           {words: 1},
	"escalate":        {words: 1, ids: 1, text: true},
	"status":          {words: 1},
	"hook":            {words: 1, ids: 1, stdin: true},
}

// parse builds the request for a command line. It reads a file argument, and reads stdin for "-" and for hooks.
func parse(args []string, env func(string) string, stdin io.Reader) (protocol.Request, error) {
	req := protocol.Request{Caller: protocol.Caller{Agent: env("SFY_AGENT"), Task: env("SFY_TASK")}}
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
	req, err := parse(args, env, stdin)
	var ue *usageError
	if errors.As(err, &ue) {
		_, _ = fmt.Fprintf(stderr, "sfy: %s\n%s", ue.msg, usage)
		return exitUsage
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
		return exitFailed
	}
	ans, err := server.Call(filepath.Join(config.Home(env), "sfy.sock"), req)
	var nr *server.NotRunningError
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
