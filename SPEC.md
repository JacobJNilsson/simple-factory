# simple factory spec

sfy runs coding agents as interactive sessions in terminal panes. The operator talks to one main agent. The main agent hands out tasks, workers do them, and a fresh reviewer session checks each result. Agents report to sfy by running its CLI.

sfy builds only this loop. A terminal multiplexer runs the panes, and an agent CLI runs the models. The core stays small, and each use extends it to fit its own needs.

This spec covers version 0.

## Scope

- sfy is written in Go and ships as one binary, `sfy`.
- The agent provider sits behind an adapter. Version 0 has one adapter, for Claude Code. The design allows more providers, and several in one sfy process.
- A multiplexer is a set of command templates in `config.toml`, with no Go code. Version 0 ships a set for tmux. A new multiplexer needs only a new set. `docs/herdr.md` holds an example set for Herdr.
- The core knows nothing about any one kind of work. The extension points below add that.
- Version 0 has no web console, cost tracking, remote agents or trust rules. sfy integrates nothing itself. The main agent integrates an approved branch with plain git. sfy needs no other tool than git for this.

## Agents

sfy runs three kinds of agent session. Each session runs in its own terminal. sfy runs the multiplexer's `show_main` template for the main agent and its `show` template for each worker and reviewer. Without `show_main`, the main agent uses `show`. A worker that stops at the provider's trust dialog needs the operator to attach to its session.

- **The main agent** is the one the operator talks to. It creates tasks, prepares the tasks that others create, answers workers, and cancels tasks. It escalates to the operator when it cannot decide. sfy sets no limit on what it may decide.
- **A worker** does one task. It runs in the task's workspace and stays alive through the task's review rounds.
- **A reviewer** checks one result. sfy starts a fresh reviewer for each review round. The reviewer reads the task file and the result, and runs the checks itself, such as the tests and the build. It judges whether the result meets the task's goal, and its verdict decides whether the task is approved. It never reads the worker's transcript, and it never takes the worker's summary as proof.

sfy starts each session with an identity in the environment: `SFY_AGENT` holds the session's name, and `SFY_TASK` holds the task id for workers and reviewers. The CLI reads these to know who is calling. `SFY_TOKEN` holds the session's secret token, which proves the identity to the sfy process, as the permissions section describes.

Before it starts any session, sfy asks the provider to check that the session can run. The Claude provider checks that it finds `claude` on PATH or at `~/.local/bin/claude`, and then runs `~/.local/bin/claude` by that path when PATH lacks it. On Linux, for workers and reviewers, it also checks that `bwrap` and `socat` are on PATH and that `bwrap --unshare-user --unshare-net --ro-bind / / true` succeeds. macOS skips the sandbox check, and the main agent runs without the sandbox. A failed check starts no session. The error names the cause and the fix: the apt command or the AppArmor profile. A task whose worker fails this check moves to `escalated` at once, because a retry cannot help, and sfy tells the main agent and the operator. A failed reviewer start returns the error to the worker's `sfy report done`. The sfy process also logs a failed check to `sfy.log` when it starts.

Session names are `main`, `worker-<task>` and `review-<task>-<round>`.

Only the main agent asks the operator questions. A worker or a reviewer with a question runs `sfy report blocked <question>`, and the main agent answers it or escalates it. So a worker or a reviewer that waits at its prompt always waits for a message.

The provider adapter learns each session's state from the agent CLI's own hooks. For Claude Code, the `Stop` hook tells sfy that a turn ended. The `Notification` hook of type `idle_prompt` tells sfy that a session waits for input, and it never sends a reminder. The `UserPromptSubmit` hook tells sfy that a turn started, so the session is busy until the next `Stop` or `idle_prompt`. Without it, a turn that the operator starts would leave the session marked as waiting. An open permission dialog sends no `idle_prompt`, and an interrupt at a permission prompt fires neither hook (measured with Claude Code 2.1.325). A provider without hooks relies on its prompt: the agent runs `sfy report` at the end of every turn.

sfy enforces the rule for workers and reviewers:

- Their settings deny the provider's tool for questions to the user.
- A hook reports a permission prompt to sfy, and the task's worker counts as blocked.
- When a turn ends without a `sfy report`, sfy sends the agent a note: "Your turn ended without a report. If you need an answer, run sfy report blocked <question>." After two such turns in a row, sfy stops the note and tells the main agent instead.

sfy ships a default prompt for each kind of agent: main, worker and reviewer. The prompt tells the agent which `sfy` commands it uses.

The worker prompt has two parts. The common part holds the rules for every task: the task file, the workspace, tools, protected paths, the `sfy report` commands, checks, the summary, failed reviews and the report that ends each turn. A worker on a task with no repo delivers its result as files in the workspace and leaves them there. The repo part holds the rules for git: commits, the task branch, push forms, the push after a passed review and `sfy report pushed`. sfy appends the repo part after the common part only when the task file has a `## Repo` section. The common part never mentions git steps. The main and reviewer prompts have no parts.

## Tasks

