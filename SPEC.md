# factory spec

factory runs coding agents as interactive sessions in terminal panes. The operator talks to one main agent. The main agent hands out tasks, workers do them, and a fresh reviewer session checks each result. Agents report to factory by running its CLI.

factory builds only this loop. A terminal multiplexer runs the panes, and an agent CLI runs the models. The core stays small, and each use extends it to fit its own needs.

This spec covers version 0.

## Scope

- factory is written in Go and ships as one binary, `factory`.
- The agent provider sits behind an adapter. Version 0 has one adapter, for Claude Code. The design allows more providers, and several in one factory.
- A multiplexer is a set of command templates in `config.toml`, with no Go code. Version 0 ships sets for tmux and Herdr. A new multiplexer needs only a new set.
- The core knows nothing about any one kind of work. The extension points below add that.
- Version 0 has no web console, cost tracking, merge handling, remote agents or trust rules.

## Agents

factory runs three kinds of agent session. Each session runs in its own pane.

- **The main agent** is the one the operator talks to. It creates tasks, prepares the tasks that others create, answers workers, and cancels tasks. It escalates to the operator when it cannot decide. factory sets no limit on what it may decide.
- **A worker** does one task. It runs in the task's workspace and stays alive through the task's review rounds.
- **A reviewer** checks one result. factory starts a fresh reviewer for each review round. The reviewer reads the task file and the result, and runs the checks itself, such as the tests and the build. It judges whether the result meets the task's goal, and its verdict decides whether the task is done. It never reads the worker's transcript, and it never takes the worker's summary as proof.

factory starts each session with an identity in the environment: `FACTORY_AGENT` holds the session's name, and `FACTORY_TASK` holds the task id for workers and reviewers. The CLI reads these to know who is calling.

Session names are `main`, `worker-<task>` and `review-<task>-<round>`.

Only the main agent asks the operator questions. A worker or a reviewer with a question runs `factory report blocked <question>`, and the main agent answers it or escalates it. So a worker or a reviewer that waits at its prompt always waits for a message.

The provider adapter learns each session's state from the agent CLI's own hooks. For Claude Code, the `Stop` hook tells factory that a turn ended. A provider without hooks relies on its prompt: the agent runs `factory report` at the end of every turn.

factory enforces the rule for workers and reviewers:

- Their settings deny the provider's tool for questions to the user.
- A hook reports a permission prompt to factory, and the task's worker counts as blocked.
- When a turn ends without a `factory report`, factory sends the agent a note: "Your turn ended without a report. If you need an answer, run factory report blocked <question>."

Each kind of agent has a prompt file in the repo: `prompts/main.md`, `prompts/worker.md` and `prompts/reviewer.md`. The prompt tells the agent which `factory` commands it uses.

## Tasks

- A task file, `task.md`, holds a description and a goal. It may also name a repo, a check command, and paths that the worker must not edit.
- A task can deliver anything: a code change, a document, an answer. A reviewer decides when a task is done.
- A task on a repo gets its own git worktree on a new branch. A task with no repo gets an empty folder.
- factory gives each task an id of the form `t<n>`, counting up from `t1`.

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

- A task that a program or the operator creates starts in `new`, and factory tells the main agent. The main agent adds context, for example from its notes, and replaces the task file. It then queues the task, cancels it, or escalates it.
- A task that the main agent creates starts in `queued`.
- factory starts a worker for the oldest `queued` task when fewer than `max_workers` workers run. The default for `max_workers` is 2.
- When a worker reports `done`, the task moves to `review`, and factory starts a reviewer.
- When the reviewer's verdict is `pass`, the task moves to `done`, and factory tells the main agent.
- When the verdict is `fail`, factory sends the verdict to the worker, and the task moves to `feedback`. The round stays at the number of reviews that have run.
- After `review_rounds` failed reviews, the task moves to `escalated`. The default for `review_rounds` is 3. The operator can set any number of 1 or more.
- When a task is `done` or `cancelled`, factory ends its worker and reviewer sessions and removes the worktree. The branch stays.

## State

