# simple factory spec

sfy runs coding agents as interactive sessions in terminal panes. The operator talks to one main agent. The main agent hands out tasks, workers do them, and a fresh reviewer session checks each result. Agents report to sfy by running its CLI.

sfy builds only this loop. A terminal multiplexer runs the panes, and an agent CLI runs the models. The core stays small, and each use extends it to fit its own needs.

This spec covers version 0.

## Scope

- sfy is written in Go and ships as one binary, `sfy`.
- The agent provider sits behind an adapter. Version 0 has one adapter, for Claude Code. The design allows more providers, and several in one sfy process.
- A multiplexer is a set of command templates in `config.toml`, with no Go code. Version 0 ships a set for tmux. A new multiplexer needs only a new set. `docs/herdr.md` holds an example set for Herdr.
- The core knows nothing about any one kind of work. The extension points below add that.
- Version 0 has no web console, cost tracking, remote agents or trust rules. sfy merges nothing itself. The main agent merges through the provider's tools.

## Agents

sfy runs three kinds of agent session. Each session runs in its own terminal. sfy runs the multiplexer's `show_main` template for the main agent and its `show` template for each worker and reviewer. Without `show_main`, the main agent uses `show`. A worker that stops at the provider's trust dialog needs the operator to attach to its session.

- **The main agent** is the one the operator talks to. It creates tasks, prepares the tasks that others create, answers workers, and cancels tasks. It escalates to the operator when it cannot decide. sfy sets no limit on what it may decide.
- **A worker** does one task. It runs in the task's workspace and stays alive through the task's review rounds.
- **A reviewer** checks one result. sfy starts a fresh reviewer for each review round. The reviewer reads the task file and the result, and runs the checks itself, such as the tests and the build. It judges whether the result meets the task's goal, and its verdict decides whether the task is approved. It never reads the worker's transcript, and it never takes the worker's summary as proof.

sfy starts each session with an identity in the environment: `SFY_AGENT` holds the session's name, and `SFY_TASK` holds the task id for workers and reviewers. The CLI reads these to know who is calling.

Session names are `main`, `worker-<task>` and `review-<task>-<round>`.

Only the main agent asks the operator questions. A worker or a reviewer with a question runs `sfy report blocked <question>`, and the main agent answers it or escalates it. So a worker or a reviewer that waits at its prompt always waits for a message.

The provider adapter learns each session's state from the agent CLI's own hooks. For Claude Code, the `Stop` hook tells sfy that a turn ended. The `Notification` hook of type `idle_prompt` tells sfy that a session waits for input, and it never sends a reminder. An open permission dialog sends no `idle_prompt`, and an interrupt at a permission prompt fires neither hook (measured with Claude Code 2.1.325). A provider without hooks relies on its prompt: the agent runs `sfy report` at the end of every turn.

sfy enforces the rule for workers and reviewers:

- Their settings deny the provider's tool for questions to the user.
- A hook reports a permission prompt to sfy, and the task's worker counts as blocked.
- When a turn ends without a `sfy report`, sfy sends the agent a note: "Your turn ended without a report. If you need an answer, run sfy report blocked <question>." After two such turns in a row, sfy stops the note and tells the main agent instead.

sfy ships a default prompt for each kind of agent: main, worker and reviewer. The prompt tells the agent which `sfy` commands it uses.

The worker prompt has two parts. The common part holds the rules for every task: the task file, the workspace, tools, protected paths, the `sfy report` commands, checks, the summary, failed reviews and the report that ends each turn. A worker on a task with no repo delivers its result as files in the workspace and leaves them there. The repo part holds the rules for git: commits, the task branch, push forms, the push after a passed review, `sfy report pushed` and pull requests. sfy appends the repo part after the common part only when the task file has a `## Repo` section. The common part never mentions git steps. The main and reviewer prompts have no parts.

## Tasks

