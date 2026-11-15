# Running sfy on Rex

This note studies Rex, a terminal app in beta, as a third multiplexer for sfy. It answers six questions from the `rex` help text.

## How to read this note

The worker sandbox blocks the Rex server socket, so no command in this note ran against a live server. Every claim has one of two marks.

- **read in help** means the claim comes from `rex --help`, `rex <command> --help`, or `rex __internal skill --feature-session-naming=false`.
- **untested** means the claim is a guess from the help text. The section "Tests to run outside the sandbox" holds a command that settles it.

The only command that ran was `rex <command> --help`. It needs no server. `rex whoami --json` failed in the sandbox with `dial unix .../rex/server.sock: connect: operation not permitted`.

## The rex binary

`rex` is not on the PATH (tested: `which rex` exits 127). The full path is:

```
/Users/jacobnilsson/Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS/rex
```

The path has spaces, and it contains `Versions/Current`, so an app update may move the binary. A template that runs `rex` needs one of these:

- A link on the PATH, for example `ln -s '<full path>' /usr/local/bin/rex`.
- The full path, quoted, in every template.

The link is simpler. sfy runs each template with `sh -c`, so a quoted full path also works, but it makes every template hard to read.

## 1. Commands for each template

All commands below are marked "read in help" for their syntax. Their exact behavior is untested.

| Template | Command | Mark |
| --- | --- | --- |
| Start | `rex new {name} -c {dir} -- {command}` | syntax read in help, behavior untested |
| Send | `rex send -s {name} -- {text} && rex send-key -s {name} enter` | syntax read in help, behavior untested |
| Alive | `rex block call -s {name} process` | syntax read in help, output untested |
| Stop | `rex kill {name}` | read in help |
| Show | `rex do session.select session_id={name}` | syntax read in help, needs Remote Control |

### Start

`rex new [LABEL] [-- COMMAND...]` creates a session with one terminal tab (read in help). `-c, --cwd` sets the working directory (read in help). `{name}` becomes the session label.

`COMMAND` runs through the login shell's environment but not through interactive startup files (read in help for `run`, assumed the same for `new`, untested). Profile variables and PATH apply, and aliases do not. `--shell=none` starts the command directly. `--interactive` also reads `~/.zshrc`.

`--keep-open` keeps the terminal after the command exits (read in help). Without it, the terminal may close when the agent exits. That changes what Alive sees. The section "Alive" covers this.

`new` has no flag that sets `REX_SESSION` for the child, but the usage guide says Rex sets `REX_SESSION` and `REX_BLOCK` when it opens a terminal (read in help).

### Send

`rex send [TEXT]` sends text "exactly as provided" and does not press a key (read in help). `rex send-key enter` presses Return (read in help). The usage guide shows this exact pair, joined with `&&`:

```
rex send -s "$REX_SESSION" -b "$shell_block" 'npm test' &&
  rex send-key -s "$REX_SESSION" -b "$shell_block" enter
```

`-s` takes a session, and `-b` takes a block. sfy keeps one name for each agent, so the template uses only `-s {name}` and relies on the default block of the session. Without `-b`, Rex uses `REX_BLOCK` if it names a live block, and otherwise the focused block of the default window (read in help for `capture` and `block call`, assumed the same for `send`, untested). The operator may move focus to another tab in the session, so the template must name the block.

Text that starts with a dash may read as a flag. The template puts `--` before `{text}`. Whether `rex send` accepts `--` is untested.

### Alive

Rex has no `has-session` command. Three candidates exist:

1. `rex block call -s {name} process` reports whether the program runs and its `last_exit` (read in help in the usage guide, output untested).
2. `rex session inspect {name} --json` exits non-zero for a missing session (untested).
3. `rex ls --json` lists sessions (read in help for the flag, JSON shape untested).

A terminal created without `--keep-open` may vanish when the agent exits. Then the session lookup fails, and "gone" is the right answer. A terminal created with `--keep-open` stays, and only the `process` call reports that the agent ended. The Herdr set solves the same problem by reading `"state":"live"`.

The Alive template must separate three results: running (exit 0), gone (exit 1), and a Rex failure such as an unreachable server (exit 2). The exit code of `rex` for a missing session is untested. The sketch in the recommendation shows both cases.

### Stop

`rex kill SESSION...` kills sessions (read in help). `rex session kill` exists too (read in help).

### Show

`rex new` "doesn't ask an app to display the new session" (read in help). To show it, the usage guide gives:

```
rex -C CLIENT do session.select session_id=SESSION
```

Details (read in help):

- Client actions need Remote Control, which is off by default. The usage guide says not to change that setting.
- Client actions need an open client app.
- Without `-C` or `REX_CLIENT`, Rex selects the app if exactly one is connected. With several, Rex needs `-C`.
- `session.select` takes a `session_id`. Whether it accepts a label is untested.

If Remote Control is off, Show fails. The Show template is optional in sfy (`Show` is empty for tmux), so sfy can leave Show empty and the operator can pick the session in the app.

## 2. Can Rex start a terminal without a tab or focus?

Yes, probably. This is read in help, with one untested step.