- A task file, `task.md`, holds a description and a goal. It may also name a repo, a check command, a Conventional Commits type for its branch, paths that the worker must not edit, tasks that must finish first, and a hold on its push.
- The optional `## After` section lists task ids. Write one per line, or separate them with commas or spaces. A line may start with `- `. sfy starts the task's worker only when every listed task is `done`, and an archived `done` task counts. `sfy task new` and `sfy task edit` reject an id that names no task, the task's own id, and an id newer than the task. Only older ids are allowed, so a cycle cannot form. The section orders starts only. It sets no merge order and reaches no other state folder.
- A task can deliver anything: a code change, a document, an answer. A reviewer decides when a task is approved.
- A task on a repo gets its own git worktree on a new branch. A task with no repo gets an empty folder.
- **Branch names.** A task's branch is `<type>/<slug>`, as in `feat/organisation-locations`. Task ids restart at `t1` in every state folder, so an id in the name would collide when two sfy instances share a remote. sfy picks the name once, when it first starts the task's worker, and records it on the task as `branch` in `state.json`. Every later step uses the recorded name and never computes it again. This covers the worker's push permissions, feedback rounds, reopening and the approval note.
  - `<type>` is a Conventional Commits type: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `perf`, `ci` or `build`. The optional `## Type` section of the task file wins, and an unknown value is a task file error. Otherwise a leading type prefix in the title wins, as in `fix: Refuse packaging writes` or `feat(api): Add locations`. Otherwise sfy maps the first word of the title: `fix`, `fixes`, `repair` and `resolve` to `fix`, `document` and `docs` to `docs`, `refactor`, `simplify`, `extract` and `rename` to `refactor`, `test` and `tests` to `test`, `optimize` and `speed` to `perf`, `bump` and `clean` to `chore`. Any other title gives `feat`, or `chore` for a task with no repo.
  - `<slug>` comes from the title without its type prefix. sfy lowercases it, folds common Latin letters with marks to ASCII, and replaces every run of other characters with one `-`. A ticket key such as `ROR-1090` stays, in lower case. The slug has at most 50 characters and ends at a word boundary. A title with no ASCII letter or digit gives the slug `task`.
  - **Collisions.** Before sfy creates the branch, it checks the name in the repo and, with `git ls-remote --heads`, on every remote of the repo. If the name is taken, sfy tries `-2`, `-3` and so on. If a remote does not answer, the start fails like any other start failure and sfy tries again.
  - A task that started before this rule keeps its `sfy/<id>` branch, with the prefix of its state folder. sfy finds such a task by its workspace or its reopened state and records no new name for it.
  - `sfy task show` prints the branch of a task that has one. The approval note to the main agent names it too.
- sfy gives each task an id of the form `t<n>`, counting up from `t1`.

A task moves through these states:

| State | Meaning |
|---|---|
| `new` | The task waits for the main agent to prepare it. |
| `queued` | The task waits for a free worker slot. |
| `working` | A worker is doing the task. |
| `review` | A reviewer checks the worker's result. |
| `feedback` | The worker fixes the result after a failed review. The round shows how many reviews have run, for example `feedback 1` after the first failed review. |
| `held` | A reviewer passed the result of a task with `## Push` set to `hold`, and the push waits for the operator. The task needs no worker. |
| `approved` | A reviewer passed the result. The task waits for the main agent to complete it, or to reopen it with `sfy task feedback`. |
| `done` | The main agent completed the task. |
| `escalated` | The task waits for the operator. |
| `cancelled` | The main agent or the operator stopped the task. |

- A task that a program or the operator creates starts in `new`, and sfy tells the main agent. The main agent adds context, for example from its notes, and may replace the task file at any time before the task is approved or cancelled. It then queues the task, cancels it, or escalates it.
- A task that the main agent creates starts in `queued`.
- A `queued` task with an `## After` list waits while any listed task is not `done`. `status.md`, `sfy task list` and `sfy task show` show it as `queued (after t6, t8)`, with the ids that are not yet done. Other `queued` tasks may start ahead of it. When a listed task is `cancelled`, the waiting task can never start. sfy tells the main agent once, "Task <id> waits for <dep>, which is cancelled", and the task stays `queued` until the main agent edits or cancels it.
- sfy starts a worker for the oldest `queued` task that does not wait when fewer than `max_workers` workers run. The default for `max_workers` is 2. After three failed attempts in a row to start a task's worker, the task moves to `escalated`.
- When a worker reports `done`, the task moves to `review`, and sfy starts a reviewer.
- When the reviewer's verdict is `pass` on a task with no repo, the task moves to `approved`. On a task with a repo that has a remote, it moves to `approved` after the worker reports that it pushed, as the push rules below describe. On a task with a repo that has no remote, it moves to `approved` at once. sfy ends the worker and reviewer sessions, keeps the workspace, and tells the main agent. For a repo task the note names the branch, the repo folder, and whether the repo has a remote. For a task with no repo the note says so, and the main agent checks the result and runs `sfy task complete <id>`.
- The main agent integrates an approved branch of a repo task with `sfy task integrate <id>`. In the repo folder, sfy runs `git fetch` when the repo has a remote, then fast-forwards the default branch to its remote copy. The default branch is the one that `origin/HEAD` names, or the checked-out branch when the repo has no remote. The repo folder must have it checked out. sfy rebases the task branch onto the default branch, fast-forwards the default branch to the task branch with `git merge --ff-only`, and pushes the default branch when the repo has a remote. It then moves the task to `done`. If a step fails, for example on a rebase conflict or a rejected push, sfy aborts a stopped rebase, forces nothing, and leaves the task `approved`. The main agent then tells the operator.
- A repo can be set to no-merge, as the section Per-repo configuration describes. `sfy task integrate` then refuses, and its error names the branch. The error tells the main agent to leave the branch for the operator. The approval note says the same, and names the branch and the repo folder. The operator merges the branch by hand, and the main agent runs `sfy task complete <id>` when the operator says it is merged. The main agent's settings hold no deny rule for merging. They hold a `PreToolUse` hook on `Bash`, `sfy hook merge-guard`, that blocks `git push`, `git merge`, `git rebase` and `gh pr merge` when the target folder is in a no-merge repo. The hook reads `cd`, `git -C`, `git --work-tree` and the hook input's working folder, then looks the folder up in the `.sfy.toml` files. It blocks when the folder comes from a shell expansion, when a `.sfy.toml` file or the config cannot be read, and when its input is unreadable. It does not catch a subshell, a shell function, a script, `eval`, `bash -c`, an alias, `gh --repo` or `gh api`, a quoted path with a space, a git alias, or another tool that pushes. A main agent that registered itself has no sfy settings, so the hook does not guard it. Without a no-merge repo, the main agent's settings add no merge or push rule. `[session_rules.main]` `allow` adds permission rules, for example `Bash(gh pr merge *)`. sfy refuses to integrate when it cannot read the repo's `.sfy.toml` files, and the approval note then treats the repo as no-merge.
- The `## Repo` section of a task is a URL or an absolute path to a local git repo, which may have no remote. A URL gets a clone in the state folder. A local path is used as it is. In both cases the worker gets a worktree on a new branch. sfy finds out whether the repo has a remote with `git remote`.
- `sfy task feedback` sends feedback on a task in any state to the task's worker and records it on the task. The round and the `review_rounds` limit stay as they are, and the feedback does not count as a failed review. What happens depends on the state:
  - In `new` and `queued`, the state stays. The first message of the task's worker holds the feedback after the task file.
  - In `working` and `feedback`, the state stays. sfy sends the feedback to the running worker as a message.
  - In `review`, sfy ends the running reviewer without a verdict and sends the feedback to the worker. The task moves to `feedback`.
  - In `escalated`, the state stays. When `sfy task resume` returns the task, its worker gets the feedback with the note to go on. A task that resumes in `new` or `queued` gives it to its first worker.
  - In `approved`, `done` and `cancelled`, the task reopens and moves to `queued`. An archived task stops being archived. When a worker slot is free, sfy starts a new worker in the task's workspace. If sfy removed the workspace, sfy creates it again from the task's branch, or as a new empty folder for a task with no repo. The worker's first message holds the task file and the feedback. It says that the workspace and branch hold the earlier work, and that the branch may already be pushed or integrated.
  - The worker then goes through the normal flow. The next reviewer gets the feedback text too and checks that the worker addressed it.