- A task file, `task.md`, holds a description and a goal. It may also name a repo, a check command, and paths that the worker must not edit.
- A task can deliver anything: a code change, a document, an answer. A reviewer decides when a task is approved.
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
| `approved` | A reviewer passed the result. The task waits for the main agent to complete it, or to reopen it with `sfy task feedback`. |
| `done` | The main agent completed the task. |
| `escalated` | The task waits for the operator. |
| `cancelled` | The main agent or the operator stopped the task. |

- A task that a program or the operator creates starts in `new`, and sfy tells the main agent. The main agent adds context, for example from its notes, and may replace the task file at any time before the task is approved or cancelled. It then queues the task, cancels it, or escalates it.
- A task that the main agent creates starts in `queued`.
- sfy starts a worker for the oldest `queued` task when fewer than `max_workers` workers run. The default for `max_workers` is 2. After three failed attempts in a row to start a task's worker, the task moves to `escalated`.
- When a worker reports `done`, the task moves to `review`, and sfy starts a reviewer.
- When the reviewer's verdict is `pass` on a task with no repo, the task moves to `approved`. On a task with a repo, it moves to `approved` after the worker reports that it pushed, as the push rules below describe. sfy ends the worker and reviewer sessions, keeps the workspace, and tells the main agent: "Task <id> is approved. Merge the pull request of branch <branch>, then run: sfy task complete <id>". A task with no repo gets a note that says it has no pull request.. A task without a pull request, for example a report, is completed the same way. The main agent checks the result and runs the command.
- `sfy task feedback` sends feedback on a task in any state to the task's worker and records it on the task. The round and the `review_rounds` limit stay as they are, and the feedback does not count as a failed review. What happens depends on the state:
  - In `new` and `queued`, the state stays. The first message of the task's worker holds the feedback after the task file.
  - In `working` and `feedback`, the state stays. sfy sends the feedback to the running worker as a message.
  - In `review`, sfy ends the running reviewer without a verdict and sends the feedback to the worker. The task moves to `feedback`.
  - In `escalated`, the state stays. When `sfy task resume` returns the task, its worker gets the feedback with the note to go on. A task that resumes in `new` or `queued` gives it to its first worker.
  - In `approved`, `done` and `cancelled`, the task reopens and moves to `queued`. An archived task stops being archived. When a worker slot is free, sfy starts a new worker in the task's workspace. If sfy removed the workspace, sfy creates it again from the task's branch, or as a new empty folder for a task with no repo. The worker's first message holds the task file and the feedback. It says that the workspace and branch hold the earlier work, and that the branch may already have a pull request.
  - The worker then goes through the normal flow. The next reviewer gets the feedback text too and checks that the worker addressed it.
- A task with a repo pushes only after a review passes, in every round. A task with no repo has nothing to push and moves to `approved` on a passed review.
  - In every round, the worker commits its work on the task branch and does not push. It reports `done`, and a reviewer checks the committed branch, not a pull request. A failed review goes back to the worker as usual.
  - When the review passes, sfy keeps the worker running and tells it to push now and to open the pull request when the task file asks for one. The task stays in `review` until the worker runs `sfy report pushed`. Then the task moves to `approved`, sfy tells the main agent, and sfy ends the sessions.
  - The worker pushes to the branch and opens no second pull request while the task's pull request is open. When the pull request is merged or closed, or the task never had one, the worker opens a new one from the same branch.
  - Feedback that arrives while the worker waits to push ends the wait. The task moves to `feedback`.
- When the main agent runs `sfy task complete`, the task moves to `done`. The main agent merges, and a merge completes a task.
- When the verdict is `fail`, sfy sends the verdict to the worker, and the task moves to `feedback`. The round stays at the number of reviews that have run.
- After `review_rounds` failed reviews, the task moves to `escalated`. sfy first sends the verdict to the worker. The default for `review_rounds` is 3. The operator can set any number of 1 or more.
- When a task is `done` or `cancelled`, sfy ends any worker and reviewer sessions that still run. It then checks the workspace. A clean worktree, found with `git status --porcelain`, or an empty folder is removed, and the branch stays. A worktree with uncommitted changes or untracked files stays, and so does a folder with files. sfy then tells the main agent that the workspace still holds work, and where it is.
- `sfy task resume` ends `escalated`. The task returns to the state it had before the escalation, with the same round. sfy tells its worker to go on, and its reviewer too when the task returns to `review`. A task that escalated after its last failed review returns to `feedback`, so its worker fixes the result and gets one more review round. The command fails for a task in any other state.
- sfy does not restart a worker that ended on its own. It tells the main agent, which decides what happens to the task.