- `rex new` creates a session. It "doesn't ask an app to display the new session" (read in help). A session is a server object and not a tab in an app.
- `rex run` opens a new tab by default, and `--focus=false` preserves the active window (read in help). `rex window new --focus=false` leaves the current window active (read in help).

`rex new` does not take a `--focus` flag (read in help). The help text promises that it shows nothing in the app. Whether the Rex app lists the session in its sidebar, or switches to it, is untested. Test T2 covers this.

The Herdr problem was that the view jumped to each new tab. A session in Rex is not a tab of the operator's session, so the jump should not happen. A second worry is a sidebar that grows with every worker. That is a display matter and not a failure.

## 3. Can Rex read and restore the input line, or hold a send?

Nothing in the help text says so. This is read in help as an absence, so the answer is "not found", and it is untested.

What the help text offers:

- `rex capture` prints the screen and scrollback as text, HTML, or VT sequences (read in help). A capture shows the text of the prompt line. It does not mark which part is the unsent draft.
- `rex block call ... modes` reports `screen` (`alternate` while a full-screen program runs) (read in help).
- `rex block inspect BLOCK --json` lists the methods of a block (read in help). A terminal block may expose a method that reads the cursor or the input line. The help text does not name one. Test T4 lists the methods.
- `rex block call -b BLOCK write '{"data":"ls\n"}'` writes raw data (read in help, example for `block call`).
- `rex send-key` can press `ctrl+u` or `ctrl+a`, which a shell and an agent TUI may use to clear or move in the line (read in help for key syntax, behavior untested).

None of these restores a draft. A restore needs the text of the draft, and `capture` cannot tell the draft from other text on screen. Claude Code's input box is drawn in a full-screen layout, so `capture` shows the whole box and its borders.

Holding a send until the line is empty needs a check of the line, and no such check exists in the help text. Task t25 was cancelled because Herdr lacks one. Rex does not close that gap on the evidence of the help text alone. Test T4 looks for a block method that does.

## 4. Starting the main agent and finding terminals by name

### Main agent

The main agent runs in the operator's terminal, so the operator starts it in the Rex app. Two ways to start it from sfy exist, and both are untested:

- Run `rex new main -c {dir} -- <agent command>` and select it with `rex do session.select session_id=main`.
- Run `rex window new main -c {dir} -- <agent command>` inside the operator's own session, using `$REX_SESSION`. This opens a tab, and `--focus=false` keeps the view.

Rex sets `REX_SESSION` and `REX_BLOCK` in the environment of each terminal it opens (read in help). The main agent then knows its own session and block. sfy can use them to avoid typing into its own pane. The usage guide says: "Don't send input to your own pane, where the agent is running."

### Finding terminals by name

- `rex ls` lists sessions. `--json` prints JSON, and `-q` prints IDs only (read in help).
- `rex session rename SESSION NAME` renames a session (read in help).
- A target may be an ID, a label, or an ID suffix. "Labels and ID suffixes may match more than one resource" (read in help). Two agents with the same label, or a label that equals an ID suffix, make a command ambiguous. sfy names agents `t<id>` style names, so an ID suffix clash is possible but unlikely. Test T6 checks the duplicate label case.
- `rex session inspect SESSION --json` reports `active_window_id` and the windows with their `focused_block_id` (read in help). The operator can use it to find the pane that the app shows.

The operator finds an agent in the Rex app by the session label. sfy finds it with the same label through `-s {name}`.

## 5. What breaks or needs care

- **Binary path.** `rex` is not on the PATH, and the path has spaces and a version folder. See "The rex binary".
- **Beta CLI.** The usage guide says "Some commands listed in help aren't implemented yet" and names `rex resize` and `rex block new` (read in help). Flags and JSON fields may change between versions. sfy should pin none of them and should keep the templates in the user config, where the operator can edit them.
- **Output formats.** sfy parses only exit codes today for tmux and the JSON `state` field for Herdr. For Rex, Alive may need `process` output or `ls --json`. The field names are untested. Test T3 records them. A parse with `sed` on JSON is brittle, so prefer a command with a clean exit code if one exists.
- **Timeouts.** The default `--timeout` is 10 seconds for each connection and request (read in help). sfy kills a template after 30 seconds. They fit together.
- **Autostart.** `--autostart` is true by default and starts a missing local server (read in help). A template that runs when Rex is closed may start a server by surprise. Use `--autostart=false` in Alive and Stop so a missing server reads as "gone".
- **Remote Control.** Client actions, and so Show, need Remote Control, which is off by default (read in help).
- **Focus.** `run`, `split`, and `window new` focus the new pane unless `--focus=false` (read in help). The Start template uses `rex new`, which has no focus flag.
- **Worker sandbox.** The sandbox blocks the Rex socket for workers (tested: `connect: operation not permitted`). A worker cannot run `rex` and cannot type into any terminal. This is the right default. A worker that could reach the socket could type into any terminal of the operator. sfy itself runs the templates outside the worker sandbox, so no change is needed for the design in this note. Letting workers reach the socket is the operator's decision.
- **Key layout.** `send-key` interprets printable keys with a US layout. `rex send` is the safe way to send exact text (read in help).
- **Scrollback.** A terminal keeps about 64 MiB of scrollback, and older lines drop without notice (read in help).
- **Argument quoting.** sfy quotes `{command}` word by word. `rex new ... -- {command}` takes words after `--` (read in help). Test T1 checks that quoting survives.
- **Duplicate sessions after a timeout.** The usage guide says a timed-out command may have succeeded, so inspect state before a retry (read in help). sfy's Start does not retry, so it needs no change.