- One long-running factory process is the only writer of factory state. The CLI sends each command to that process over a Unix socket. Agents never write state files.
- The state folder is `~/.factory` by default. `FACTORY_HOME` overrides it.
- The store is plain files behind one Go interface, so another store can replace it later.
- Each task has a folder, `tasks/<id>/`, with `task.md`, its state, its verdicts and its messages. The state includes the task's workspace folder and the worker's last progress report.
- factory rewrites `status.md` on every state change. It lists each task with its title, state, round, agents and the time of its last change. Escalations stand at the top.
- The main agent keeps `notes.md` for information that spans tasks, such as goals, the operator's decisions, open questions and lessons. The main agent is its only writer, and it edits the file directly. Workers and reviewers do not read it. The main agent copies what a task needs into the task file.
- When the main agent starts, it reads `notes.md` and `status.md` first. Its context holds nothing that these files lack, so factory can restart it at any time.
- The socket file is readable only by the operator's user.

## Messages

- A message is a file in the receiver's inbox inside the state folder. The receiver gets a short note in its session: "New message. Run: factory inbox".
- The provider adapter delivers the note through the best channel that the provider has. Without a native channel, the adapter types the note into the pane once the session waits for input. In version 0, every adapter types the note.
- `factory inbox` prints the receiver's unread messages and marks them read.
- While a message stays unread, factory sends the note again every `resend_minutes`. The default is 5.
- An escalation shows in `status.md`. If the multiplexer's set has a `notify` template, factory also runs it for the main agent's pane.

## Commands

The CLI is the interface for agents, for the operator and for other programs. A program such as a Jira watcher creates tasks with `factory task new`, the same way the main agent does. Its tasks start in `new`.

| Command | Used by | What it does |
|---|---|---|
| `factory up` | operator | Starts the factory process and the main agent. Starts a new main agent if the old one died. |
| `factory task new` | main agent, operator, programs | Creates a task from a description, a goal and the optional fields. Prints the task id. |
| `factory task edit <id> <file>` | main agent | Replaces the file of a `new` task. |
| `factory task queue <id>` | main agent | Moves a `new` task to `queued`. |
| `factory task list` | anyone | Lists the tasks and their states. |
| `factory task show <id>` | anyone | Prints a task's file, state and verdicts. |
| `factory task cancel <id>` | main agent, operator | Marks a task `cancelled`. |
| `factory send <agent> <text>` | main agent | Sends a message to an agent. |
| `factory report done <text>` | worker | Reports the result and starts the review. |
| `factory report blocked <text>` | worker | Tells the main agent that the worker cannot go on. |
| `factory report progress <text>` | worker | Records progress in the task's state. |
| `factory verdict pass\|fail <file>` | reviewer | Records the verdict that the file holds. |
| `factory inbox` | any agent | Prints unread messages and marks them read. |
| `factory escalate <id> <text>` | main agent | Moves a task to `escalated` and tells the operator. |
| `factory status` | anyone | Prints `status.md`. |

Two internal commands do not appear in prompts:

- `factory serve` runs the factory process. `factory up` starts it.
- `factory hook <event>` is the command that an agent CLI's hooks run, for example when a turn ends or a permission prompt opens.

A command that an agent kind may not run fails with an error that names the command it may run instead.

## Permissions

- Workers run in the provider's mode that accepts edits without prompts. One default allow list lets them run git, gh and the repo's build and test commands.
- Deny rules protect the paths that the task file names, in every mode.
- Reviewers may run commands but may not edit files.

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
| `allow` | git, gh, build, test | The workers' allow list. |

## Multiplexer templates

A template set lives under `[multiplexers.<name>]` in `config.toml`. Each template is one command line. factory fills in `{name}`, `{dir}`, `{command}` and `{text}`, and quotes each value for the shell.

| Template | Required | What the command does |
|---|---|---|
| `start` | yes | Starts `{command}` in a new session called `{name}`, in the folder `{dir}`. |
| `send` | yes | Types `{text}` into session `{name}` and presses Enter. |
| `alive` | yes | Exits 0 if session `{name}` still runs. |
| `stop` | yes | Ends session `{name}`. |
| `notify` | no | Draws the operator's attention to session `{name}`, with `{text}` as the message. |

factory does not ask the multiplexer whether an agent waits for input. The provider adapter tells it.

## Extending factory

A use of factory extends it at these points, without changes to the core:

- **Prompts.** A prompt file in the state folder's `prompts/` replaces the default prompt for that kind of agent.
- **Task files.** A task file may carry extra sections. factory passes the whole file to the worker and the reviewer.
- **Programs.** Any program can create, list and cancel tasks through the CLI.
- **Providers.** A new provider is a new adapter that implements the Go interface.
- **Multiplexers.** A new multiplexer is a new template set in `config.toml`.
- **Configuration.** `config.toml` sets the models, the limits and the allow list.

## Platforms

factory runs on Linux and macOS, with tmux or Herdr.