- A task with a repo that has a remote pushes only after a review passes, in every round. A task with no repo, and a task on a repo with no remote, has nothing to push and moves to `approved` on a passed review.
  - In every round, the worker commits its work on the task branch and does not push. It reports `done`, and a reviewer checks the committed branch. A failed review goes back to the worker as usual.
  - When the review passes, sfy keeps the worker running and tells it to push now. The task stays in `review` until the worker runs `sfy report pushed`. Then the task moves to `approved`, sfy tells the main agent, and sfy ends the sessions.
  - Feedback that arrives while the worker waits to push ends the wait. The task moves to `feedback`.
- The optional `## Push` section holds one word, `hold`, and any other value is a task file error. It holds the push of that task until the operator releases it. Other tasks of the same repo push as usual, and a task without the section changes nothing.
  - **A hard hold.** While a task holds, its worker sessions get no push allow rule, and their settings deny every `git push`. A worker that ignores its prompt cannot push. Every worker start and restart builds its settings from the task, so a reopened worker holds too.
  - **The `held` state.** When the review passes on a task that holds and whose repo has a remote, sfy does not ask the worker to push. It moves the task to `held`, ends the worker and reviewer sessions, keeps the workspace, and tells the main agent: the branch name, the repo folder, and that the push waits for the operator. A new state fits better than `approved` with a note, because `approved` invites `sfy task integrate`, which would skip the hold. A task on a repo with no remote has nothing to push, so a hold changes nothing and the task moves to `approved`.
  - **Release.** `sfy task push <id>` moves a `held` task back to `review` with the push flag set. It starts a worker with the normal push allow rules and tells it to push. The task then goes on as after any passed review: `sfy report pushed` moves it to `approved`, and integrate or no-merge works as usual. The main agent runs `sfy task push` only when the operator says to push, and the main prompt says so.
  - **Feedback.** `sfy task feedback` on a `held` task reopens it and keeps the hold. After a release, feedback ends the running worker, since its session has the push rules, and the next worker starts without them.
- When the main agent runs `sfy task complete`, the task moves to `done`. A successful `sfy task integrate` moves the task to `done` too.
- When the verdict is `fail`, sfy sends the verdict to the worker, and the task moves to `feedback`. The round stays at the number of reviews that have run.
- After `review_rounds` failed reviews, the task moves to `escalated`. sfy first sends the verdict to the worker. The default for `review_rounds` is 3. The operator can set any number of 1 or more.
- When a task is `done` or `cancelled`, sfy ends any worker and reviewer sessions that still run. It then checks the workspace. A clean worktree, found with `git status --porcelain`, or an empty folder is removed, and the branch stays. A worktree with uncommitted changes or untracked files stays, and so does a folder with files. sfy then tells the main agent that the workspace still holds work, and where it is.
- `sfy task resume` ends `escalated`. The task returns to the state it had before the escalation, with the same round. sfy tells its worker to go on, and its reviewer too when the task returns to `review`. A task that escalated after its last failed review returns to `feedback`, so its worker fixes the result and gets one more review round. The command fails for a task in any other state.
- sfy does not restart a worker that ended on its own. It tells the main agent, which decides what happens to the task.

## State

- One long-running sfy process is the only writer of sfy state. The CLI sends each command to that process over a Unix socket. Agents never write state files.
- The state folder is `~/.sfy` by default. `SFY_HOME` overrides it.
- Several state folders can run side by side, each with its own tasks, notes, sfy process and main agent. A state folder is a factory. `SFY_HOME=<folder> sfy up` starts the one in `<folder>`.
- A factory belongs to a folder that the operator works in. sfy writes no file in that folder. The state of a factory that sfy creates lives in `~/.sfy/factories/<name>/`. `<name>` is the base name of the folder in lower case, with each character outside `a-z0-9-` replaced by `-`. A second folder with the same base name gets a suffix, as in `web-2`. The file `folder` in the state folder records the absolute path of the folder.
- Rules for the state folder of a command:
  1. `SFY_HOME`, when set. Agents get it from sfy, so they keep working unchanged.
  2. Otherwise the nearest registered factory folder at or above the current folder, as git finds a repo. The user's home folder is never a factory folder.
  3. Otherwise the default factory, `~/.sfy`.
