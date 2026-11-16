# Registering a running agent as the main agent

This note investigates one request. The operator wants an agent that already runs in a terminal to start sfy and register itself as the main agent (operator, 2026-11-16). The investigation changed no Go code.

## Decision

The operator declined registration of a running agent (designs 2 and 3 below, and any form of `sfy up --register`). The operator chose design 1, with one change: it is the default and has no `--here` flag (operator, 2026-11-16).

- `sfy` and `sfy up` start the main agent in the terminal where the operator runs them, when the main agent does not run and the template set has a `whoami` template that prints an address.
- sfy writes the address to `sessions/main/address` and replaces its own process with the main agent's command. The flags, hooks, prompt, identity and resume stay the same as for a started session.
- Without `whoami`, or when it fails or prints nothing, sfy starts the main agent through `start` and `show_main` as before.
- `mux.Registered` in `internal/mux` sends the calls for the session `main` to the recorded address. It removes the record when `alive` reports the address gone, and after a stop.
- `sfy down` stops the main agent through `stop` with the recorded address. `sfy restart` keeps the main agent and sends it the note. Neither one replaces its own process.
- The tmux set, `docs/herdr.md` and `docs/rex.md` have a `whoami` line. The Herdr and Rex lines were not run against a live terminal, as in the checks below.

The text below is the investigation as it stood before the decision.

## Summary

- sfy addresses the main agent by the literal session name `main` in every template call. A registered agent needs a stored address that replaces `{name}` for `main`.
- A running Claude Code process cannot get `SFY_AGENT`, the system prompt or (with certainty) the hooks. Each of the three changes how sfy treats the main agent.
- Registering from inside the running agent (design 2) costs the most code and delivers the least.
- Starting the main agent in the operator's current terminal (design 1) keeps flags, hooks, prompt and identity. It costs about 150 lines of core code.
- Recommendation: build nothing in Go now. Use design 3, which is documentation. Build design 1 only if placement through `show_main` proves too weak.

## What was checked

| Mark | Meaning |
|---|---|
| read | Read in this repository or in a help text. |
| ran | A command ran in this worker's sandbox. |
| docs | Read in the Claude Code docs through a fetch. The fetch was summarized by a subagent and some passages were not verbatim. |
| reasoned | Follows from the code, and no test ran. |
| not run | Could not run here. The section names the test for the operator. |

Limits of this investigation:

- The worker's environment sets no `TMUX_PANE`, `HC_TERMINAL_ID`, `REX_BLOCK` or `REX_SESSION`. No `whoami` template ran against a live terminal.
- `tmux new-session` fails in the sandbox with `fork failed: Operation not permitted`. A live interactive Claude Code session could not start.
- The Rex server socket is blocked in the sandbox, as `docs/rex.md` records.

## 1. Notes: how sfy learns the address

**How sfy addresses the main agent today (read).**

- `internal/up/up.go` starts the main agent with `Name: "main"`.
- `internal/up/restart.go` sends the restart note with `o.launcher.Send(mainSession, note)`.
- `internal/engine/commands.go` calls `e.l.Notify("main", ...)` for escalations and permission prompts.
- `internal/engine/tick.go` calls `e.l.Alive("main")` for the resend of unread notes.
- `internal/engine/down.go` starts its list of sessions to stop with `"main"`.

The mux runner (`internal/mux/mux.go`) fills `{name}` with that string. For a started session, the Start template gives the name to the multiplexer, so the name is also the address.

**A registered agent has no sfy-assigned name.** Its terminal exists already, and the multiplexer named it. sfy needs two things.

1. The address of the agent's own terminal, read from inside that terminal.
2. A place to store it, and a step that swaps `main` for the stored address before a template runs.

**A `whoami` template.** The new optional template prints the current terminal's address. `sfy` runs it in the agent's terminal, so the tool's own variable is set. sfy stores the output, for example in `sessions/main/address`, and the launcher passes it as `{name}` for `main`. Go code names no tool.

Each tool already has a command that prints the value, and the value already works as `{name}` in that tool's set.