## State

- One long-running sfy process is the only writer of sfy state. The CLI sends each command to that process over a Unix socket. Agents never write state files.
- The state folder is `~/.sfy` by default. `SFY_HOME` overrides it.
- Several state folders can run side by side, each with its own `config.toml`, tasks, notes, sfy process and main agent. `SFY_HOME=<folder> sfy up` starts the one in `<folder>`.
- Each state folder has a prefix. The default folder `~/.sfy` has an empty prefix. Any other folder gets its base name without a leading dot, in lower case, with each character outside `a-z0-9-` replaced by `-`, and a final `-`. The folder `~/.sfy-work` gives `sfy-work-`.
- sfy puts the prefix in front of every session name that it gives to the terminal tool, such as `sfy-work-main`, `sfy-work-sfy` and `sfy-work-worker-t1`. It puts the prefix in worker branches too, as in `sfy/sfy-work-t1`. Agent names in messages, notes, `sfy send`, `status.md` and `SFY_AGENT` carry no prefix.
- The store is plain files behind one Go interface, so another store can replace it later.
- Each task has a folder, `tasks/<id>/`, with `task.md`, its state, its verdicts and its messages. The state includes the task's workspace folder, which is empty after sfy removed or kept the workspace, and the worker's last progress report.
- sfy rewrites `status.md` on every state change. It lists each task with its title, state, round, agents and the time of its last change. Escalated tasks stand above all others. Within each group, the task with the newest last change comes first, a task without a change time comes last, and the higher id comes first when two times are equal. It leaves out archived tasks.
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
| `sfy`, `sfy up` | operator | Both do the same. `sfy up` pairs with `sfy down`, and `sfy up` with an extra argument fails with the usage. Starts the sfy process and the main agent. Starts the main agent again if its session ended. The Claude provider resumes the main agent's last conversation with `claude --resume <id>`. The id comes from `sessions/main/conversation-id` in the state folder, which the first start writes through `claude --session-id <id>`. The resumed agent keeps its settings and system prompt flags, and its first message is the note "sfy started again. Read notes.md and status.md in <state folder> with the Read tool." If the id file is missing or invalid, or if the resumed session ends within two seconds, sfy starts a new conversation with a new id. The output names the main agent as "(resumed conversation)" or "(new conversation)". Workers and reviewers always start new conversations. When the main agent already runs, sfy runs the main agent's show again, so the operator gets it where they started sfy. It then prints "The main agent already runs and was shown." Warns when the provider does not trust the sfy home. When an agent runs it (`SFY_AGENT` set), it prints the usage and exits 1. |
| `sfy help`, `sfy --help` | all | Print the usage and exit 0. Any extra argument after a command that takes none is an error with the usage. |
| `sfy restart [--update]` | operator, main agent | With `--update`, it first reads the installed version with `brew list --cask --versions sfy`, runs `brew update` so that the tap lists the newest release, and runs `brew upgrade sfy`. It then reads the version again. It prints "Upgraded sfy from X to Y." when the version changed and "sfy X is already the newest version." when it did not. If brew is missing or a brew command fails, it prints brew's error, keeps the old process and exits with a non-zero code. Otherwise it continues in both cases. Stops the sfy process and starts it from the installed sfy binary. The sfy process shuts down on the hangup that the multiplexer sends, so it removes its socket and lock file. A running main agent keeps its process, terminal and chat. Workers and reviewers keep running, and their sfy commands reach the new process. Afterwards sfy sends the main agent one note, "sfy restarted." If the main prompt differs from the one in `main-prompt.md` in the state folder, the note also names that file, which sfy rewrites with the new prompt. sfy writes `main-prompt.md` each time it starts a main agent. Without a sfy process it acts like a plain `sfy`, and a main agent that sfy starts gets no note. It prints what it stopped, started and told. It does not build or install sfy, except for the brew upgrade of `--update`. The hook and allow-list paths that sfy writes into each agent's settings.json name the path that sfy started as, found on PATH, and keep a symlink such as `/opt/homebrew/bin/sfy` unresolved. When no such path matches the running binary, they name the `os.Executable` path. It fails when run by a worker or a reviewer. |
| `sfy down` | operator | Stops every agent session and the sfy process. It sends one request over the socket to the running sfy process. That process stops each session it knows, the main agent and the workers and reviewers of tasks that have a workspace, through the launcher and the template set that it loaded at start. A change to `config.toml` after the start does not affect it, so sfy down still stops the sessions of the old terminal tool. It stops only sessions that run. The process then answers and exits, and removes its socket as on a hangup. It prints what it stopped, for example "Stopped main, worker-t3 and the sfy process." Without a sfy process it prints "sfy is not running." and exits 0. If a stop fails, the sfy process keeps running and the command prints the error, so the operator can run it again. A worker, a reviewer and the main agent get an error and nothing stops, because the main agent would stop itself in mid-command. sfy down changes no task. A later plain `sfy` starts the sfy process and the main agent. A task in `working`, `review` or `feedback` whose worker was stopped is handled as a worker that ended on its own: the new sfy process tells the main agent once that the worker is gone, and the main agent decides what happens to the task. |
| `sfy task new` | main agent, operator, programs | Creates a task from a description, a goal and the optional fields. Prints the task id. A warning about an untrusted folder goes to standard error. |
| `sfy task edit <id> <file>` | main agent | Replaces the file of a task in any state except `approved`, `done` and `cancelled`. The task keeps its id, state, round, source, progress and folder. Once the task has a workspace, an edit may not change `## Repo` or `## Protected`. A running worker gets a message to read the new file. A running reviewer keeps the old file, and the next round reads the new one. |
| `sfy task queue <id>` | main agent | Moves a `new` task to `queued`. |
| `sfy task list [--archived]` | anyone | Lists the tasks and their states, without archived tasks. With `--archived`, lists only the archived tasks. |
| `sfy task show <id>` | anyone | Prints a task's file, state and verdicts. |
| `sfy task complete <id>` | main agent, operator | Moves an `approved` task to `done` and cleans up. Fails for a task in any other state. |
| `sfy task feedback <id> <text>` | main agent, operator | Sends feedback on a task in any state to its worker and records it on the task. A task in `approved`, `done` or `cancelled` reopens. The lifecycle above describes each state. `sfy task show` lists the feedback with the verdicts. |
| `sfy task archive <id>` | main agent, operator | Archives a `done` or `cancelled` task. The task keeps its state, files, verdicts and id, and its workspace and branch stay. Fails for any other state and for an archived task. `sfy task show` still prints it and says that it is archived. |
| `sfy task cancel <id>` | main agent, operator | Marks a task `cancelled`. Works in any state except `done` and `cancelled`. |
| `sfy task resume <id>` | main agent, operator | Returns an `escalated` task to the state it had before, and tells its worker or reviewer to go on. |
| `sfy send <agent> <text>` | main agent | Sends a message to an agent. |
| `sfy report done <text>` | worker | Reports the result and starts the review. |
| `sfy report blocked <text>` | worker, reviewer | Tells the main agent that the agent cannot go on. |
| `sfy report progress <text>` | worker | Records progress in the task's state. |
| `sfy report pushed <text>` | worker | Reports that the worker pushed the branch after a passed review of a task with a repo. Moves the task to `approved`. Fails when sfy asked for no push. |
| `sfy verdict pass\|fail <text...>` | reviewer | Records the verdict. The verdict is the text after `pass` or `fail`, or standard input with `-`. |
| `sfy inbox` | any agent | Prints unread messages and marks them read. |
| `sfy escalate <id> <text>` | main agent | Moves a task to `escalated` and tells the operator. |
| `sfy status` | anyone | Prints `status.md`. |