- `sfy up` in the user's home folder starts the default factory, and the main agent keeps its working directory. In a factory folder or below one, it starts that factory or, when it runs, behaves as for a running sfy. Anywhere else it creates `~/.sfy/factories/<name>/` for the current folder and starts it. The main agent of such a factory works in the factory folder.
- `sfy status` and `sfy task list` print a first line, `Factory: <name> (<folder>)`, so the operator sees which factory answered. The default factory has the name `default` and its folder is `~/.sfy`.
- Every factory reads the operator's `~/.sfy/config.toml`. A `config.toml` in the factory's own state folder overrides it key by key and is optional.
- Each state folder has a prefix. The default folder `~/.sfy` has an empty prefix. Any other folder gets its base name without a leading dot, in lower case, with each character outside `a-z0-9-` replaced by `-`, and a final `-`. The folder `~/.sfy-work` gives `sfy-work-`.
- sfy puts the prefix in front of every session name that it gives to the terminal tool, such as `sfy-work-main`, `sfy-work-sfy` and `sfy-work-worker-t1`. It puts the prefix in the branch of a task that started before branches took the form `<type>/<slug>`, as in `sfy/sfy-work-t1`. New branches carry no prefix, because the collision check keeps them apart. Agent names in messages, notes, `sfy send`, `status.md` and `SFY_AGENT` carry no prefix.
- The store is plain files behind one Go interface, so another store can replace it later.
- Each task has a folder, `tasks/<id>/`, with `task.md`, its state, its verdicts and its messages. The state includes the task's workspace folder, which is empty after sfy removed or kept the workspace, and the worker's last progress report.
- sfy rewrites `status.md` on every state change. It lists each task with its title, state, round, agents and the time of its last change. It shows that time as `YYYY-MM-DD HH:MM` in the machine's local time zone and names the zone in the column header, as in `Last change (CET)`. Escalated tasks stand above all others. Within each group, the task with the newest last change comes first, a task without a change time comes last, and the higher id comes first when two times are equal. It leaves out archived tasks.
- The main agent keeps `notes.md` for information that spans tasks, such as goals, the operator's decisions, open questions and lessons. The main agent is its only writer, and it edits the file directly. Workers and reviewers do not read it. The main agent copies what a task needs into the task file.
- When the main agent starts, it reads `notes.md` and `status.md` first. Its context holds nothing that these files lack, so sfy can restart it at any time.
- The socket file is readable only by the operator's user. It keeps other users out, but it does not separate the operator's own agents from each other, since they all run as that user.

## Messages

- A message is a file in the receiver's inbox inside the state folder. The receiver gets a short note in its session: "New message. Run: sfy inbox".
- Every note that sfy gives an agent goes through one delivery step: a new-message note, a task note, a reminder and the restart note. The core asks the provider to deliver the note without typing into the input line. The core names no provider. When the provider cannot deliver, because it has no such channel, the session has no delivery set up, or delivery fails, the core types the note into the pane with the `send` template, as before. A note that the core types waits until the session waits for input. The core never types into a busy session, however long its turn runs, because the operator may be typing in that pane. Typing is the last resort for a session that waits and whose hook left the text unread.
- A busy session with a marker gets new messages queued in its note file at once, each message once. Claude Code runs the `FileChanged` hook and gives its output to the agent between tool calls or at the end of the turn, so a long tool call delays the text but loses nothing. The core waits for the hook for the whole turn and types the note only after the turn ended and the hook still left the text unread. Each turn end gives the hook a fresh wait of `note_fallback_after` first. The default of five minutes covers a long tool call and a slow hook.
- For a message, the core hands the provider the sender and full text of each unread message, one message at a time. The agent needs no `sfy inbox` call. A message stays unread until the session takes its text, so a message that never arrives still shows in `sfy inbox`. When the core types the note instead, the messages stay unread too. Any other note arrives as its note text.
- The Claude provider keeps one note file for each session, `sessions/<name>/note.md` in the state folder. Each entry has a header line, and an entry for a message names the message id. A `SessionStart` hook runs `sfy hook note-watch`, which writes the marker `sessions/<name>/hooks-ready`, creates the file and prints `watchPaths` for it. A `FileChanged` hook with `asyncRewake` runs `sfy hook note`. That command takes a lock and moves the file aside. It prints the plain notes and the texts of the messages that are still unread to stderr, marks those messages read and exits with code 2. Claude Code then gives the text to the agent as hook feedback and starts a turn, also when the session is idle. Delivery appends to the file under the same lock, so a note that arrives during a drain is never lost. Several notes before the agent wakes arrive together.
- A folder change, such as a Bash `cd`, makes Claude Code replace the `FileChanged` watch set with the `watchPaths` of the `CwdChanged` hooks. So the settings also hold a `CwdChanged` hook that runs `sfy hook note-watch`. The command reads `hook_event_name` from its input, names that event in its output and keeps `SessionStart` when the input has none. Writing the marker and the file again is harmless.
- The provider delivers to a running session only when its marker exists. Writing the settings for a new start removes the marker, so a session needs its own `SessionStart` hook to set it. A session that an older sfy started has settings without the note hooks, never sets the marker, and gets typed notes even when its session folder exists. sfy checks the marker at each delivery, not at start, because the settings file can change after the session started. A message that was resent or that `sfy inbox` already showed reaches the agent once, because the drain skips the entries of read messages and repeated ids. A race between a drain and `sfy inbox` can show one message twice. That is harmless, because the agent sees the same text.
- A note file that holds text while its session has no marker is stuck. Messages in it stay unread, so `sfy inbox` shows them. `sfy inbox` also takes the other stuck text and prints it after the messages. It leaves the file alone for a session with a marker, because the hook drains it, and for an agent with no session folder. A failed drain is logged and never fails `sfy inbox`. When the core types a note to a session with stuck text, it types a second note, "You have undelivered notes. Run: sfy inbox".
- Claude Code behavior that tests cannot check here: whether `FileChanged` fires again when `note.md` is recreated after a drain, and whether a resumed session runs `SessionStart` again. sfy relies on both. A resumed session starts with newly written settings and no marker, so it gets typed notes until its `SessionStart` hook runs.
- A registered main agent has no sfy session folder, because its hooks come from the user settings. It keeps the typed note.
- The role prompts tell each agent that sfy delivers notes and messages as hook feedback, that they come from sfy and not from the operator, and that the agent acts on them as on a typed note.
- `sfy inbox` prints the receiver's unread messages and marks them read. It also prints stuck note text, as described above.
- While a message stays unread, sfy sends the note again every `resend_minutes`. The default is 5.
- An escalation shows in `status.md`. If the multiplexer's set has a `notify` template, sfy also runs it for the main agent's pane.

