# simple factory spec

sfy runs coding agents as interactive sessions in terminal panes. The operator talks to one main agent. The main agent hands out tasks, workers do them, and a fresh reviewer session checks each result. Agents report to sfy by running its CLI.

sfy builds only this loop. A terminal multiplexer runs the panes, and an agent CLI runs the models. The core stays small, and each use extends it to fit its own needs.

This spec covers version 0.

## Scope

- sfy is written in Go and ships as one binary, `sfy`.
- The agent provider sits behind an adapter. Version 0 has one adapter, for Claude Code. The design allows more providers, and several in one sfy process.
- A multiplexer is a set of command templates in `config.toml`, with no Go code. Version 0 ships sets for tmux and Herdr. A new multiplexer needs only a new set.
- The core knows nothing about any one kind of work. The extension points below add that.
- Version 0 has no web console, cost tracking, merge handling, remote agents or trust rules.

## Agents

sfy runs three kinds of agent session. Each session runs in its own pane.

- **The main agent** is the one the operator talks to. It creates tasks, prepares the tasks that others create, answers workers, and cancels tasks. It escalates to the operator when it cannot decide. sfy sets no limit on what it may decide.
- **A worker** does one task. It runs in the task's workspace and stays alive through the task's review rounds.
- **A reviewer** checks one result. sfy starts a fresh reviewer for each review round. The reviewer reads the task file and the result, and runs the checks itself, such as the tests and the build. It judges whether the result meets the task's goal, and its verdict decides whether the task is done. It never reads the worker's transcript, and it never takes the worker's summary as proof.

sfy starts each session with an identity in the environment: `SFY_AGENT` holds the session's name, and `SFY_TASK` holds the task id for workers and reviewers. The CLI reads these to know who is calling.

Session names are `main`, `worker-<task>` and `review-<task>-<round>`.

Only the main agent asks the operator questions. A worker or a reviewer with a question runs `sfy report blocked <question>`, and the main agent answers it or escalates it. So a worker or a reviewer that waits at its prompt always waits for a message.

The provider adapter learns each session's state from the agent CLI's own hooks. For Claude Code, the `Stop` hook tells sfy that a turn ended. The `Notification` hook of type `idle_prompt` tells sfy that a session waits for input, and it never sends a reminder. An open permission dialog sends no `idle_prompt`, and an interrupt at a permission prompt fires neither hook (measured with Claude Code 2.1.325). A provider without hooks relies on its prompt: the agent runs `sfy report` at the end of every turn.

sfy enforces the rule for workers and reviewers:

- Their settings deny the provider's tool for questions to the user.
- A hook reports a permission prompt to sfy, and the task's worker counts as blocked.
- When a turn ends without a `sfy report`, sfy sends the agent a note: "Your turn ended without a report. If you need an answer, run sfy report blocked <question>." After two such turns in a row, sfy stops the note and tells the main agent instead.

sfy ships a default prompt for each kind of agent: main, worker and reviewer. The prompt tells the agent which `sfy` commands it uses.

## Tasks

- A task file, `task.md`, holds a description and a goal. It may also name a repo, a check command, and paths that the worker must not edit.
- A task can deliver anything: a code change, a document, an answer. A reviewer decides when a task is done.
- A task on a repo gets its own git worktree on a new branch. A task with no repo gets an empty folder.
- sfy gives each task an id of the form `t<n>`, counting up from `t1`.

A task moves through these states:

| State | Meaning |
|---|---|
| `new` | The task waits for the main agent to prepare it. |
| `queued` | The task waits for a free worker slot. |
| `working` | A worker is doing the task. |
| `review` | A reviewer checks the worker's result. |
| `feedback` | The worker fixes the result after a failed review. The round shows how many reviews have run, for example `feedback 1` after the first failed review. |
| `done` | A reviewer passed the result. |
| `escalated` | The task waits for the operator. |
| `cancelled` | The main agent or the operator stopped the task. |

- A task that a program or the operator creates starts in `new`, and sfy tells the main agent. The main agent adds context, for example from its notes, and may replace the task file at any time before the task is done or cancelled. It then queues the task, cancels it, or escalates it.
- A task that the main agent creates starts in `queued`.
- sfy starts a worker for the oldest `queued` task when fewer than `max_workers` workers run. The default for `max_workers` is 2. After three failed attempts in a row to start a task's worker, the task moves to `escalated`.
- When a worker reports `done`, the task moves to `review`, and sfy starts a reviewer.
- When the reviewer's verdict is `pass`, the task moves to `done`, and sfy tells the main agent.
- When the verdict is `fail`, sfy sends the verdict to the worker, and the task moves to `feedback`. The round stays at the number of reviews that have run.
- After `review_rounds` failed reviews, the task moves to `escalated`. sfy first sends the verdict to the worker. The default for `review_rounds` is 3. The operator can set any number of 1 or more.
- When a task is `done` or `cancelled`, sfy ends its worker and reviewer sessions. It then checks the workspace. A clean worktree, found with `git status --porcelain`, or an empty folder is removed, and the branch stays. A worktree with uncommitted changes or untracked files stays, and so does a folder with files. sfy then tells the main agent that the workspace still holds work, and where it is.
- `sfy task resume` ends `escalated`. The task returns to the state it had before the escalation, with the same round. sfy tells its worker to go on, and its reviewer too when the task returns to `review`. A task that escalated after its last failed review returns to `feedback`, so its worker fixes the result and gets one more review round. The command fails for a task in any other state.
- sfy does not restart a worker that ended on its own. It tells the main agent, which decides what happens to the task.