| Set | Variable | `whoami` template | Works as `{name}` in the existing set |
|---|---|---|---|
| tmux | `TMUX_PANE` | `tmux display-message -p -t "$TMUX_PANE" '#{session_name}'` | Yes. The set uses `-t ={name}:`, which takes a session name. |
| Herdr | `HC_TERMINAL_ID` | `hc get --json`, then the `address` field, the way `show_main` in `docs/herdr.md` does | Yes. `hc send`, `hc get` and `hc kill` accept an address. |
| Rex | `REX_SESSION`, `REX_BLOCK` | `printf '%s' "$REX_SESSION"` | Yes for `-s {name}`. The block problem in `docs/rex.md` stays. |

What was checked:

- read: `hc get --help` and `hc --help` say that `get` picks the current terminal when the terminal argument is missing. The argument takes "`t7`, a name, or `machine/t7`".
- read: `rex whoami --help` exists and prints "identity, server, and location".
- read: `docs/rex.md` says Rex sets `REX_SESSION` and `REX_BLOCK` in each terminal it opens.
- not run: all three `whoami` lines. Run each one inside a terminal of its tool and check that the printed value works in the set's Alive template.

**Care for each set.**

- tmux. The address is a session, so Send types into the session's active pane. If the operator moved to another pane or window, the note lands there. A pane id such as `%3` is exact, but the set's `={name}:` form rejects it. A tmux user needs a second Send template, which is more core.
- Herdr. The terminal address is exact. `hc` also holds sends while the operator types a draft (`draft_wait` in `hc --help`), which helps a main agent that the operator uses.
- Rex. Session scope has the same moving-focus problem as tmux. `docs/rex.md` already lists it for started sessions.

Without a `whoami` template, `sfy up --register <address>` could take the address as an argument. That adds an argument and no template, but the operator must find the address by hand.

## 2. Hooks

**What the hooks do for the main agent (read).** `internal/provider/claude/claude.go` `writeSettings` installs three hooks for every kind of agent.

| Hook | Command | Effect for `main` |
|---|---|---|
| `Stop` | `sfy hook turn-ended` | `hookTurnEnded` sets `waiting` and `hookSince`. If a message is unread and newer than the last note, it types "New message. Run: sfy inbox" into the pane. |
| `Notification` with `idle_prompt` | `sfy hook idle` | `hookIdle` does the same without the missing-report reminder, which `main` never gets. |
| `PermissionRequest` | `sfy hook permission` | For `main`, `hookPermission` runs the `notify` template with "Main waits at a permission prompt" and returns no decision. The prompt stays open for the operator. |

**What breaks without them (reasoned from `internal/engine`).**

- `message()` in `effects.go` types a note only when `agent(to).waiting` is true. Only a hook sets it. Without hooks, `waiting` stays false and no message produces an immediate note.
- The only other path is `resendNotes` in `tick.go`. It types after `resend_minutes` (default 5) when `s.waiting || !s.hookSince`. Without hooks `hookSince` stays false, so the condition always holds.
- So every note arrives after at least five minutes. sfy then types it into the pane at that moment, even mid-turn or while the operator types a draft. It repeats every five minutes while the message stays unread.
- The permission notification never fires, so the operator does not learn that the main agent waits at a prompt.
- Escalations still notify, because `escalateTask` calls `Notify` directly and needs no hook.
- Worker and reviewer handling does not change. Their hooks come from their own settings.

**Can a running session get the hooks later?**

- docs: the settings page says "Claude Code watches settings files and reloads" them, and lists `hooks` among the changes applied without a restart. A `ConfigChange` event exists. The `/hooks` menu is a read-only browser. No slash command adds a hook.
- docs: the same page does not say whether a file passed with `--settings` is watched. It covers "user, project, local, and managed settings".
- ran, inconclusive: a long-lived `claude -p --input-format stream-json` process with `--settings <file>`, where I rewrote the settings file between two turns. The process never produced a model turn in the sandbox, so the test shows nothing about reload.
- not run: the same test in a normal terminal. Start `claude --settings /tmp/x.json`, run one turn, add a `Stop` hook that appends to a file, in `/tmp/x.json` and then in `.claude/settings.local.json`, run a second turn, and check which file grew.

If the reload works, a registered agent can get hooks by two routes.