## Commands

The CLI is the interface for agents, for the operator and for other programs. A program such as a Jira watcher creates tasks with `sfy task new`, the same way the main agent does. Its tasks start in `new`.

| Command | Used by | What it does |
|---|---|---|
| `sfy`, `sfy up` | operator | Both do the same. `sfy up` pairs with `sfy down`, and `sfy up` with an extra argument fails with the usage. Starts the sfy process and the main agent. Starts the main agent again if its session ended. When the main agent does not run and the template set has a `whoami` template that prints an address, the main agent runs in the terminal where the operator ran sfy. sfy starts the sfy process in the background, writes the address to `sessions/main/address` in the state folder, runs `show_main` with that address as `{name}`, and replaces its own process with the main agent's command. That is the command that a started session runs, with the same flags, settings, prompt and resume. For the session `main`, the launcher uses the recorded address as `{name}` in `send`, `notify`, `alive`, `stop`, `show` and `show_main`. When `alive` reports the address gone, sfy removes the record, so the next start runs in place again. A failed `show_main` prints a warning on stderr and the main agent still starts in place. An empty `show_main` falls back to `show`, as for any start. Without `whoami`, or when it fails or prints nothing, sfy starts the main agent through `start` and `show_main` as described below. `sfy register` makes an agent that already runs the main agent. When a registered main agent runs, a plain `sfy` does not run `show_main`, because the operator already works in its terminal, and prints "The registered main agent already runs." The Claude provider resumes the main agent's last conversation with `claude --resume <id>`. The id comes from `sessions/main/conversation-id` in the state folder, which the first start writes through `claude --session-id <id>`. The resumed agent keeps its settings and system prompt flags, and its first message is the note "sfy started again. Read notes.md and status.md in <state folder> with the Read tool." If the id file is missing or invalid, or if the resumed session ends within two seconds, sfy starts a new conversation with a new id. The output names the main agent as "(resumed conversation)" or "(new conversation)". Workers and reviewers always start new conversations. When the main agent already runs, sfy runs the main agent's show again, so the operator gets it where they started sfy. It then prints "The main agent already runs and was shown." Warns when the provider does not trust the sfy home. When an agent runs it (`SFY_AGENT` set, or a registered session), it prints the usage and exits 1. |
| `sfy register` | agent session that sfy did not start | Makes the agent session that runs it the main agent. It refuses, and changes nothing, when `alive` reports that a main agent runs, when the set has no `whoami` template that prints an address, and when it runs outside an agent session. Otherwise it starts the sfy process when that does not run, as `sfy up` does, and runs `whoami`. It writes the address to `sessions/main/address` and the registration to `sessions/main/registered.json`. The registration holds the session id and the working folder. The Claude provider takes the id from `CLAUDE_CODE_SESSION_ID`, and the core asks the provider for the current session's id. sfy writes `main-prompt.md` as for a started main agent. It then prints one line that tells the agent to follow the text below as its role, the main prompt with the additions, and the line that names the state folder. The agent reads the prompt as a tool result, so it is not a system prompt and a compaction may drop it. It refuses for a started agent (`SFY_AGENT` set). The registered conversation is never resumed. |
| `sfy hooks install` | operator | Adds the `Stop`, `PermissionRequest` and `Notification` (`idle_prompt`) hooks to the user's Claude Code settings, `~/.claude/settings.json` or `$CLAUDE_CONFIG_DIR/settings.json`. Each runs `<sfy> hook --user <event>`. It keeps every other key and hook, rewrites an older sfy hook that names another binary path, and changes nothing when the hooks are in place. It prints what it changed, and it fails on a file that is not a JSON object without touching it. The file loses its key order, because sfy writes it as sorted JSON. |
| `sfy help`, `sfy --help` | all | Print the usage and exit 0. Any extra argument after a command that takes none is an error with the usage. |
| `sfy restart [--update]` | operator, main agent | With `--update`, it first picks the install method from the path of the running binary. A binary under a Homebrew `Cellar` or `Caskroom` folder, after symlinks resolve, is a brew install, and no brew binary is needed for that decision. A brew install reads the installed version with `brew list --cask --versions sfy`, runs `brew update` so that the tap lists the newest release, runs `brew upgrade sfy`, and reads the version again. If brew is missing or a brew command fails, it prints brew's error, keeps the old process and exits with a non-zero code. Any other install is a release binary. It reads the newest tag from the redirect of `https://github.com/JacobJNilsson/simple-factory/releases/latest`, without the `gh` CLI. When the tag matches `sfy --version`, nothing changes. Otherwise it downloads `sfy_<version>_<os>_<arch>.tar.gz` for the running system and `checksums.txt`, and checks the SHA-256 sum. It writes the new binary next to the running one and renames it over the running one, so the replacement is atomic. It never calls sudo. When the binary's folder is not writable, it keeps the verified binary in a temporary folder, prints `sudo install -m 0755 <downloaded file> <binary path>` for the operator, keeps the old process and exits with a non-zero code. A network, checksum or archive failure also keeps the old binary and the old process and exits with a non-zero code. Both methods print "Upgraded sfy from X to Y." when the version changed and "sfy X is already the newest version." when it did not. Otherwise it continues in both cases. Stops the sfy process and starts it from the installed sfy binary. The sfy process shuts down on the hangup that the multiplexer sends, so it removes its socket and lock file. A running main agent keeps its process, terminal and chat. Workers and reviewers keep running, and their sfy commands reach the new process. Afterwards sfy sends the main agent one note, "sfy restarted." If the main prompt differs from the one in `main-prompt.md` in the state folder, the note also names that file, which sfy rewrites with the new prompt. sfy writes `main-prompt.md` each time it starts a main agent. Without a sfy process it acts like a plain `sfy`, and a main agent that sfy starts gets no note. It never replaces its own process, so a main agent that is missing starts through `start` and `show_main`. It prints what it stopped, started and told. It does not build or install sfy, except for the upgrade of `--update`. The new sfy process gets the environment of the shell that ran `sfy restart`, filtered as for `sfy serve`. That includes the caller's `PATH`, so the multiplexer server's own `PATH` does not reach it. The hook and allow-list paths that sfy writes into each agent's settings.json name the path that sfy started as, found on PATH, and keep a symlink such as `/opt/homebrew/bin/sfy` unresolved. When no such path matches the running binary, they name the `os.Executable` path. It fails when run by a worker or a reviewer. |
| `sfy down` | operator | Stops every agent session and the sfy process. It sends one request over the socket to the running sfy process. That process stops each session it knows, the main agent and the workers and reviewers of tasks that have a workspace, through the launcher and the template set that it loaded at start. A change to `config.toml` after the start does not affect it, so sfy down still stops the sessions of the old terminal tool. It stops only sessions that run. The process then answers and exits, and removes its socket as on a hangup. It stops the main agent through `stop` with the recorded address when the main agent runs in the operator's terminal, and removes the record. That ends the terminal or session that the address names. It does not stop a registered main agent, because sfy did not start it. It clears the registration and the recorded address, leaves the agent running, and adds "Left main running and cleared its registration, because sfy did not start it." to what it prints. The agent's own commands then run as the operator's again. It prints what it stopped, for example "Stopped main, worker-t3 and the sfy process." Without a sfy process it prints "sfy is not running." and exits 0. If a stop fails, the sfy process keeps running and the command prints the error, so the operator can run it again. A worker, a reviewer and the main agent get an error and nothing stops, because the main agent would stop itself in mid-command. sfy down changes no task. A later plain `sfy` starts the sfy process and the main agent. A task in `working`, `review` or `feedback` whose worker was stopped is handled as a worker that ended on its own: the new sfy process tells the main agent once that the worker is gone, and the main agent decides what happens to the task. |
| `sfy task new` | main agent, operator, programs | Creates a task from a description, a goal and the optional fields. Prints the task id. A warning about an untrusted folder goes to standard error. |
| `sfy task edit <id> <file>` | main agent | Replaces the file of a task in any state except `approved`, `done` and `cancelled`. The task keeps its id, state, round, source, progress and folder. Once the task has a workspace, an edit may not change `## Repo` or `## Protected`. A running worker gets a message to read the new file. A running reviewer keeps the old file, and the next round reads the new one. |
| `sfy task queue <id>` | main agent | Moves a `new` task to `queued`. |
| `sfy task list [--archived]` | anyone | Lists the tasks and their states, without archived tasks. With `--archived`, lists only the archived tasks. |
| `sfy task show <id>` | anyone | Prints a task's file, state and verdicts. |
| `sfy task complete <id>` | main agent, operator | Moves an `approved` task to `done` and cleans up. Fails for a task in any other state. |
| `sfy task integrate <id>` | main agent, operator | Integrates the branch of an `approved` repo task into the default branch, as the state rules describe, then moves the task to `done` and cleans up. It refuses in a no-merge repo and names the branch. It fails for a task with no repo and for a task in any other state. |
| `sfy task push <id>` | main agent, operator | Releases the push hold of a `held` task and starts a worker that pushes the branch. Run it only on the operator's word. Fails for a task in any other state. |
| `sfy task feedback <id> <text>` | main agent, operator | Sends feedback on a task in any state to its worker and records it on the task. A task in `held`, `approved`, `done` or `cancelled` reopens. The lifecycle above describes each state. `sfy task show` lists the feedback with the verdicts. |
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
- `sfy hook --user <event>` is the same hook in the user's settings, which every session of the user reads. It acts as the main agent only when the hook input's `session_id` is the registered session. For any other session, for a worker or reviewer (which get their own hooks through `--settings`), and when no session is registered, it exits 0 at once, prints nothing and makes no socket call. It never decides a permission request for a session that is not the registered one.
- A registered agent's own `sfy` commands carry no `SFY_AGENT`. The CLI asks the provider for the current session's id, and when it equals the registered id, the command runs as `main`. The commands that only the operator may run, `sfy down`, `sfy up` and `sfy hooks install`, refuse for it.