## State

- One long-running sfy process is the only writer of sfy state. The CLI sends each command to that process over a Unix socket. Agents never write state files.
- The state folder is `~/.sfy` by default. `SFY_HOME` overrides it.
- The store is plain files behind one Go interface, so another store can replace it later.
- Each task has a folder, `tasks/<id>/`, with `task.md`, its state, its verdicts and its messages. The state includes the task's workspace folder, which is empty after sfy removed or kept the workspace, and the worker's last progress report.
- sfy rewrites `status.md` on every state change. It lists each task with its title, state, round, agents and the time of its last change. Escalations stand at the top.
- The main agent keeps `notes.md` for information that spans tasks, such as goals, the operator's decisions, open questions and lessons. The main agent is its only writer, and it edits the file directly. Workers and reviewers do not read it. The main agent copies what a task needs into the task file.
- When the main agent starts, it reads `notes.md` and `status.md` first. Its context holds nothing that these files lack, so sfy can restart it at any time.
- The socket file is readable only by the operator's user. It keeps other users out, but it does not separate the operator's own agents from each other, since they all run as that user.

## Messages

- A message is a file in the receiver's inbox inside the state folder. The receiver gets a short note in its session: "New message. Run: sfy inbox".
- The provider adapter delivers the note through the best channel that the provider has. Without a native channel, the adapter types the note into the pane once the session waits for input. In version 0, every adapter types the note.
- `sfy inbox` prints the receiver's unread messages and marks them read.
- While a message stays unread, sfy sends the note again every `resend_minutes`. The default is 5.
- An escalation shows in `status.md`. If the multiplexer's set has a `notify` template, sfy also runs it for the main agent's pane.

## Commands

The CLI is the interface for agents, for the operator and for other programs. A program such as a Jira watcher creates tasks with `sfy task new`, the same way the main agent does. Its tasks start in `new`.

| Command | Used by | What it does |
|---|---|---|
| `sfy up` | operator | Starts the sfy process and the main agent. Starts a new main agent if the old one died. Warns when the provider does not trust the sfy home. |
| `sfy task new` | main agent, operator, programs | Creates a task from a description, a goal and the optional fields. Prints the task id. A warning about an untrusted folder goes to standard error. |
| `sfy task edit <id> <file>` | main agent | Replaces the file of a task in any state except `done` and `cancelled`. The task keeps its id, state, round, source, progress and folder. Once the task has a workspace, an edit may not change `## Repo` or `## Protected`. A running worker gets a message to read the new file. A running reviewer keeps the old file, and the next round reads the new one. |
| `sfy task queue <id>` | main agent | Moves a `new` task to `queued`. |
| `sfy task list` | anyone | Lists the tasks and their states. |
| `sfy task show <id>` | anyone | Prints a task's file, state and verdicts. |
| `sfy task cancel <id>` | main agent, operator | Marks a task `cancelled`. |
| `sfy task resume <id>` | main agent, operator | Returns an `escalated` task to the state it had before, and tells its worker or reviewer to go on. |
| `sfy send <agent> <text>` | main agent | Sends a message to an agent. |
| `sfy report done <text>` | worker | Reports the result and starts the review. |
| `sfy report blocked <text>` | worker, reviewer | Tells the main agent that the agent cannot go on. |
| `sfy report progress <text>` | worker | Records progress in the task's state. |
| `sfy verdict pass\|fail <text...>` | reviewer | Records the verdict. The verdict is the text after `pass` or `fail`, or standard input with `-`. |
| `sfy inbox` | any agent | Prints unread messages and marks them read. |
| `sfy escalate <id> <text>` | main agent | Moves a task to `escalated` and tells the operator. |
| `sfy status` | anyone | Prints `status.md`. |

Three internal commands do not appear in prompts:

- `sfy serve` runs the sfy process. `sfy up` starts it.
- `sfy run [-i] <envfile> -- <command...>` starts every agent session and the sfy process. It reads the environment from the file, deletes the file, and replaces itself with the command. With `-i` the command sees only the file's variables. Without it, they overlay the current environment. This keeps variable values, such as tokens, out of the process's argv.
- `sfy hook <event>` is the command that an agent CLI's hooks run, for example when a turn ends or a permission prompt opens.