Three internal commands do not appear in prompts:

- `sfy serve` runs the sfy process. `sfy` and `sfy restart` start it.
- `sfy run [-i] <envfile> -- <command...>` starts every agent session and the sfy process. It reads the environment from the file, deletes the file, and replaces itself with the command. With `-i` the command sees only the file's variables. Without it, they overlay the current environment. This keeps variable values, such as tokens, out of the process's argv.
- `sfy hook <event>` is the command that an agent CLI's hooks run, for example when a turn ends or a permission prompt opens.

A command that an agent kind may not run fails with an error that names the command it may run instead.

## Permissions

- Workers and reviewers run in the provider's sandbox. Writes reach only the workspace and the paths in `allow_write`. Reads skip the paths in `deny_read`.
- The sandbox leaves the network open. It runs no command outside the sandbox. sfy refuses to start a worker or a reviewer where the sandbox is unavailable. On macOS the sandbox lets commands reach the system TLS trust service, so Go tools can verify certificates. On macOS sandboxed commands may use Unix sockets only under the sandbox's own temp folder.
- A worker or a reviewer never waits on a permission prompt. sfy's permission hook denies the call, gives the agent a reason, and records the denial in `sfy.log`.
- Workers and reviewers get a clean environment. It holds the `SFY_*` variables, the basic shell variables, the provider's own variables and the names in `pass_env`.
- Workers and reviewers run their sandboxed commands without an allow list. The `allow` setting is the main agent's allow list.
- Workers may read and create pull requests and issues through gh, and push only the task branch. They may edit the body and the title of a pull request with `gh pr edit`, and no other property such as reviewers, base branch, labels or milestone. They may not merge, close, comment or review. Reviewers may view, diff and check pull requests. Reviewers may not push. These commands run outside the sandbox.
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
| `show` | no | Makes session `{name}` visible to the operator. sfy runs it after each successful `start` of a worker or reviewer, and of the main agent when `show_main` is empty. A failure goes to `sfy.log` and does not fail the start. |
| `show_main` | no | Makes the main agent's session `{name}` visible to the operator. sfy runs it after the main agent starts, in place of `show`. An empty `show` with a `show_main` hides workers and reviewers. |