A command that an agent kind may not run fails with an error that names the command it may run instead.

## Permissions

- Workers and reviewers run in the provider's sandbox. Writes reach only the workspace and the paths in `allow_write`. Reads skip the paths in `deny_read`.
- The sandbox leaves the network open. It runs no command outside the sandbox. sfy refuses to start a worker or a reviewer where the sandbox is unavailable. On macOS the sandbox lets commands reach the system TLS trust service, so Go tools can verify certificates. On macOS sandboxed commands may use Unix sockets only under the sandbox's own temp folder.
- A worker or a reviewer never waits on a permission prompt. sfy's permission hook denies the call, gives the agent a reason, and records the denial in `sfy.log`.
- Workers and reviewers get a clean environment. It holds the `SFY_*` variables, the basic shell variables, the provider's own variables and the names in `pass_env`.
- Workers and reviewers run their sandboxed commands without an allow list. The `allow` setting is the main agent's allow list.
- Workers may push only the task branch, with the two exact push forms, and not while the task holds its push. Reviewers may not push. Pushes run outside the sandbox. The main agent runs git through its allow list. Its merge guard hook refuses `git push`, `git merge`, `git rebase` and `gh pr merge` in a no-merge repo. sfy runs the integration itself.
- Extra rules for one kind of session come from `[session_rules.<kind>]` in `config.toml`. Its `allow` list adds permission rules to that kind's allow rules, and its `excluded_commands` list adds command patterns that run outside the sandbox. sfy adds no rule for any other tool by itself.
- Workers and reviewers run only the `sfy` commands that exact rules name, and these run outside the sandbox, because the sandbox blocks the sfy socket. The same patterns appear in the allow rules and in the sandbox's excluded commands. A worker gets `sfy report *` and `sfy inbox`. A reviewer gets `sfy verdict *`, `sfy report blocked *` and `sfy inbox`. Neither gets `sfy *`, because `sfy run` would start any command outside the sandbox. The main agent keeps `sfy *`. The session's own hooks run from the settings and need no rule.
- `sfy run`, `sfy serve`, `sfy register` and `sfy restart` refuse when `SFY_AGENT` names a worker or a reviewer. So does `sfy hook` with any event other than the ones that a session's own hooks use: `turn-ended`, `permission`, `idle`, `note` and `note-watch`. The environment can be faked, so the exact rules are the real barrier. The refusal is a second layer.
- A caller does not choose its identity with `SFY_AGENT`. When sfy starts a session, it writes a random token to `sessions/<name>/token` in the state folder, with mode 0600, and puts it in `SFY_TOKEN` in the session's environment. `sfy register` writes the token of the registered main agent to `sessions/main/token`, and the CLI reads it for a session that matches the registration. The CLI sends the token with each request. The sfy process finds the session that owns the token and takes the caller's name from it. A request with a name and no token, with a token that no session owns, or with a name that differs from the token's owner fails. A request with neither name nor token comes from the operator. The sandbox hides the `sessions` folder from workers and reviewers, so a session cannot read another session's token. Without the token of main, `SFY_AGENT=main` gives a worker nothing.
- A session that started under an sfy without tokens still runs after an upgrade and `sfy restart`, and it has no `SFY_TOKEN`. On start, `sfy serve` writes a random legacy token to `sessions/<name>/legacy-token`, with mode 0600, for each session folder that has no `token` file. The sfy process accepts a legacy token like a session token. The CLI reads it when the environment has no token. For `main` it reads the file only when `CLAUDE_CODE_SESSION_ID` equals `sessions/main/conversation-id`, so a worker that sets `SFY_AGENT=main` gets no token, because it cannot read that id. Starting a session removes its legacy token. The error for a missing token tells the caller to restart the session or run `sfy restart`. The sandbox hides the `sessions` folder from workers and reviewers. The CLI runs outside the sandbox, so a worker that sets `SFY_AGENT` to another worker or reviewer name could still read that legacy token. Only sessions that started before the upgrade have one, and restarting such a session removes the gap.
- Workers and reviewers pass text to sfy only on standard input, so sfy reads no file for them.
- The operator trusts the sfy home and each repo once, by hand, in the provider. sfy only warns about an untrusted folder and never changes the provider's own config. A sfy home equal to $HOME always warns, because Claude Code keeps trust for $HOME in memory only.
- Deny rules protect the paths that the task file names, in every mode.
- Reviewers may run commands in the sandbox, but their edit tools are off. The sandbox limits what a build can write.
- The sandbox covers shell commands. The provider's edit tools follow the permission rules.