A command that an agent kind may not run fails with an error that names the command it may run instead.

## Permissions

- Workers and reviewers run in the provider's sandbox. Writes reach only the workspace and the paths in `allow_write`. Reads skip the paths in `deny_read`.
- The sandbox leaves the network open. It runs no command outside the sandbox. sfy refuses to start a worker or a reviewer where the sandbox is unavailable. On macOS the sandbox lets commands reach the system TLS trust service, so Go tools can verify certificates.
- A worker or a reviewer never waits on a permission prompt. sfy's permission hook denies the call, gives the agent a reason, and records the denial in `sfy.log`.
- Workers and reviewers get a clean environment. It holds the `SFY_*` variables, the basic shell variables, the provider's own variables and the names in `pass_env`.
- Workers and reviewers run their sandboxed commands without an allow list. The `allow` setting is the main agent's allow list.
- Workers may read and create pull requests and issues through gh, and push only the task branch. They may not merge, close, edit, comment or review. Reviewers may view, diff and check pull requests. Reviewers may not push. These commands run outside the sandbox.
- Workers and reviewers run `sfy` commands outside the sandbox, because the sandbox blocks the sfy socket. They pass text to sfy only on standard input, so sfy reads no file for them.
- The operator trusts the sfy home and each repo once, by hand, in the provider. sfy only warns about an untrusted folder and never changes the provider's own config. A sfy home equal to $HOME always warns, because Claude Code keeps trust for $HOME in memory only.
- Deny rules protect the paths that the task file names, in every mode.
- Reviewers may run commands in the sandbox, but their edit tools are off. The sandbox limits what a build can write.
- The sandbox covers shell commands. The provider's edit tools follow the permission rules.

## Configuration

`config.toml` in the state folder holds the settings:

| Setting | Default | Meaning |
|---|---|---|
| `review_rounds` | 3 | Failed reviews before a task moves to `escalated`. |
| `max_workers` | 2 | Workers that run at once. |
| `resend_minutes` | 5 | Minutes between notes for an unread message. |
| `provider` | `claude` | The agent provider adapter. |
| `multiplexer` | `tmux` | The name of the template set to use. |
| `models.main` | `opus` | The main agent's model. |
| `models.worker` | `sonnet` | The workers' model. |
| `models.reviewer` | `sonnet` | The reviewers' model. |
| `allow` | git, gh, gofmt, build, test | The main agent's allow list. |
| `deny_read` | `~/.ssh`, `~/.aws`, `~/.config/gh`, `~/.gnupg`, `~/.netrc`, `~/.docker`, `~/.kube`, `~/.claude.json` | Paths that the sandbox hides from workers and reviewers. An empty list means no path. |
| `allow_write` | The user cache folder and `~/go/pkg/mod`. On macOS also the per-user temp folder from `getconf DARWIN_USER_TEMP_DIR`, without its trailing slash | Paths outside the workspace that the sandbox lets workers and reviewers write. An empty list means no path. |
| `pass_env` | none | Names of environment variables that workers and reviewers keep. |

## Multiplexer templates

A template set lives under `[multiplexers.<name>]` in `config.toml`. Each template is one command line. sfy fills in `{name}`, `{dir}`, `{command}` and `{text}`, and quotes each value for the shell.

| Template | Required | What the command does |
|---|---|---|
| `start` | yes | Starts `{command}` in a new session called `{name}`, in the folder `{dir}`. |
| `send` | yes | Types `{text}` into session `{name}` and presses Enter. |
| `alive` | yes | Exits 0 if session `{name}` still runs. |
| `stop` | yes | Ends session `{name}`. |
| `notify` | no | Draws the operator's attention to session `{name}`, with `{text}` as the message. |
| `show` | no | Makes session `{name}` visible to the operator. sfy runs it after each successful `start`. A failure goes to `sfy.log` and does not fail the start. |

sfy does not ask the multiplexer whether an agent waits for input. The provider adapter tells it.

## Extending sfy

A use of sfy extends it at these points, without changes to the core:

- **Prompts.** A prompt file in the state folder's `prompts/` replaces the default prompt for that kind of agent.
- **Task files.** A task file may carry extra sections. sfy passes the whole file to the worker and the reviewer.
- **Programs.** Any program can create, list and cancel tasks through the CLI.
- **Providers.** A new provider is a new adapter that implements the Go interface.
- **Multiplexers.** A new multiplexer is a new template set in `config.toml`.
- **Configuration.** `config.toml` sets the models, the limits, the allow list and the sandbox paths.
- **Isolation.** A VM or a container for each task can replace the sandbox and its two path lists.

## Platforms

sfy runs on Linux and macOS, with tmux or Herdr.