- The user settings file or the project's `.claude/settings.local.json`. The user file applies to every Claude Code session of that user, including sfy's workers and reviewers. The project file applies to every session that runs in that folder. Hook entries merge across files, so the workers would run the new hook as well as their own.
- The hook command can carry its identity: `SFY_AGENT=main sfy hook turn-ended`. That solves the identity problem for hooks only. Question 4 covers the commands that the agent types.

## 3. Prompt

**Today (read).** `Command` passes `--append-system-prompt <role>`. `recordMainPrompt` in `internal/up/up.go` also writes the same text to `main-prompt.md` at every start. The default is `internal/prompts/defaults/main.md`, about 1,000 words.

**Options for a registered agent.**

1. `sfy up --register` prints the prompt text as its output. The agent reads it as a tool result.
2. The operator tells the agent to read `main-prompt.md` with the Read tool. `restart` already uses this route for a changed prompt.
3. The operator sets a line in `CLAUDE.md` that points to the file. That is a change outside sfy.

**Cost compared with a system prompt (docs for the flag, reasoned for the rest).**

- A system prompt is sent with every request, and compaction does not drop it. A tool result is part of the conversation. A later compaction may summarize or drop it, and the agent then forgets the `sfy` command list and the merge rules.
- A tool result carries less authority than the system prompt. A conflict with the user's own `CLAUDE.md` resolves less predictably.
- The agent must read the text in every new conversation. A resumed conversation of a registered agent has no way to receive it again, except through a note.
- Nothing in the Claude Code docs I read adds a system prompt to a running session. The settings page names `--append-system-prompt` as a launch flag.

The cost is acceptable for a trial and weak for daily use, because the main prompt holds rules that matter, such as "never do a task's work yourself".

## 4. Identity

**What a registered agent runs as (read).**

- `internal/cli/cli.go` `parse` reads `SFY_AGENT` and `SFY_TASK` from the environment of the `sfy` call.
- `internal/engine/engine.go` `kindOf` turns the empty name into `operator`.
- A registered agent's tool shell has no `SFY_AGENT`, so every call arrives as the operator.

**What the operator may run that matters here (read, `commands` in `engine.go`).**

| Command | Operator | Main agent | Effect for a registered agent that runs as the operator |
|---|---|---|---|
| `inbox` | no | yes | The agent cannot read its messages. The loop stops. |
| `send` | no | yes | It cannot message workers. |
| `task edit`, `task queue` | no | yes | It cannot prepare tasks that others create. |
| `escalate` | no | yes | It cannot escalate. |
| `down` | yes | no | It can run `sfy down`, which stops its own session mid-command. The engine's permission table gives `down` to the operator only, for that reason. |
| `sfy`, `sfy up` | yes | no | `cmd/sfy/main.go` blocks these when `SFY_AGENT` is set. A registered agent could run them. If main is not registered yet, a plain `sfy` starts a second main agent. |

So the agent cannot do its job as the operator. It needs the identity `main`.

**Is giving it that identity safe?** The identity is not a security boundary (read).

- `SPEC.md` says the socket "does not separate the operator's own agents from each other, since they all run as that user".
- Workers and reviewers run `sfy` outside the sandbox with their own environment (`excludedCommands` in `claude.go`). A worker can already write `SFY_AGENT=main sfy ...`. I did not test this. The code gives `sfy` no other check.

So a registered agent that claims `main` adds no capability that a worker does not already have. The identity is a convention that keeps honest agents inside their role.

**How would the running agent get the identity?**