## Configuration

`~/.sfy/config.toml` holds the settings. A `config.toml` in a factory's own state folder overrides them:

| Setting | Default | Meaning |
|---|---|---|
| `review_rounds` | 3 | Failed reviews before a task moves to `escalated`. |
| `max_workers` | 2 | Workers that run at once. |
| `resend_minutes` | 5 | Minutes between notes for an unread message. |
| `note_fallback_after` | `5m` | A Go duration. How long queued text may stay unread by the note hook before sfy types the note into an idle pane. |
| `provider` | `claude` | The agent provider adapter. |
| `multiplexer` | `tmux` | The name of the template set to use. |
| `models.main` | `opus` | The main agent's model. |
| `models.worker` | `sonnet` | The workers' model. |
| `models.reviewer` | `sonnet` | The reviewers' model. |
| `allow` | git, gofmt, build, test | The main agent's allow list. |
| `deny_read` | `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.netrc`, `~/.docker`, `~/.kube`, `~/.claude.json` | Paths that the sandbox hides from workers and reviewers. An empty list means no path. |
| `allow_write` | The user cache folder and `~/go/pkg/mod`. On macOS also the per-user temp folder from `getconf DARWIN_USER_TEMP_DIR`, without its trailing slash | Paths outside the workspace that the sandbox lets workers and reviewers write. An empty list means no path. |
| `pass_env` | none | Names of environment variables that workers and reviewers keep. |
| `no_merge` | `false` | When true, the main agent leaves approved branches for the operator. A per-repo file can set it per repo. |
| `draft_pr` | `false` | When true, the push note tells the worker that any PR it opens must be a draft. A per-repo file can set it per repo. |
| `session_rules.main`, `.worker`, `.reviewer` | none | Tables with an `allow` list and an `excluded_commands` list. sfy adds them to that kind's permission allow rules and sandbox excluded commands. |
| `session_settings.main`, `.worker`, `.reviewer` | none | Tables of extra keys. sfy adds them at the top level of that kind's settings file. Worker and reviewer files hold `promptSuggestionEnabled = false` unless a table sets it. |

### Session settings

A `session_settings.<kind>` table holds keys that sfy copies into the provider settings file of that kind of session. The provider decides what the keys mean, so sfy passes the values without a check. For example, this config stops notifications from workers and reviewers and leaves the main agent's alone:

```toml
[session_settings.worker]
preferredNotifChannel = "notifications_disabled"

[session_settings.reviewer]
preferredNotifChannel = "notifications_disabled"
```

sfy keeps its own keys. A table that sets `permissions`, `hooks` or `sandbox` is a config error that names the key. A kind other than `main`, `worker` or `reviewer` is a config error. Worker and reviewer settings files hold `"promptSuggestionEnabled": false` by default, which turns off Claude Code's prompt suggestions. Nobody reads those panes, so a suggestion would only look like a stuck message and cost a model request. A table can set the key to `true` to override the default. The main agent's file stays at Claude Code's default. Without the tables, the settings files hold only sfy's keys and that default.

### Per-repo configuration