## 6. Example config

sfy ships no Rex set. A user adds one to `~/.sfy/config.toml` and sets `multiplexer = "rex"`. Each template is a shell command, so the example writes the full binary path in each one.

```toml
multiplexer = "rex"

[multiplexers.rex]
start = '''
"/Users/<you>/Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS/rex" new {name} -c {dir} --keep-open -- {command}
'''
send = '''
rex="/Users/<you>/Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS/rex"
"$rex" send -s {name} -- {text} && "$rex" send-key -s {name} enter
'''
alive = '''
rex="/Users/<you>/Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS/rex"
out=$("$rex" --autostart=false block call -s {name} process 2>&1); rc=$?
case $out in *"not found"*) exit 1;; esac
if [ $rc -ne 0 ]; then printf '%s\n' "$out" >&2; exit 2; fi
case $out in
  *'"child":null'*|*'"child": null'*) exit 1;;
  *'"child"'*) exit 0;;
esac
exit 2
'''
stop = '''
"/Users/<you>/Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS/rex" --autostart=false kill {name}
'''
```

Notes on the example:

- Replace `/Users/<you>` with the home folder. The path contains `Versions/Current`, so an app update may move the binary.
- `rex new` does not move focus, so the set has no `show` template.
- Rex allows two sessions with one label. Remove a stale session with `rex kill <label>`.
- Alive exits 1 for a finished or missing session and 2 for any other failure.
- Rex cannot read the input line, so sfy cannot see a draft that the operator has typed.
- The strings `not found` and `"child"` come from the `rex block call process` output. Test T3 shows the real output on your version.

## Tests to run outside the sandbox

Run these in a normal terminal with the Rex app open. Each line shows the command and the output that answers the question. Use a link `rex` on the PATH, or the full path.

**T1. Start works and quotes survive (Q1).**

```
rex new sfy-test -c /tmp --keep-open -- sh -c 'echo "it'"'"'s up"; sleep 600'
```

Pass: exit 0, and `rex capture -s sfy-test --trim` prints `it's up`.

**T2. No tab and no focus change (Q2).** Keep the Rex app on another session, then run T1.

Pass: the app does not switch to `sfy-test` and shows no new tab in the current session. Check with `rex session inspect "$REX_SESSION" --json`. The value of `active_window_id` must not change.

**T3. Alive output (Q1, Q5).**

```
rex block call -s sfy-test process
rex session inspect sfy-test --json
rex ls --json
rex session inspect no-such-session --json; echo "exit $?"
```

Pass: `process` shows a field that tells running from finished. Record its exact name and value. The last command shows the exit code and text for a missing session.

**T4. Input line access (Q3).**

```
rex block inspect -s sfy-test --json
rex block call -s sfy-test modes
```

Pass: a method in the `inspect` output that returns the cursor position or the input line. If none exists, Q3 is "no", and t25 stays cancelled. Then run `rex send -s sfy-test 'draft'` and `rex capture -s sfy-test --trim`. Check whether the capture shows `draft` in a place that tells it from output.

**T5. Send plus Enter (Q1).**

```
rex new sfy-send -c /tmp --keep-open -- sh
rex send -s sfy-send -- '-n hello' && rex send-key -s sfy-send enter
rex capture -s sfy-send --trim
```

Pass: the capture shows the line `-n hello` typed and entered, so `--` and a leading dash work. Use `echo` first, if `sh` runs the line as a command and reports an error.

**T6. Names (Q4).**

```
rex new sfy-test -c /tmp -- sleep 60
rex ls --json
rex kill sfy-test
```

Pass: the second `new` with the same label either fails or produces a session that `rex kill sfy-test` refuses as ambiguous. Record which.

**T7. Show (Q1).**

```
rex client ls
rex do session.select session_id=sfy-test
```

Pass: the app selects `sfy-test`. A failure message shows whether Remote Control is off or whether `session.select` rejects a label. If it rejects the label, use the `session_id` from `rex ls --json`.

**T8. Stop (Q1).**

```
rex kill sfy-test sfy-send; echo "exit $?"
rex ls
```

Pass: exit 0, and both sessions are gone from the list.

**T9. Main agent environment (Q4).** Start a session with `rex new sfy-env -c /tmp --keep-open -- env`, then run `rex capture -s sfy-env --trim`.

Pass: the output holds `REX_SESSION` and `REX_BLOCK`.

**T10. Server not running (Q5).** Quit the Rex app and its server, then run `rex --autostart=false ls; echo "exit $?"`.

Pass: a non-zero exit code that differs from the one in T3 for a missing session. Record both codes. Alive uses them to tell "gone" from "error".