sfy does not ask the multiplexer whether an agent waits for input. The provider adapter tells it.

## Extending sfy

A use of sfy extends it at these points, without changes to the core:

- **Prompts.** A prompt file in the state folder's `prompts/` replaces the default prompt for that kind of agent. For a worker, `worker.md` replaces the common part and `worker-repo.md` replaces the repo part. The files that end in `.md` in `prompts/<kind>.d/` append to that kind's prompt in name order, each after a blank line, to the default and to a replacing file alike. A missing folder adds nothing, a file that cannot be read is an error, and other files are skipped. The main agent's recorded prompt includes the additions.
- **Task files.** A task file may carry extra sections. sfy passes the whole file to the worker and the reviewer.
- **Programs.** Any program can create, list and cancel tasks through the CLI.
- **Providers.** A new provider is a new adapter that implements the Go interface.
- **Multiplexers.** A new multiplexer is a new template set in `config.toml`.
- **Configuration.** `config.toml` sets the models, the limits, the allow list and the sandbox paths.
- **Isolation.** A VM or a container for each task can replace the sandbox and its two path lists.

## Platforms

sfy runs on Linux and macOS, with tmux, or with another multiplexer that has a template set. See `docs/herdr.md`.

docs/rex.md has an example config for Rex.

A pushed tag `v*` starts a release. GoReleaser builds sfy for darwin and linux on amd64 and arm64, publishes a GitHub release, and writes the Homebrew cask to `JacobJNilsson/homebrew-tap`. The binary reports the tag with `sfy --version`, and a build without the release flags reports `dev`.