A per-repo file holds the settings of one repo. A `.sfy.toml` file can also cover every repo below one folder. Both hold only per-repo settings. A new per-repo setting is one typed field in `repoSettings`.

| Setting | Default | Meaning |
|---|---|---|
| `no_merge` | the value in `config.toml`, `false` | When true, `sfy task integrate` refuses, and the main agent leaves the approved branch for the operator to merge by hand. |
| `draft_pr` | the value in `config.toml`, `false` | When true, the note that asks the worker to push also says that any PR it opens must be a draft, with `gh pr create --draft`. sfy opens no PR. |

- **Which folder counts.** For a task whose `## Repo` is a local path, the folder is that path. For a task whose `## Repo` is a URL, the folder is the clone that sfy keeps in `repos/` of the state folder, which exists after the first task of that URL starts. So a `.sfy.toml` in `repos/` or in the state folder covers URL repos. sfy never reads the task's worktree or branch, so a worker's commit cannot change settings before the operator merges it.
- **The `.sfy/` folder.** sfy reads `.sfy/config.toml` in that folder only, with no parent walk. The operator may keep `.sfy/` out of git with `core.excludesFile`, or commit it. In a URL clone, a committed `.sfy/` arrives with each fetch of the default branch, and the operator can also put an untracked one there by hand.
- **Lookup.** sfy reads `.sfy.toml` in that folder and in each parent folder up to `$HOME`. It stops at `$HOME`. A folder outside `$HOME` counts alone, with no parent. sfy reads the files again on each use, so an edit applies at once.
- **Precedence.** Key by key, the first file that sets the key wins. The order is `.sfy/config.toml` in the repo folder, then each `.sfy.toml` from the nearest to the farthest, then the factory `config.toml`, then `~/.sfy/config.toml`.
- **Refusals.** A key that sfy does not know is an error. So is a global-only key, such as `multiplexer`, `max_workers` or `multiplexers`. The error names the file and the key. A file that does not parse is also an error. While a file has an error, `sfy task integrate` refuses for the repos below it, and the push note adds no draft rule.

For example, this file at `~/Developer/work/.sfy.toml` makes the operator the only one who merges in every work repo below it:

```toml
no_merge = true
```

A file in one repo, such as `~/Developer/work/sandbox/.sfy.toml`, can set `no_merge = false` to allow the main agent to integrate there again.

## Multiplexer templates

A template set lives under `[multiplexers.<name>]` in `config.toml`. Each template is one command line. sfy fills in `{name}`, `{dir}`, `{command}`, `{text}` and `{home}`, and quotes each value for the shell. `{home}` is the absolute path of the running factory's state folder, and every template accepts it. sfy also sets `SFY_HOME` to that folder in the environment of every template command, also when `sfy up` finds the factory through the folder rules.

| Template | Required | What the command does |
|---|---|---|
| `start` | yes | Starts `{command}` in a new session called `{name}`, in the folder `{dir}`. |
| `send` | yes | Types `{text}` into session `{name}` and presses Enter. |
| `alive` | yes | Reports whether session `{name}` runs, with three results. Exit 0 means it runs. Exit 1 means it is gone. Any other exit code means the check failed, so sfy does not know. sfy never reads a failed check as gone. It logs the first failure of an agent, tells main after three ticks in a row ("sfy cannot tell whether worker-t1 runs: <error>"), and pauses the cleanup retries of a task while the check fails. |
| `stop` | yes | Ends session `{name}`. |
| `notify` | no | Draws the operator's attention to session `{name}`, with `{text}` as the message. |
| `show` | no | Makes session `{name}` visible to the operator. sfy runs it after each successful `start` of a worker or reviewer, and of the main agent when `show_main` is empty. A failure goes to `sfy.log` and does not fail the start. |
| `show_main` | no | Makes the main agent's session `{name}` visible to the operator. sfy runs it after the main agent starts, in place of `show`. On an in-place start, sfy runs it after it records the address and before it replaces its own process. An empty `show` with a `show_main` hides workers and reviewers. |
| `whoami` | no | Prints the address of the terminal that it runs in on one line. The address must work as `{name}` in the other templates. sfy runs it in the operator's terminal, so the terminal tool's own variables are set. |

A recorded address replaces `{name}` for the main agent only. The `sfy up` row in the command table describes it.

sfy does not ask the multiplexer whether an agent waits for input. The provider adapter tells it.

## Extending sfy

A use of sfy extends it at these points, without changes to the core:

- **Prompts.** A prompt file in the state folder's `prompts/` replaces the default prompt for that kind of agent. For a worker, `worker.md` replaces the common part and `worker-repo.md` replaces the repo part. The kinds are `main`, `worker`, `reviewer` and `worker-repo`. The files that end in `.md` in `prompts/<kind>.d/` append to that kind's prompt in name order, each after a blank line, to the default and to a replacing file alike. A missing folder adds nothing, a file that cannot be read is an error, and other files are skipped. The main agent's recorded prompt includes the additions.
- **Task files.** A task file may carry extra sections. sfy passes the whole file to the worker and the reviewer.
- **Programs.** Any program can create, list and cancel tasks through the CLI.
- **Providers.** A new provider is a new adapter that implements the Go interface.
- **Multiplexers.** A new multiplexer is a new template set in `config.toml`.
- **Configuration.** `config.toml` sets the models, the limits, the allow list and the sandbox paths.
- **Isolation.** A VM or a container for each task can replace the sandbox and its two path lists.

## Platforms

sfy runs on Linux and macOS, with tmux, or with another multiplexer that has a template set. See `docs/herdr.md`.

docs/rex.md has an example config for Rex.

A pushed tag `v*` starts a release. GoReleaser builds sfy for darwin and linux on amd64 and arm64, publishes a GitHub release, and writes the Homebrew cask to `JacobJNilsson/homebrew-tap`. Each release holds `sfy_<version>_<os>_<arch>.tar.gz` archives and `checksums.txt`, which `sfy restart --update` reads on a non-brew install. The binary reports the tag with `sfy --version`, and a build without the release flags reports `dev`.