- The environment of a running Claude Code process cannot change. A shell state such as an `export` does not persist between its Bash calls (this session's tool description says so). So the agent cannot set `SFY_AGENT` once.
- The agent could type `SFY_AGENT=main sfy inbox` each time. The prompt can say so. The allow rules are `Bash(sfy *)` and `Bash(<sfyBin> *)`, and I did not test whether a leading assignment still matches. This is fragile.
- The CLI could run `whoami` on every command and send the address as a second caller field. The engine maps the registered address to `main`. This makes the identity follow the terminal, so it also covers the hook route. It costs about 60 lines in `cli` and `engine`, and it runs a shell template before each `sfy` command.
- The registering step could start the agent itself with `SFY_AGENT=main` in its environment. That is design 1.

## 5. Lifecycle

For each template, what sfy should run on a session it did not start.

| Template | Run it on a registered session? | Reason |
|---|---|---|
| Alive | Yes | It is read-only. `tick.go` and `ensure` need it to know that main runs, and to find a stale address. |
| Send | Yes | It delivers notes. |
| Notify | Yes | It draws attention to the operator's own terminal. |
| Show, `show_main` | No | The operator is already in that terminal. Herdr's `show_main` would move the terminal. |
| Stop | No | In tmux, `kill-session` ends the operator's session with every other pane. In Herdr, `hc kill` ends the program in a terminal that the operator uses. The operator started this terminal, so the operator ends it. |

**`sfy down` (read, `internal/engine/down.go`).** `downCommand` lists `main` first and stops it when Alive is true. For a registered agent it must skip the stop and say so, for example "Left main running. It is registered, so sfy did not start it." That changes the SPEC line "Stops every agent session".

**`sfy restart` (read, `internal/up/restart.go`).** It stops only the sfy process, then starts it again. It sends `noteMain` through `Send`, so a registered main gets the note without change. The note names `main-prompt.md` when the prompt changed.

**Resume of the main conversation (read, `conversation.go`).**

- The first start writes `sessions/main/conversation-id`. A resumed start passes `--resume <id>` and requires the same folder. sfy's main always starts in the state folder.
- A registered agent has its own conversation, and sfy does not know its id. The file stays missing or stale.
- If the registered agent ends and the operator runs a plain `sfy`, the stored address is dead. `ensure` sees Alive false and starts a new main with the templates. If a stale conversation id exists, it resumes that one. The old address must go first, or Send keeps typing into a dead target.
- Design 1 avoids the mismatch. sfy starts the agent, writes the id, and `sfy up --here` resumes it the next time like today.

**A plain `sfy` while a registered main runs (read, `run` in `up.go`).** It prints "The main agent already runs and was shown." and calls `ShowMain`. Show must skip a registered session, as the table says.

## 6. Several state folders (task t45)

I did not read t45's code. This section reasons from its description: a prefix per state folder, so several sfy folders run side by side.

- The address file lives in the state folder, at `sessions/main/address`. Two folders have two files, so the stored addresses do not collide.
- A prefix changes the names that sfy gives its own sessions, for example `<prefix>-main`. A registered main has a stored address and needs no prefix. The resolve step must run before the prefix, or the prefix must skip a registered name.
- One terminal can host one agent, but a single address file does not know that. `sfy up --register` could write the same address into two folders. Two folders would then type notes into one agent, and both would claim it as `main`. The step must refuse an address that another folder's file holds. That check needs a scan of the sibling folders, which sfy cannot find without a registry. Design 1 avoids it, because `--here` replaces the shell with the agent, so the terminal cannot register twice.
- `SFY_HOME` selects the folder (`SPEC.md`). A registered agent has no `SFY_HOME` in its environment, so its `sfy` commands go to `~/.sfy`. With several folders it needs the variable on every command, like `SFY_AGENT`. This is one more reason to prefer an exec that sets the environment.

## Designs

Sizes count Go lines in `internal/`, including the tests that the repo's gate expects (about one test line per code line).

### Design 1. `sfy up --here`: start the main agent in this terminal

The operator opens a terminal, or quits the agent in it, and runs `sfy up --here` in a shell. sfy runs `whoami`, stores the address, and then replaces the shell with the normal main command (`sfy run` into `claude` with the same flags, settings, prompt and conversation id). The agent is not already running. It starts in a terminal that already exists.

- Core code:
  - `mux`: a `whoami` field in `Templates`, in `FromMap`, and a `Whoami()` method. About 15 lines.
  - `launcher`: a resolve step that swaps `main` for the address in Send, Alive, Notify, Stop and Show. About 25 lines.
  - `up`: the `--here` flag, a Start that execs and skips `mux.Start`, and the stale-address cleanup. About 60 lines.
  - `engine`: skip Stop and Show for a registered main in `down`. About 15 lines.
  - Total: about 115 lines of code and about 115 of tests.
- Template sets: add one `whoami` line to tmux, Herdr and Rex. The other templates stay.
- Hooks, prompt and identity: all as today, because sfy starts claude.
- What breaks:
  - The terminal's shell is gone. Closing the terminal ends the agent.
  - `sfy down` leaves main running, which changes one SPEC sentence.
  - An agent that already runs cannot join. The operator quits it and starts it with `--here`. The conversation can continue only if it started in the state folder.
- Size: medium, about 230 lines with tests.

### Design 2. `sfy up --register` from inside the running agent

The agent runs `sfy up --register` in its own Bash tool. sfy stores the address and prints the prompt.

- Core code:
  - The `whoami` template and the launcher resolve step, as in design 1. About 40 lines.
  - `up`: the `--register` flag, prompt printing, and a refusal of a second registration. About 40 lines.
  - Identity: the CLI runs `whoami` and sends a caller address, and the engine maps it to `main`. About 60 lines.
  - Hooks: a settings route that merges into user or project settings, and sets `SFY_AGENT=main` in the hook command. About 50 lines in `internal/provider/claude`, and it works only if the reload test passes.
  - `engine`: skip Stop and Show. About 15 lines.
  - Total: about 205 lines, and about 205 of tests.
- Template sets: one `whoami` line each, as in design 1.
- What breaks:
  - The prompt is a tool result and not a system prompt (question 3).
  - Without a working reload, no hooks. Notes are delayed by `resend_minutes` and arrive blind (question 2).
  - The hook settings in the user or project file also reach workers and reviewers. This needs a guard.
  - Every `sfy` command pays for one shell template (not measured).
  - The conversation id is unknown.
- Size: large, about 410 lines with tests. It also adds the first path where the CLI runs a template.

### Design 3. No registration. Move the main terminal with the existing templates

The operator keeps `sfy` as it is. The set's `start`, `show_main` and a tool command that moves the terminal place main where the operator wants it, for example `hc attach main` or `tmux attach -t main`. A short section in `docs/herdr.md` and in the README shows the command for each tool.

- Core code: none. About 20 lines of documentation.
- Template sets: no change. A user who wants a different place edits `show_main`.
- What breaks: nothing. The agent cannot be adopted. An agent that already runs in a terminal stays outside sfy.
- Size: none in Go.

## Recommendation

Do not build registration now. Write design 3 into the docs, and wait for the case it cannot meet.

The reasons:

1. The only thing that design 3 cannot do is adopt a process that already runs. Design 2 is the only way to do that, and it loses the hooks, the system prompt and the identity of a started agent. Each loss needs more core code to patch, and the patches (a template run in the CLI, a hook in user settings) add the most to the core.
2. Design 1 gives the operator's terminal placement with the full agent. A `show_main` template already places the started agent. The gain over design 3 is small and costs about 230 lines, a new template, and a changed `sfy down`.
3. The core stays small if each of these changes waits for a user that needs it.

If the operator still wants registration, build design 1 and not design 2. Before that, run the three `whoami` lines inside real terminals, and run the hook reload test from question 2, because that result decides whether design 2 could ever reach parity.

## Tests to run outside the sandbox

**T1. The `whoami` lines (question 1).** In a tmux pane, a Herdr terminal and a Rex block, run the line from the table. Then run the set's Alive template with the printed value as `{name}`. Pass: Alive exits 0 in each tool.

**T2. Hook reload (question 2).**

```
claude --settings /tmp/x.json
# one turn, then edit /tmp/x.json to add a Stop hook that runs: echo hit >> /tmp/hit.txt
# a second turn
```

Repeat with the hook in `.claude/settings.local.json` of the working folder. Pass: `/tmp/hit.txt` grows after the second turn. The file that grew tells which route reloads.

**T3. A leading assignment under an allow rule (question 4).** In a main session with the `Bash(sfy *)` allow rule, ask for `SFY_AGENT=main sfy status`. Pass: no permission prompt.

**T4. Identity is not a boundary (question 4).** From a worker session run `SFY_AGENT=main sfy inbox`. Pass: the reply is the main agent's inbox. This confirms the reasoning above and shows how open the check is.
