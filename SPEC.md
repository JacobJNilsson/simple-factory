# simple factory spec

sfy runs coding agents as interactive sessions in terminal panes. The operator talks to one lead. The lead hands out tasks, workers do them, and a fresh reviewer session checks each result. Agents report to sfy by running its CLI.

sfy builds only this loop. A terminal multiplexer runs the panes, and an agent CLI runs the models. The core stays small, and each use extends it to fit its own needs.

This spec covers version 0.

## Scope

- sfy is written in Go and ships as one binary, `sfy`.
- The agent provider sits behind an adapter. Version 0 has one adapter, for Claude Code. The design allows more providers, and several in one sfy process.
- A multiplexer is a set of command templates in `config.toml`, with no Go code. Version 0 ships a set for tmux. A new multiplexer needs only a new set. `docs/herdr.md` holds an example set for Herdr.
- The core knows nothing about any one kind of work. The extension points below add that.
- Version 0 has no web console, cost tracking, remote agents or trust rules. sfy integrates nothing itself. The lead integrates an approved branch with plain git. sfy needs no other tool than git for this.

## Agents

sfy runs three kinds of agent session. Each session runs in its own terminal. sfy runs the multiplexer's `show_lead` template for the lead and its `show` template for each worker and reviewer. Without `show_lead`, the lead uses `show`. A worker that stops at the provider's trust dialog needs the operator to attach to its session.

- **The lead** is the one the operator talks to. It creates tasks, prepares the tasks that others create, answers workers, and cancels tasks. It escalates to the operator when it cannot decide. sfy sets no limit on what it may decide.
- **A worker** does one task. It runs in the task's workspace and stays alive through the task's review rounds.
- **A reviewer** checks one result. sfy starts a fresh reviewer for each review round. The reviewer reads the task file and the result, and runs the checks itself, such as the tests and the build. When the task names a check command, sfy runs it before the review starts, and the reviewer need not run it again. It judges whether the result meets the task's goal, and its verdict decides whether the task is approved. It never reads the worker's transcript, and it never takes the worker's summary as proof.

sfy starts each session with an identity in the environment: `SFY_AGENT` holds the session's name, and `SFY_TASK` holds the task id for workers and reviewers. The CLI reads these to know who is calling. `SFY_TOKEN` holds the session's secret token, which proves the identity to the sfy process, as the permissions section describes.

Before it starts any session, sfy asks the provider to check that the session can run. The Claude provider checks that it finds `claude` on PATH or at `~/.local/bin/claude`, and then runs `~/.local/bin/claude` by that path when PATH lacks it. On Linux, for workers and reviewers, it also checks that `bwrap` and `socat` are on PATH and that `bwrap --unshare-user --unshare-net --ro-bind / / true` succeeds. macOS skips the sandbox check, and the lead runs without the sandbox. A failed check starts no session. The error names the cause and the fix: the apt command or the AppArmor profile. A task whose worker fails this check moves to `escalated` at once, because a retry cannot help, and sfy tells the lead and the operator. A failed reviewer start returns the error to the worker's `sfy report done`. The sfy process also logs a failed check to `sfy.log` when it starts.

Session names are `lead`, `worker-<task>` and `review-<task>-<round>`.

Only the lead asks the operator questions. A worker or a reviewer with a question runs `sfy report blocked <question>`, and the lead answers it or escalates it. So a worker or a reviewer that waits at its prompt always waits for a message.

The provider adapter learns each session's state from the agent CLI's own hooks. For Claude Code, the `Stop` hook tells sfy that a turn ended. The `Notification` hook of type `idle_prompt` tells sfy that a session waits for input, and it never sends a reminder. The `UserPromptSubmit` hook tells sfy that a turn started, so the session is busy until the next `Stop` or `idle_prompt`. Without it, a turn that the operator starts would leave the session marked as waiting. An open permission dialog sends no `idle_prompt`, and an interrupt at a permission prompt fires neither hook (measured with Claude Code 2.1.325). A provider without hooks relies on its prompt: the agent runs `sfy report` at the end of every turn.

sfy enforces the rule for workers and reviewers:

- Their settings deny the provider's tool for questions to the user.
- A hook reports a permission prompt to sfy, and the task's worker counts as blocked.
- When a turn ends without a `sfy report`, sfy sends the agent a note: "Your turn ended without a report. If you need an answer, run sfy report blocked <question>." After two such turns in a row, sfy stops the note and tells the lead instead.

sfy ships a default prompt for each kind of agent: lead, worker and reviewer. The prompt tells the agent which `sfy` commands it uses.

The worker prompt has two parts. The common part holds the rules for every task: the task file, the workspace, tools, protected paths, the `sfy report` commands, checks, the summary, failed reviews and the report that ends each turn. A worker on a task with no repo delivers its result as files in the workspace and leaves them there. The repo part holds the rules for git: commits, the task branch, push forms, the push after a passed review and `sfy report pushed`. sfy appends the repo part after the common part only when the task file has a `## Repo` section. The common part never mentions git steps. The lead and reviewer prompts have no parts.

## Tasks

- A task file, `task.md`, holds a description and a goal. It may also name a repo, a check command, which sfy runs before each review, a Conventional Commits type for its branch, paths that the worker must not edit, tasks that must finish first, and a hold on its push.
- The optional `## After` section lists task ids. Write one per line, or separate them with commas or spaces. A line may start with `- `. sfy starts the task's worker only when every listed task is `done`, and an archived `done` task counts. `sfy task new` and `sfy task edit` reject an id that names no task, the task's own id, and an id newer than the task. Only older ids are allowed, so a cycle cannot form. The section orders starts only. It sets no merge order and reaches no other factory folder.
- A task can deliver anything: a code change, a document, an answer. A reviewer decides when a task is approved.
- A task on a repo gets its own git worktree on a new branch. A task with no repo gets an empty folder.
- **Branch names.** A task's branch is `<type>/<slug>`, as in `feat/organisation-locations`. Task ids restart at `t1` in every factory folder, so an id in the name would collide when two sfy instances share a remote. sfy picks the name once, when it first starts the task's worker, and records it on the task as `branch` in `state.json`. Every later step uses the recorded name and never computes it again. This covers the worker's push permissions, feedback rounds, reopening and the approval note.
  - `<type>` is a Conventional Commits type: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `perf`, `ci` or `build`. The optional `## Type` section of the task file wins, and an unknown value is a task file error. Otherwise a leading type prefix in the title wins, as in `fix: Refuse packaging writes` or `feat(api): Add locations`. Otherwise sfy maps the first word of the title: `fix`, `fixes`, `repair` and `resolve` to `fix`, `document` and `docs` to `docs`, `refactor`, `simplify`, `extract` and `rename` to `refactor`, `test` and `tests` to `test`, `optimize` and `speed` to `perf`, `bump` and `clean` to `chore`. Any other title gives `feat`, or `chore` for a task with no repo.
  - `<slug>` comes from the title without its type prefix. sfy lowercases it, folds common Latin letters with marks to ASCII, and replaces every run of other characters with one `-`. A ticket key such as `ROR-1090` stays, in lower case. The slug has at most 50 characters and ends at a word boundary. A title with no ASCII letter or digit gives the slug `task`.
  - **Collisions.** Before sfy creates the branch, it checks the name in the repo and, with `git ls-remote --heads`, on every remote of the repo. If the name is taken, sfy tries `-2`, `-3` and so on. If a remote does not answer, the start fails like any other start failure and sfy tries again.
  - A task that started before this rule keeps its `sfy/<id>` branch, with the prefix of its factory folder. sfy finds such a task by its workspace or its reopened state and records no new name for it.
  - `sfy task show` prints the branch of a task that has one. The approval note to the lead names it too.
- sfy gives each task an id of the form `t<n>`, counting up from `t1`.

A task moves through these states:

| State | Meaning |
|---|---|
| `new` | The task waits for the lead to prepare it. |
| `queued` | The task waits for a free worker slot. |
| `working` | A worker is doing the task. |
| `review` | A reviewer checks the worker's result. |
| `feedback` | The worker fixes the result after a failed review. The round shows how many reviews have run, for example `feedback 1` after the first failed review. |
| `held` | A reviewer passed the result of a task with `## Push` set to `hold`, and the push waits for the operator. The task needs no worker. |
| `approved` | A reviewer passed the result. The task waits for the lead to complete it, or to reopen it with `sfy task feedback`. |
| `done` | The lead completed the task. |
| `escalated` | The task waits for the operator. |
| `cancelled` | The lead or the operator stopped the task. |

- A task that a program or the operator creates starts in `new`, and sfy tells the lead. The lead adds context, for example from its notes, and may replace the task file at any time before the task is approved or cancelled. It then queues the task, cancels it, or escalates it.
- A task that the lead creates starts in `queued`.
- A `queued` task with an `## After` list waits while any listed task is not `done`. `status.md`, `sfy task list` and `sfy task show` show it as `queued (after t6, t8)`, with the ids that are not yet done. Other `queued` tasks may start ahead of it. When a listed task is `cancelled`, the waiting task can never start. sfy tells the lead once, "Task <id> waits for <dep>, which is cancelled", and the task stays `queued` until the lead edits or cancels it.
- sfy starts a worker for the oldest `queued` task that does not wait when fewer than `max_workers` workers run. The default for `max_workers` is 2. After three failed attempts in a row to start a task's worker, the task moves to `escalated`.
- When a worker reports `done` on a task without `## Check`, the task moves to `review`, and sfy starts a reviewer.
- On a task with `## Check`, sfy first runs the check command with `sh -c` in the task's workspace, in a sandbox that has the worker's path lists. `sfy report done` answers at once, and the task keeps its state while the check runs. `status.md`, `sfy task list` and `sfy task show` show the state as `working (check runs)`. A second `sfy report done` during the run fails.
- When the check exits 0, the task moves to `review`, and sfy starts a reviewer. The reviewer's first message says that the check passed.
- When the check fails or runs longer than `check_timeout`, sfy starts no reviewer. It sends the worker a message with the command, the error, and the last 8 KiB of its combined output. The task moves to `feedback`, and the failure counts as a review round. When the round reaches `review_rounds`, the task moves to `escalated`, as after a failed review.
- sfy drops the result of a check when the task left `working` or `feedback` during the run, or when a new worker started. When an edit changed the check command during the run, sfy tells the worker to report done again.
- A restart of the sfy process stops a running check. The worker gets no result and runs `sfy report done` again.
- When the reviewer's verdict is `pass` on a task with no repo, the task moves to `approved`. On a task with a repo that has a remote, it moves to `approved` after the worker reports that it pushed, as the push rules below describe. On a task with a repo that has no remote, it moves to `approved` at once. sfy ends the worker and reviewer sessions, keeps the workspace, and tells the lead. For a repo task the note names the branch, the repo folder, and whether the repo has a remote. For a task with no repo the note says so, and the lead checks the result and runs `sfy task complete <id>`.
- The lead integrates an approved branch of a repo task with `sfy task integrate <id>`. In the repo folder, sfy runs `git fetch` when the repo has a remote, then fast-forwards the default branch to its remote copy. The default branch is the one that `origin/HEAD` names, or the checked-out branch when the repo has no remote. The repo folder must have it checked out. sfy rebases the task branch onto the default branch, fast-forwards the default branch to the task branch with `git merge --ff-only`, and pushes the default branch when the repo has a remote. It then moves the task to `done`. If a step fails, for example on a rebase conflict or a rejected push, sfy aborts a stopped rebase, forces nothing, and leaves the task `approved`. The lead then tells the operator.
- A repo can be set to no-merge, as the section Per-repo configuration describes. `sfy task integrate` then refuses, and its error names the branch. The error tells the lead to leave the branch for the operator. The approval note says the same, and names the branch and the repo folder. The operator merges the branch by hand, and the lead runs `sfy task complete <id>` when the operator says it is merged. The lead's settings hold no deny rule for merging. They hold a `PreToolUse` hook on `Bash`, `sfy hook merge-guard`, that blocks `git push`, `git merge`, `git rebase` and `gh pr merge` when the target folder is in a no-merge repo. The hook reads `cd`, `git -C`, `git --work-tree` and the hook input's `cwd`, then looks the folder up in the `.sfy.toml` files. It blocks when the folder comes from a shell expansion, when a `.sfy.toml` file or the config cannot be read, and when its input is unreadable. It does not catch a subshell, a shell function, a script, `eval`, `bash -c`, an alias, `gh --repo` or `gh api`, a quoted path with a space, a git alias, or another tool that pushes. A lead that registered itself has no sfy settings, so the hook does not guard it. Without a no-merge repo, the lead's settings add no merge or push rule. `[session_rules.lead]` `allow` adds permission rules, for example `Bash(gh pr merge *)`. sfy refuses to integrate when it cannot read the repo's `.sfy.toml` files, and the approval note then treats the repo as no-merge.
- The optional `## Branch` section holds one line: the name of an existing branch on the remote `origin` of the task's repo. It needs a `## Repo` section.
  - **Refusals.** `sfy task new` and `sfy task edit` refuse a branch that equals the default branch, and a branch that `origin` lacks. The default branch is the one that `origin`'s HEAD names, or the branch that a local repo folder has checked out. Once the task has a workspace, an edit may not change the section, like `## Repo`.
  - **Checkout.** The worker's worktree checks out that branch as a local branch that tracks `origin/<branch>`, in place of a new `<type>/<slug>` branch. The task's branch is that name. sfy records the sha of the remote branch at checkout.
  - **Push rules.** The worker may run `git push -u origin <branch>` and `git push --force-with-lease=<branch>:<sha> origin HEAD:<branch>`, with the recorded sha. It may not run a bare `--force-with-lease`, because git then trusts the remote-tracking ref that a fetch updates, and the lease would cover commits that the worker never saw. A moved remote branch makes the lease push fail, and the worker reports blocked. When the worker reports that it pushed, sfy records the new remote sha for later rounds.
  - **Hold and integrate.** `## Push` set to `hold` works as for any task. After approval, `sfy task integrate` refuses, also in a repo without no-merge, and the error says to run `sfy task complete <id>`. The approval note says that the worker pushed to the existing branch and to run `sfy task complete <id>`. The merge guard does not change.
- The `## Repo` section of a task is a URL or an absolute path to a local git repo, which may have no remote. A URL gets a clone in the factory folder. A local path is used as it is. In both cases the worker gets a worktree on a new branch. sfy finds out whether the repo has a remote with `git remote`.
- `sfy task feedback` sends feedback on a task in any state to the task's worker and records it on the task. The round and the `review_rounds` limit stay as they are, and the feedback does not count as a failed review. What happens depends on the state:
  - In `new` and `queued`, the state stays. The first message of the task's worker holds the feedback after the task file.
  - In `working` and `feedback`, the state stays. sfy sends the feedback to the running worker as a message.
  - In `review`, sfy ends the running reviewer without a verdict and sends the feedback to the worker. The task moves to `feedback`.
  - In `escalated`, the state stays. When `sfy task resume` returns the task, its worker gets the feedback with the note to go on. A task that resumes in `new` or `queued` gives it to its first worker. A task that escalated from `review` returns to `review` with the feedback. sfy gives the feedback to its worker, and to its reviewer unless the task waits for a push. sfy does not end the review or the push wait for it, as it does for feedback in `review`.
  - In `approved`, `done` and `cancelled`, the task reopens and moves to `queued`. An archived task stops being archived. When a worker slot is free, sfy starts a new worker in the task's workspace. If sfy removed the workspace, sfy creates it again from the task's branch, or as a new empty folder for a task with no repo. The worker's first message holds the task file and the feedback. It says that the workspace and branch hold the earlier work, and that the branch may already be pushed or integrated.
  - The worker then goes through the normal flow. The next reviewer gets the feedback text too and checks that the worker addressed it.
- A task with a repo that has a remote pushes only after a review passes, in every round. A task with no repo, and a task on a repo with no remote, has nothing to push and moves to `approved` on a passed review.
  - In every round, the worker commits its work on the task branch and does not push. It reports `done`, and a reviewer checks the committed branch. A failed review goes back to the worker as usual.
  - When the review passes, sfy keeps the worker running and tells it to push now. The task stays in `review` until the worker runs `sfy report pushed`. Then the task moves to `approved`, sfy tells the lead, and sfy ends the sessions.
  - Feedback that arrives while the worker waits to push ends the wait. The task moves to `feedback`.
- The optional `## Push` section holds one word, `hold`, and any other value is a task file error. It holds the push of that task until the operator releases it. Other tasks of the same repo push as usual, and a task without the section changes nothing.
  - **A hard hold.** While a task holds, its worker sessions get no push allow rule, and their settings deny every `git push`. A worker that ignores its prompt cannot push. Every worker start and restart builds its settings from the task, so a reopened worker holds too.
  - **The `held` state.** When the review passes on a task that holds and whose repo has a remote, sfy does not ask the worker to push. It moves the task to `held`, ends the worker and reviewer sessions, keeps the workspace, and tells the lead: the branch name, the repo folder, and that the push waits for the operator. A new state fits better than `approved` with a note, because `approved` invites `sfy task integrate`, which would skip the hold. A task on a repo with no remote has nothing to push, so a hold changes nothing and the task moves to `approved`.
  - **Release.** `sfy task push <id>` moves a `held` task back to `review` with the push flag set. It starts a worker with the normal push allow rules and tells it to push. The task then goes on as after any passed review: `sfy report pushed` moves it to `approved`, and integrate or no-merge works as usual. The lead runs `sfy task push` only when the operator says to push, and the lead's prompt says so.
  - **Feedback.** `sfy task feedback` on a `held` task reopens it and keeps the hold. After a release, feedback ends the running worker, since its session has the push rules, and the next worker starts without them.
- When the lead runs `sfy task complete`, the task moves to `done`. A successful `sfy task integrate` moves the task to `done` too.
- When the verdict is `fail`, sfy sends the verdict to the worker, and the task moves to `feedback`. The round stays at the number of reviews that have run.
- After `review_rounds` failed reviews, the task moves to `escalated`. sfy first sends the verdict to the worker. The default for `review_rounds` is 3. The operator can set any number of 1 or more.
- When a task is `done` or `cancelled`, sfy ends any worker and reviewer sessions that still run. It then checks the workspace. A clean worktree, found with `git status --porcelain`, or an empty folder is removed, and the branch stays. A worktree with uncommitted changes or untracked files stays, and so does a folder with files. sfy then tells the lead that the workspace still holds work, and where it is.
- `sfy task resume` ends `escalated`. The task returns to the state it had before the escalation, with the same round. sfy tells its worker to go on, and its reviewer too when the task returns to `review`. A task that escalated after its last failed review returns to `feedback`, so its worker fixes the result and gets one more review round. The command fails for a task in any other state.
- sfy does not restart a worker that ended on its own. It tells the lead, which decides what happens to the task.

### The task process

sfy runs every task through a process of agents, lists and branches. The rules above are the embedded default process. A factory that sets no `agents`, `lists` or `start` key runs it unchanged.

- **Agents.** An `[agents.<name>]` table defines one agent. Its `harness` is `claude`, `shell`, `lead` or `operator`, and it defaults to the `provider` key. A `claude` agent runs in a session that the launcher starts through the adapter of its harness. A `shell` agent runs its `run` commands with `sh -c` in the workspace, in the check sandbox, as the task's check runs. A `lead` agent notes the lead and waits for `sfy task complete` or `sfy task integrate`. An `operator` agent waits for `sfy task push`.
- **How an agent ends.** The `ends` key names the command that passes the agent. A `claude` agent with `ends = "done"` passes at `sfy report done`, with `ends = "pushed"` at `sfy report pushed`, and with `ends = "verdict"` at `sfy verdict pass`. It fails at `sfy verdict fail`. A `shell` agent passes when every command exits 0. An entry that is only `{task_check}` is skipped for a task without `## Check`, and an agent with nothing to run passes at once. `sfy report blocked`, `sfy report progress`, `sfy task cancel` and `sfy escalate` work for every agent.
- **Lists.** A `[lists.<name>]` table holds `agents`, a list of agent and list names in order. A list name in `agents` is a join, and sfy replaces it with that list's agents at every depth when it loads the config. A list ends with a fail at the first agent that fails, or with a pass after its last agent passes. `max_rounds` is the round at which a fail escalates, and it defaults to `review_rounds`.
- **Branches.** The `then` table of a list picks the next target. A target is a list name, `done`, `escalated`, or a table `{ list = "<name>", round = true }` that also counts a round. `pass` is a target, or an array of entries whose `if` facts must all hold, where the last entry has no `if`. The facts are `repo`, `remote`, `hold` and `branch`. `fail` is a target, or a table keyed by the name of the agent that failed. Without `then`, a pass goes to `done` and a fail to `escalated`. A branch to a list starts that list's first agent. A branch to `done` completes the task as `sfy task complete` does. In the default, the reviewer has `count_round`, so the start of each review counts a round.
- **The default.** In the operator's notation:

  ```
  build = [worker, check, reviewer]
    fail at check    -> build, a new round
    fail at reviewer -> build
    pass, remote and hold -> held
    pass, remote          -> ship
    pass                  -> land
  ship = [push, land]       a join, so [push, integrate]
  held = [release, ship]    a join, so [release, push, integrate]
  land = [integrate] -> done
  ```

  `worker` is a `claude` agent with `session = "task"`. `push` copies the worker with `like = "worker"`, so it shares the worker's session and its exact push rules. `check` is a `shell` agent that runs `{task_check}`. `reviewer` is a `claude` agent with `session = "round"` and `count_round`, so a fresh reviewer starts each round. `release` is the `operator` agent, and `integrate` the `lead` agent. `sfy config` prints the full tables.
- **States.** The task states stay as the table above names them. A task in `build/worker` is `working` or `feedback`, in `build/reviewer` it is `review`, and in `land/integrate` it is `approved`. A task in `ship/push` is `review` with the push flag, in `held/release` it is `held`, and in `held/push` it is `review` with the push and release flags. A task in a shell agent keeps the state of the agent before it, so a restart stops the run and an older sfy reads every `state.json`. `status.md` shows a running shell agent by its `show` key, where `{from}` stands for the state, as in `working (check runs)`.
- **Results.** sfy stores each agent's result in `tasks/<id>/results/<n>-<agent>.md`, where `<n>` counts up: the worker's report, the tail of a shell agent's output, a verdict, the text of `sfy report pushed`, and the text after the id of `sfy task push`. The verdict files stay, and `sfy task show` reads them as before. The reviewer gets the worker's report in its first message, and a failed agent's result goes to the worker as a message. In a `run` command, `{result}` names the file of the result before.
- **Placeholders.** A `run` command may hold `{task_check}`, the task's check command as it is. `{factory}`, `{task_file}`, `{result}` and `{drop}` each give a path as one quoted shell word. `{drop}` names a fresh folder, `tasks/<id>/drop/<n>-<agent>/`, which sfy adds to that run's write list. When the run ends, sfy gives each drop file in it to the lead as a message from `<agent>@<task id>`, such as `check@t12`. sfy ignores the drop's `From` and `Id` lines, and a drop of `Kind: task` is refused. A command gets no `SFY_*` variable.
- **Permissions of an agent.** The `permissions` table of a `claude` agent may add `allow` and `excluded_commands`. sfy adds them to `session_rules.<name>`, so both keys mean one list. Its other permission keys keep their default. The table of a `shell` agent may set `sandbox`, `allow_write` and `deny_read`. `sandbox = false` runs the commands with no sandbox. `allow_write` and `deny_read` replace the factory's list of that name for this agent, and the sandbox still hides the sfy socket and every `sessions` folder. A wide agent runs only scripts from the factory folder and treats the workspace as data. `sfy config` marks it as wide.
- **What a factory may change.** This version runs a narrow part of the schema, and `sfy up`, `sfy restart` and `sfy config` refuse the rest with an error that names the key:
  - A factory may add `shell` agents with `run`, `show` and the permissions above. Their `feedback`, `receives`, `slot` and `count_round` keep the default.
  - A shell agent runs only between `worker` and `reviewer` of a list that starts with `worker`. Such a list holds `worker`, then shell agents only, then `reviewer` last. The start list is one of them.
  - The fail targets of such a list are `done`, `escalated`, or a list that starts with `worker`. Its pass targets are `done`, `escalated`, or a list after the review.
  - A list after the review holds `release`, `push` and `integrate` in that order, each at most once, ends with `integrate`, and passes to `done`.
  - A `claude`, `lead` or `operator` agent keeps a default name and every key of its default agent. Only `model` of `worker` and `reviewer`, and the permissions `allow` and `excluded_commands`, may change.
  - A list may set `max_rounds`, and a shell branch is refused.

  The deep merge keeps every key of the default that a file leaves out, and a file's `agents` list replaces the list as a whole. A factory with a gate agent in `build` looks like this:

  ```toml
  [agents.gate]
  harness = "shell"
  run = ["sh {factory}/checks/gate.sh {task_file}"]
  show = "{from} (gate runs)"

  [lists.build]
  agents = ["worker", "gate", "check", "reviewer"]

  [lists.build.then]
  fail.gate = { list = "build", round = true }
  ```

- **Validation.** `sfy up`, `sfy restart` and `sfy config` check the process at each load. They refuse a name used for both an agent and a list, a name outside `[a-z][a-z0-9-]*`, an unknown agent or list in `agents`, a join that contains itself, a key that does not apply to the agent's harness, an unknown harness, a session harness with no adapter, an unknown target or fact, and a loop in which no branch counts a round and no agent waits. They also refuse a process that no longer has the position of an open task, and the error names the task and its position. `sfy restart` loads the config before it stops the old process, so a refused change keeps the factory running.

## State

- One long-running sfy process is the only writer of sfy state. The CLI sends each command to that process over a Unix socket. Agents never write state files.
- Each factory has a factory folder, which holds its definition and its state. The factory folder is `~/.sfy` by default. `SFY_HOME` overrides it.
- Several factory folders can run side by side, each with its own tasks, notes, sfy process and lead. `SFY_HOME=<folder> sfy up` starts the factory in `<folder>`.
- A factory belongs to a working folder, the folder that the operator works in and the lead runs in. sfy writes no file in the working folder. The factory folder of a factory that sfy creates is `~/.sfy/factories/<name>/`. `<name>` is the base name of the working folder in lower case, with each character outside `a-z0-9-` replaced by `-`. A second working folder with the same base name gets a suffix, as in `web-2`. The file `folder` in the factory folder records the absolute path of the working folder.
- Rules for the factory folder of a command:
  1. `SFY_HOME`, when set. Agents get it from sfy, so they keep working unchanged.
  2. Otherwise the factory of the nearest registered working folder at or above the current folder, as git finds a repo. The user's home folder is never a working folder.
  3. Otherwise the default factory, `~/.sfy`.
- `sfy up` in the user's home folder starts the default factory, and the lead keeps its working directory. In a working folder or below one, it starts that factory or, when it runs, behaves as for a running sfy. Anywhere else it creates `~/.sfy/factories/<name>/` for the current folder and starts it. The lead of such a factory works in the working folder.
- `sfy status` and `sfy task list` print a first line, `Factory: <name> (<folder>)`, so the operator sees which factory answered. The default factory has the name `default` and its folder is `~/.sfy`.
- sfy reads a factory's definition from two places, the machine layer `~/.sfy` and the factory folder. The section Factory definition gives the rules.
- sfy keeps the definition and the state of a factory folder apart by name:

  | Names | Owner | sfy reads them for behavior |
  |---|---|---|
  | `config.d/`, `config.toml`, `prompts/`, `skills/` | the factory, through a factory repo or by hand | yes, they are the definition |
  | `folder` | `sfy up`, or a factory repo's install script | yes, to find the working folder |
  | `tasks/`, `work/`, `repos/`, `sessions/`, `inbox/`, `next-id`, `status.md`, `lead-prompt.md`, the link `main-prompt.md`, `definition.toml`, `sfy.log`, `sfy.sock`, `sfy.lock`, `sfy.pid`, `drop/` | sfy, and programs write drops into `drop/` as the section Dropped messages describes | no, they are state |
  | `notes.md` | the lead | no, the lead reads it |
  | any other name, such as `checks/` | the factory | no |

- At start, the sfy process writes the flat result of the definition to `definition.toml`, atomically and with mode 0600. The file holds each setting as `sfy config` prints it and the SHA-256 sum of each prompt file of each kind. It holds the values of `session_settings.<kind>.env` as `<set>`. `sfy restart` compares the new flat result with it.
- sfy reserves the state names. A factory repo's install script must not write them. sfy refuses no other name and reads nothing from it. `sfy config` lists the other names as factory files. A link is fine anywhere in the definition, and sfy follows it when it reads the file.
- Each factory folder has a prefix. The default folder `~/.sfy` has an empty prefix. Any other folder gets its base name without a leading dot, in lower case, with each character outside `a-z0-9-` replaced by `-`, and a final `-`. The folder `~/.sfy-work` gives `sfy-work-`.
- sfy puts the prefix in front of every session name that it gives to the terminal tool, such as `sfy-work-lead`, `sfy-work-sfy` and `sfy-work-worker-t1`. It puts the prefix in the branch of a task that started before branches took the form `<type>/<slug>`, as in `sfy/sfy-work-t1`. New branches carry no prefix, because the collision check keeps them apart. Agent names in messages, notes, `sfy send`, `status.md` and `SFY_AGENT` carry no prefix.
- The store is plain files behind one Go interface, so another store can replace it later.
- Each task has a folder, `tasks/<id>/`, with `task.md`, its state, its verdicts and its messages. The state includes the task's workspace folder, which is empty after sfy removed or kept the workspace, and the worker's last progress report.
- sfy rewrites `status.md` on every state change. It lists each task with its title, state, round, agents and the time of its last change. It shows that time as `YYYY-MM-DD HH:MM` in the machine's local time zone and names the zone in the column header, as in `Last change (CET)`. Escalated tasks stand above all others. Within each group, the task with the newest last change comes first, a task without a change time comes last, and the higher id comes first when two times are equal. It leaves out archived tasks.
- The lead keeps `notes.md` for information that spans tasks, such as goals, the operator's decisions, open questions and lessons. The lead is its only writer, and it edits the file directly. Workers and reviewers do not read it. The lead copies what a task needs into the task file.
- When the lead starts, it reads `notes.md` and `status.md` first. Its context holds nothing that these files lack, so sfy can restart it at any time.
- The socket file is readable only by the operator's user. It keeps other users out, but it does not separate the operator's own agents from each other, since they all run as that user.

## Messages

- A message is a file in the receiver's inbox inside the factory folder. The receiver gets a short note in its session: "New message. Run: sfy inbox".
- Every note that sfy gives an agent goes through one delivery step: a new-message note, a task note, a reminder and the restart note. The core asks the provider to deliver the note without typing into the input line. The core names no provider, so the hook command turns the provider's permission payload into a one-line description for the core and turns the core's denial reason into the provider's reply. When the provider cannot deliver, because it has no such channel, the session has no delivery set up, or delivery fails, the core types the note into the pane with the `send` template, as before. A note that the core types waits until the session waits for input. The core never types into a busy session, however long its turn runs, because the operator may be typing in that pane. Typing is the last resort for a session that waits and whose hook left the text unread.
- A busy session with a marker gets new messages queued in its note file at once, each message once. Claude Code runs the `FileChanged` hook and gives its output to the agent between tool calls or at the end of the turn, so a long tool call delays the text but loses nothing. The `Stop` hook drains the note file as well. When text waits, the hook gives it to the agent as hook feedback and the turn goes on, so a message that arrives during a turn reaches the agent at the turn end at the latest, also when `FileChanged` missed it. The hook then tells sfy that the session is busy and not that the turn ended. The core waits for the hook for the whole turn and types the note only after the turn ended and the text stayed unread, which now covers only an idle session whose `FileChanged` hook missed. Each turn end gives the hook a fresh wait of `note_fallback_after` first. The default of five minutes covers a long tool call and a slow hook.
- For a message, the core hands the provider the sender and full text of each unread message, one message at a time. The agent needs no `sfy inbox` call. A message stays unread until the session takes its text, so a message that never arrives still shows in `sfy inbox`. When the core types the note instead, the messages stay unread too. Any other note arrives as its note text.
- The Claude provider keeps one note file for each session, `sessions/<name>/note.md` in the factory folder. Each entry has a header line, and an entry for a message names the message id. A `SessionStart` hook runs `sfy hook note-watch`, which writes the marker `sessions/<name>/hooks-ready`, creates the file and prints `watchPaths` for it. A `FileChanged` hook with `asyncRewake` runs `sfy hook note`. That command takes a lock, reads the file and empties it in place, so the watch on the file holds. It prints the plain notes and the texts of the messages that are still unread to stderr, marks those messages read and exits with code 2. Claude Code then gives the text to the agent as hook feedback and starts a turn, also when the session is idle. Delivery appends to the file under the same lock, so a note that arrives during a drain is never lost. Several notes before the agent wakes arrive together.
- A folder change, such as a Bash `cd`, makes Claude Code replace the `FileChanged` watch set with the `watchPaths` of the `CwdChanged` hooks. So the settings also hold a `CwdChanged` hook that runs `sfy hook note-watch`. The command reads `hook_event_name` from its input, names that event in its output and keeps `SessionStart` when the input has none. Writing the marker and the file again is harmless.
- The provider delivers to a running session only when its marker exists. Writing the settings for a new start removes the marker, so a session needs its own `SessionStart` hook to set it. A session that an older sfy started has settings without the note hooks, never sets the marker, and gets typed notes even when its session folder exists. sfy checks the marker at each delivery, not at start, because the settings file can change after the session started. A message that was resent or that `sfy inbox` already showed reaches the agent once, because the drain skips the entries of read messages and repeated ids. A race between a drain and `sfy inbox` can show one message twice. That is harmless, because the agent sees the same text.
- A note file that holds text while its session has no marker is stuck. Messages in it stay unread, so `sfy inbox` shows them. `sfy inbox` also takes the other stuck text and prints it after the messages. It leaves the file alone for a session with a marker, because the hook drains it, and for an agent with no session folder. A failed drain is logged and never fails `sfy inbox`. When the core types a note to a session with stuck text, it types a second note, "You have undelivered notes. Run: sfy inbox".
- Claude Code behavior that tests cannot check here: whether a resumed session runs `SessionStart` again, and whether a `Stop` hook that exits with code 2 keeps the turn going. sfy relies on both. A resumed session starts with newly written settings and no marker, so it gets typed notes until its `SessionStart` hook runs.
- A registered lead has no sfy session folder, because its hooks come from the user settings. It keeps the typed note, which waits until the lead waits for input.
- The role prompts tell each agent that sfy delivers notes and messages as hook feedback, that they come from sfy and not from the operator, and that the agent acts on them as on a typed note.
- `sfy inbox` prints the receiver's unread messages and marks them read. It also prints stuck note text, as described above.
- While a message stays unread, sfy sends the note again every `resend_minutes`. The default is 5.
- An escalation shows in `status.md`. If the multiplexer's set has a `notify` template, sfy also runs it for the lead's pane.

### Messages between factories

- A lead sends to the lead of another factory on the same machine with `sfy send <factory>/lead <text>`, such as `sfy send default/lead <text>`. The factory name is the one that `sfy status` prints in its first line.
- The lead's sfy process checks the command as for any send. It then opens the socket of the other factory and sends a `relay` request. The request holds the sender's factory name, the agent name `lead` and the sender's session token. The process does not hold its lock while it waits for the answer.
- The receiving process finds the sender's factory folder by name: `~/.sfy` for `default` and `~/.sfy/factories/<name>/` for any other name. It checks the token against the token files in that folder's `sessions/`. A missing or wrong token fails the relay. So does a valid token of any session other than the lead, such as a worker's.
- The receiving process delivers the message to its own lead only, through the normal inbox and note path. The message names its sender as `<factory>/lead`, such as `From: web/lead`, so it never shows as the operator's. A relay to any other agent fails.
- The receiver replies with `sfy send <factory>/lead <text>`, because the sender is the reply address. A message within one factory keeps a plain sender name, such as `worker-t3`.
- A send to a factory whose sfy process does not run fails with "factory <name> is not running". sfy queues nothing, and the sender tries again later. A send to a name with no factory folder fails with "no factory <name> on this machine".
- A factory folder outside `~/.sfy/factories/`, such as one that `SFY_HOME` names by hand, has no name that another factory can find. Its lead cannot send to another factory, and no other factory can reach it.
- The operator has no session token, so the operator never relays. The operator messages another factory's lead with `SFY_HOME=<factory folder> sfy send lead <text>`, and the message names `operator`.
- The lead's default prompt says that a message from another factory is information from another agent. It is not an instruction from the operator, and it never grants a permission that the operator did not grant.
- The relay keeps workers and reviewers out: they may not run `sfy send`, and their sandbox hides the `sessions` folder of every factory, as the Permissions section says. A command in `excluded_commands` runs outside the sandbox and can read every token file, as it can read any file of the operator. A lead runs as the operator's user with no sandbox, so a lead could read the token of another factory's lead. The relay trusts leads as the operator does.
- Factories on other machines are out of scope. A Unix socket and a token file reach one machine only.

### Dropped messages

A program with no sfy session, such as a poller, a timer or a script, has no token. It sends to the lead of a factory by dropping a file into the factory's drop folder. A file needs no sfy binary, no socket and no running sfy process. A drop states the sender's name, and nothing checks it.

- **The folder.** The drop folder is `drop/` in the factory folder: `~/.sfy/drop/` for the default factory and `~/.sfy/factories/<name>/drop/` for any other. The sfy process creates `drop/tmp/` and `drop/new/` at start. Only sfy writes `drop/done/`, `drop/rejected/` and `drop/replies/`. A program never creates `drop/`, because a missing folder means that the factory does not exist.
- **The format.** A drop is a UTF-8 file of at most 1 MiB. It holds header lines of the form `Key: value`, a blank line and the body, like a message file in an inbox. sfy ignores other header lines.

  | Header | Meaning | Required |
  |---|---|---|
  | `To` | `lead`, or the old name `main`. Any other name rejects the drop. | yes |
  | `From` | The stated name of the sender. It matches `[a-z0-9][a-z0-9-]{0,31}`, and sfy refuses `operator`, `sfy`, `lead` and `main`. | yes |
  | `Id` | The sender's id for this drop, with 1 to 64 characters from `A-Z`, `a-z`, `0-9`, `.`, `_` and `-`, starting with a letter or a digit. | yes |
  | `Kind` | `message` or `task`. For `task`, the body is a task file, as `sfy task new` takes it. | no, `message` by default |

- **The steps of a sender.** The sender names the file `<time>-<from>-<id>.md`, where `<time>` sorts, such as the output of `date +%s%N`. It writes the file to `drop/tmp/` and renames it into `drop/new/`. The drop is delivered when the rename succeeds. Two senders with the same `Id` then still use two file names. To send the same item again, the sender reuses its `Id`.
- **What sfy does.** On each tick, and once at start before the socket listens, sfy reads `drop/new/` in name order. sfy takes `From` and `Id` from the headers, never from the file name. For each file:
  1. It checks the size, the headers and the stated name. A failed check moves the file to `drop/rejected/`, writes the reason to a file beside it with the extra suffix `.why`, and logs it in `sfy.log`.
  2. When `drop/done/<from>/<id>.md` exists, the drop is a duplicate. sfy logs it and removes the file.
  3. For `Kind: message`, sfy stores the body in the lead's inbox with `From: drop:<from>`, and the lead gets it through the note path.
  4. For `Kind: task`, sfy creates a task as for `sfy task new` from the operator. The task starts in `new` with the source `drop:<from>`, and the lead gets the "New task" note. A body that is no valid task file rejects the drop.
  5. sfy moves the file to `drop/done/<from>/<id>.md`, which records the sender and the id.
- **Replies.** A sender has no inbox, so sfy writes replies into `drop/replies/<from>/` of the receiving factory. A reply has the format of a drop, with the headers `To`, `From`, `Id` and `Kind`, and the extra headers `Re`, `Task` and `State` where they apply. `Id` is sfy's own id for the reply, and the file is `<Id>.md`, so the names sort in write order. sfy writes each file through a temporary name that starts with a dot, so a sender skips such names. The sender reads the folder at its next run and deletes what it read. A sender that wants no replies ignores the folder. sfy never writes outside its own factory folder.
  - **A receipt.** Each drop that sfy takes gets `Kind: receipt` and `Re: <id>`, and for a task also `Task: <task id>`. A duplicate gets no receipt.
  - **A rejection.** A rejected drop whose `From` passed its check gets `Kind: rejected`, with `Re: <id>` when the id passed its check, and the reason as the body.
  - **The end of a task.** When a task with a `drop:` source reaches `done`, `cancelled` or `escalated`, sfy writes `Kind: status`, `Re: <id>`, `Task: <task id>` and `State: <state>`. The task records the drop's id in `state.json` as `source_id`.
  - **An answer from the lead.** The lead runs `sfy send drop:<from> <text>`, and sfy writes the text with `From: lead` and `Kind: message`. A name with no earlier drop fails, so the lead cannot fill a folder that no program reads.
  - A reply that sfy cannot write goes to `sfy.log` and changes nothing else.
- **Cleanup.** At start, sfy removes the files in `drop/done/` and `drop/replies/` that are older than 30 days, so a duplicate after that time delivers again. It also removes files in `drop/tmp/` that are older than one day, which a sender left after a crash. sfy never reads `drop/tmp/` otherwise. A stopped factory leaves drops in `drop/new/` until it starts.
- **The stated name.** sfy shows the sender as `drop:<from>`. No factory name and no session name holds a colon, so a stated name never looks like a checked sender, and a drop never shows as the operator. The note says "From drop:<from>, a name that the sender stated and nothing checked". `sfy task show` prints the line `Source: drop:<from>`, and `sfy task list` and `status.md` add "(from drop:<from>)" after the title.
- **What a drop may do.** A drop creates a message for the lead or a task in `new`, and nothing more. It never runs a command, never reaches a worker or a reviewer, and never queues, cancels or changes a task. Only the lead queues a task that a drop created. The lead's default prompt says that a drop is data from an unknown program, even when its name looks familiar.
- **The risk of a false name.** Any process of the operator's user can write a drop with any stated name. Workers, reviewers and checks cannot, because their sandboxes write only the workspace and `allow_write`, unless a factory adds a `drop/` folder to `allow_write`. A command in `excluded_commands` runs outside the sandbox and can write a drop.

## Commands

The CLI is the interface for agents and for the operator. A program such as a ticket watcher sends to the lead through the drop folder, as the section Dropped messages describes. Its tasks start in `new` and show the source `drop:<name>`. A program that runs `sfy task new` with no token counts as the operator.

| Command | Used by | What it does |
|---|---|---|
| `sfy`, `sfy up` | operator | Both do the same. `sfy up` pairs with `sfy down`, and `sfy up` with an extra argument fails with the usage. Starts the sfy process and the lead. Starts the lead again if its session ended. When the lead does not run and the template set has a `whoami` template that prints an address, the lead runs in the terminal where the operator ran sfy. sfy starts the sfy process in the background, writes the address to `sessions/lead/address` in the factory folder, runs `show_lead` with that address as `{name}`, and replaces its own process with the lead's command. That is the command that a started session runs, with the same flags, settings, prompt and resume. For the session `lead`, the launcher uses the recorded address as `{name}` in `send`, `notify`, `alive`, `stop`, `show` and `show_lead`. When `alive` reports the address gone, sfy removes the record, so the next start runs in place again. A failed `show_lead` prints a warning on stderr and the lead still starts in place. An empty `show_lead` falls back to `show`, as for any start. Without `whoami`, or when it fails or prints nothing, sfy starts the lead through `start` and `show_lead` as described below. `sfy register` makes an agent that already runs the lead. When a registered lead runs, a plain `sfy` does not run `show_lead`, because the operator already works in its terminal, and prints "The registered lead already runs." The Claude provider resumes the lead's last conversation with `claude --resume <id>`. The id comes from `sessions/lead/conversation-id` in the factory folder, which the first start writes through `claude --session-id <id>`. The resumed agent keeps its settings and system prompt flags, and its first message is the note "sfy started again. Read notes.md and status.md in <factory folder> with the Read tool." If the id file is missing or invalid, or if the resumed session ends within two seconds, sfy starts a new conversation with a new id. The output names the lead as "(resumed conversation)" or "(new conversation)". Workers and reviewers always start new conversations. When the lead already runs, sfy runs the lead's show again, so the operator gets it where they started sfy. It then prints "The lead already runs and was shown." Warns when the provider does not trust the working folder. When an agent runs it (`SFY_AGENT` set, or a registered session), it prints the usage and exits 1. |
| `sfy register` | agent session that sfy did not start | Makes the agent session that runs it the lead. It refuses, and changes nothing, when `alive` reports that a lead runs, when the set has no `whoami` template that prints an address, and when it runs outside an agent session. Otherwise it starts the sfy process when that does not run, as `sfy up` does, and runs `whoami`. It writes the address to `sessions/lead/address` and the registration to `sessions/lead/registered.json`. The registration holds the session id and the folder that the command ran in. The Claude provider takes the id from `CLAUDE_CODE_SESSION_ID`, and the core asks the provider for the current session's id. sfy writes `lead-prompt.md` as for a started lead. It then prints one line that tells the agent to follow the text below as its role, the lead's prompt with the additions, and the line that names the factory folder. The agent reads the prompt as a tool result, so it is not a system prompt and a compaction may drop it. It refuses for a started agent (`SFY_AGENT` set). The registered conversation is never resumed. |
| `sfy hooks install` | operator | Adds the `Stop`, `PermissionRequest`, `Notification` (`idle_prompt`) and `UserPromptSubmit` hooks to the user's Claude Code settings, `~/.claude/settings.json` or `$CLAUDE_CONFIG_DIR/settings.json`. Each runs `<sfy> hook --user <event>`. It keeps every other key and hook, rewrites an older sfy hook that names another binary path, and changes nothing when the hooks are in place. It prints what it changed, and it fails on a file that is not a JSON object without touching it. The file loses its key order, because sfy writes it as sorted JSON. |
| `sfy config` | operator, lead | Prints the flat result of the factory that the rules for the factory folder find, as the section Factory definition describes. It reads files only, needs no sfy process and creates no factory. It exits with a non-zero code when the definition does not load. An extra argument fails with the usage. It fails when run by a worker or a reviewer. |
| `sfy help`, `sfy --help` | all | Print the usage and exit 0. Any extra argument after a command that takes none is an error with the usage. |
| `sfy restart [--update]` | operator, lead | With `--update`, it first picks the install method from the path of the running binary. A binary under a Homebrew `Cellar` or `Caskroom` folder, after symlinks resolve, is a brew install, and no brew binary is needed for that decision. A brew install reads the installed version with `brew list --cask --versions sfy`, runs `brew update` so that the tap lists the newest release, runs `brew upgrade sfy`, and reads the version again. If brew is missing or a brew command fails, it prints brew's error, keeps the old process and exits with a non-zero code. Any other install is a release binary. It reads the newest tag from the redirect of `https://github.com/JacobJNilsson/simple-factory/releases/latest`, without the `gh` CLI. When the tag matches `sfy --version`, nothing changes. Otherwise it downloads `sfy_<version>_<os>_<arch>.tar.gz` for the running system and `checksums.txt`, and checks the SHA-256 sum. It writes the new binary next to the running one and renames it over the running one, so the replacement is atomic. It never calls sudo. When the binary's folder is not writable, it keeps the verified binary in a temporary folder, prints `sudo install -m 0755 <downloaded file> <binary path>` for the operator, keeps the old process and exits with a non-zero code. A network, checksum or archive failure also keeps the old binary and the old process and exits with a non-zero code. Both methods print "Upgraded sfy from X to Y." when the version changed and "sfy X is already the newest version." when it did not. Otherwise it continues in both cases. It loads the definition first, so a definition that does not load keeps the old process. It then compares the new flat result with `definition.toml`, before it stops anything, and prints one line per change. A setting's line names its key, the old value and the new value, as in `Setting models.worker changed from "sonnet" to "opus".`, with `unset` for a missing side. A prompt file's line names the kind, the file below `prompts/` and whether it was added, changed or removed, as in `The worker prompt file worker.d/writing.md was removed.` When nothing changed, it prints "The definition did not change since the last start." Without `definition.toml` it prints no report. A `definition.toml` that does not load prints a warning and does not stop the restart. Stops the sfy process and starts it from the installed sfy binary. The sfy process shuts down on the hangup that the multiplexer sends, so it removes its socket and lock file. A running lead keeps its process, terminal and chat. Workers and reviewers keep running, and their sfy commands reach the new process. Afterwards sfy sends the lead one note, "sfy restarted.", followed by the report lines on the same line. The new sfy process gives the note through the delivery step, so a note that sfy must type waits until the lead waits for input. It prints "The lead gets the note when it waits." when the note waits, and "Told the lead." otherwise. A lead that sfy has no hook state for gets the typed note after `resend_minutes`, unless a hook reported its turn as busy. If the lead's prompt differs from the one in `lead-prompt.md` in the factory folder, the note also names that file, which sfy rewrites with the new prompt. sfy writes `lead-prompt.md` each time it starts a lead. Without a sfy process it acts like a plain `sfy`, and a lead that sfy starts gets no note. It never replaces its own process, so a lead that is missing starts through `start` and `show_lead`. It prints what it stopped, started and told. It does not build or install sfy, except for the upgrade of `--update`. The new sfy process gets the environment of the shell that ran `sfy restart`, filtered as for `sfy serve`. That includes the caller's `PATH`, so the multiplexer server's own `PATH` does not reach it. The hook and allow-list paths that sfy writes into each agent's settings.json name the path that sfy started as, found on PATH, and keep a symlink such as `/opt/homebrew/bin/sfy` unresolved. When no such path matches the running binary, they name the `os.Executable` path. It fails when run by a worker or a reviewer. |
| `sfy down` | operator | Stops every agent session and the sfy process. It sends one request over the socket to the running sfy process. That process stops each session it knows, the lead and the workers and reviewers of tasks that have a workspace, through the launcher and the template set that it loaded at start. A change to `config.toml` after the start does not affect it, so sfy down still stops the sessions of the old terminal tool. It stops only sessions that run. The process then answers and exits, and removes its socket as on a hangup. It stops the lead through `stop` with the recorded address when the lead runs in the operator's terminal, and removes the record. That ends the terminal or session that the address names. It does not stop a registered lead, because sfy did not start it. It clears the registration and the recorded address, leaves the agent running, and adds "Left lead running and cleared its registration, because sfy did not start it." to what it prints. The agent's own commands then run as the operator's again. It prints what it stopped, for example "Stopped lead, worker-t3 and the sfy process." Without a sfy process it prints "sfy is not running." and exits 0. If a stop fails, the sfy process keeps running and the command prints the error, so the operator can run it again. A worker, a reviewer and the lead get an error and nothing stops, because the lead would stop itself in mid-command. sfy down changes no task. A later plain `sfy` starts the sfy process and the lead. A task in `working`, `review` or `feedback` whose worker was stopped is handled as a worker that ended on its own: the new sfy process tells the lead once that the worker is gone, and the lead decides what happens to the task. |
| `sfy task new` | lead, operator, programs | Creates a task from a description, a goal and the optional fields. Prints the task id. A warning about an untrusted folder goes to standard error. |
| `sfy task edit <id> <file>` | lead | Replaces the file of a task in any state except `approved`, `done` and `cancelled`. The task keeps its id, state, round, source, progress and folder. Once the task has a workspace, an edit may not change `## Repo`, `## Branch` or `## Protected`. A running worker and a running reviewer each get a message that names the path of the new file. Both read the current file with the Read tool, so an edit is visible to them at once. |
| `sfy task queue <id>` | lead | Moves a `new` task to `queued`. |
| `sfy task list [--archived]` | anyone | Lists the tasks and their states, without archived tasks. With `--archived`, lists only the archived tasks. |
| `sfy task show <id>` | lead, operator | Prints a task's file, state and verdicts. |
| `sfy task complete <id>` | lead, operator | Moves an `approved` task to `done` and cleans up. Fails for a task in any other state. |
| `sfy task integrate <id>` | lead, operator | Integrates the branch of an `approved` repo task into the default branch, as the state rules describe, then moves the task to `done` and cleans up. It refuses in a no-merge repo and names the branch. It fails for a task with no repo, for a task with a `## Branch` section, and for a task in any other state. |
| `sfy task push <id>` | lead, operator | Releases the push hold of a `held` task and starts a worker that pushes the branch. Run it only on the operator's word. Fails for a task in any other state. |
| `sfy task feedback <id> <text>` | lead, operator | Sends feedback on a task in any state to its worker and records it on the task. A task in `held`, `approved`, `done` or `cancelled` reopens. The lifecycle above describes each state. `sfy task show` lists the feedback with the verdicts. |
| `sfy task archive <id>` | lead, operator | Archives a `done` or `cancelled` task. The task keeps its state, files, verdicts and id, and its workspace and branch stay. Fails for any other state and for an archived task. `sfy task show` still prints it and says that it is archived. |
| `sfy task cancel <id>` | lead, operator | Marks a task `cancelled`. Works in any state except `done` and `cancelled`. |
| `sfy task resume <id>` | lead, operator | Returns an `escalated` task to the state it had before, and tells its worker or reviewer to go on. |
| `sfy send <agent> <text>` | operator, lead | Sends a message to an agent. The operator may send only to `lead`, and the message names the operator as its sender. The lead may also send to `<factory>/lead`, the lead of another factory on the machine, as the section Messages between factories describes, and to `drop:<name>`, a program that dropped a file, as the section Dropped messages describes. The message follows the normal path of the inbox and the note, so sfy never types it into a busy pane. |
| `sfy report done <text>` | worker | Reports the result. On a task with a check, sfy runs the check first and starts the review only when it passes. |
| `sfy report blocked <text>` | worker, reviewer | Tells the lead that the agent cannot go on. |
| `sfy report progress <text>` | worker | Records progress in the task's state. |
| `sfy report pushed <text>` | worker | Reports that the worker pushed the branch after a passed review of a task with a repo. Moves the task to `approved`. Fails when sfy asked for no push. |
| `sfy verdict pass\|fail <text...>` | reviewer | Records the verdict. The verdict is the text after `pass` or `fail`, or standard input with `-`. |
| `sfy inbox` | any agent | Prints unread messages and marks them read. |
| `sfy escalate <id> <text>` | lead | Moves a task to `escalated` and tells the operator. |
| `sfy status` | anyone | Prints `status.md`. |

Three internal commands do not appear in prompts:

- `sfy serve` runs the sfy process. `sfy` and `sfy restart` start it.
- `sfy run [-i] <envfile> -- <command...>` starts every agent session and the sfy process. It reads the environment from the file, deletes the file, and replaces itself with the command. With `-i` the command sees only the file's variables. Without it, they overlay the current environment. This keeps variable values, such as tokens, out of the process's argv.
- `sfy hook <event>` is the command that an agent CLI's hooks run, for example when a turn ends or a permission prompt opens.
- `sfy hook --user <event>` is the same hook in the user's settings, which every session of the user reads. It acts as the lead only when the hook input's `session_id` is the registered session. For any other session, for a worker or reviewer (which get their own hooks through `--settings`), and when no session is registered, it exits 0 at once, prints nothing and makes no socket call. It never decides a permission request for a session that is not the registered one.
- A registered agent's own `sfy` commands carry no `SFY_AGENT`. The CLI asks the provider for the current session's id, and when it equals the registered id, the command runs as `lead`. The commands that only the operator may run, `sfy down`, `sfy up` and `sfy hooks install`, refuse for it.

A command that an agent kind may not run fails with an error that names the command it may run instead.

## Permissions

- Workers and reviewers run in the provider's sandbox. Writes reach only the workspace and the paths in `allow_write`. Reads skip the paths in `deny_read`, and the `sessions` folder of every factory: `~/.sfy/sessions` and each `~/.sfy/factories/<name>/sessions`. sfy lists the factories when it starts the session, so a factory that sfy creates later is hidden from that session only from its next start. The provider's Read tool reaches the workspace and the session's own task file, `tasks/<id>/task.md` in the factory folder. It reaches no other task's file.
- The sandbox leaves the network open. It runs no command outside the sandbox. sfy refuses to start a worker or a reviewer where the sandbox is unavailable. On macOS the sandbox lets commands reach the system TLS trust service, so Go tools can verify certificates. On macOS sandboxed commands may use Unix sockets only under the sandbox's own temp folder.
- sfy runs a task's check command itself, because the sandbox of a session cannot run it for sfy. The command runs in a sandbox with the same `allow_write` and `deny_read` lists as a worker, plus its workspace, its own temp folder, and the git folder of its worktree for writes. It also cannot read the sfy socket and the `sessions` folder of any factory. The sandbox is `bwrap` on Linux and `sandbox-exec` on macOS, and it leaves the network open. The command gets sfy's environment without the `SFY_*` variables and without the git variables that point at another repository. A check that cannot start in the sandbox fails with the error.
- A worker or a reviewer never waits on a permission prompt. sfy's permission hook denies the call, gives the agent a reason, and records the denial in `sfy.log`.
- Workers and reviewers get a clean environment. It holds the `SFY_*` variables, the basic shell variables, the provider's own variables and the names in `pass_env`.
- Workers and reviewers run their sandboxed commands without an allow list. The `allow` setting is the lead's allow list.
- Workers may push only the task branch, with the two exact push forms, and not while the task holds its push. Reviewers may not push. Pushes run outside the sandbox. The lead runs git through its allow list. Its merge guard hook refuses `git push`, `git merge`, `git rebase` and `gh pr merge` in a no-merge repo. sfy runs the integration itself.
- Extra rules for one kind of session come from `[session_rules.<kind>]` in `config.toml`. Its `allow` list adds permission rules to that kind's allow rules, and its `excluded_commands` list adds command patterns that run outside the sandbox. sfy adds no rule for any other tool by itself.
- Workers and reviewers run only the `sfy` commands that exact rules name, and these run outside the sandbox, because the sandbox blocks the sfy socket. The same patterns appear in the allow rules and in the sandbox's excluded commands. A worker gets `sfy report *` and `sfy inbox`. A reviewer gets `sfy verdict *`, `sfy report blocked *` and `sfy inbox`. Neither gets `sfy *`, because `sfy run` would start any command outside the sandbox. The lead keeps `sfy *`. The session's own hooks run from the settings and need no rule. A command whose text holds a backtick stays in the sandbox and fails, even inside quotes. The worker and reviewer prompts tell agents to keep backticks out of sfy text.
- `sfy run`, `sfy serve`, `sfy register`, `sfy restart` and `sfy config` refuse when `SFY_AGENT` names a worker or a reviewer. So does `sfy hook` with any event other than the ones that a session's own hooks use: `turn-ended`, `permission`, `idle`, `busy`, `note` and `note-watch`. The environment can be faked, so the exact rules are the real barrier. The refusal is a second layer.
- A caller does not choose its identity with `SFY_AGENT`. When sfy starts a session, it writes a random token to `sessions/<name>/token` in the factory folder, with mode 0600, and puts it in `SFY_TOKEN` in the session's environment. `sfy register` writes the token of the registered lead to `sessions/lead/token`, and the CLI reads it for a session that matches the registration. The CLI sends the token with each request. The sfy process finds the session that owns the token and takes the caller's name from it. A request with a name and no token, with a token that no session owns, or with a name that differs from the token's owner fails. A request with neither name nor token comes from the operator. The sandbox hides the `sessions` folder of every factory from workers and reviewers, so a session cannot read another session's token. Without the token of the lead, `SFY_AGENT=lead` gives a worker nothing, and so does the old name `SFY_AGENT=main`.
- A session that started under an sfy without tokens still runs after an upgrade and `sfy restart`, and it has no `SFY_TOKEN`. On start, `sfy serve` writes a random legacy token to `sessions/<name>/legacy-token`, with mode 0600, for each session folder that has no `token` file. The sfy process accepts a legacy token like a session token. The CLI reads it when the environment has no token. For `lead`, and for its old name `main`, it reads the file only when `CLAUDE_CODE_SESSION_ID` equals `sessions/lead/conversation-id`, so a worker that sets `SFY_AGENT=lead` gets no token, because it cannot read that id. Starting a session removes its legacy token. The error for a missing token tells the caller to restart the session or run `sfy restart`. The sandbox hides the `sessions` folder from workers and reviewers. The CLI runs outside the sandbox, so a worker that sets `SFY_AGENT` to another worker or reviewer name could still read that legacy token. Only sessions that started before the upgrade have one, and restarting such a session removes the gap.
- Workers and reviewers pass text to sfy only on standard input, so sfy reads no file for them.
- The operator trusts the working folder and each repo once, by hand, in the provider. sfy only warns about an untrusted folder and never changes the provider's own config. A working folder equal to $HOME always warns, because Claude Code keeps trust for $HOME in memory only.
- Deny rules protect the paths that the task file names, in every mode.
- Reviewers may run commands in the sandbox, but their edit tools are off. The sandbox limits what a build can write.
- The sandbox covers shell commands. The provider's edit and read tools follow the permission rules.

## Configuration

sfy builds a factory's settings from three places, lowest first: the defaults in the table below, the machine layer `~/.sfy`, and the factory folder. In each folder, sfy reads the `.toml` files of `config.d/` in name order, then `config.toml`. The files merge key by key at every depth, as the section Factory definition describes:

| Setting | Default | Meaning |
|---|---|---|
| `review_rounds` | 3 | Failed reviews before a task moves to `escalated`. |
| `max_workers` | 2 | Workers that run at once. |
| `resend_minutes` | 5 | Minutes between notes for an unread message. |
| `note_fallback_after` | `5m` | A Go duration. How long queued text may stay unread by the note hook before sfy types the note into an idle pane. |
| `check_timeout` | `20m` | A Go duration. How long a task's check may run before sfy stops it and counts it as failed. |
| `provider` | `claude` | The agent provider adapter. |
| `multiplexer` | `tmux` | The name of the template set to use. |
| `models.lead` | `opus` | The lead's model. |
| `models.worker` | `sonnet` | The workers' model. |
| `models.reviewer` | `sonnet` | The reviewers' model. |
| `allow` | git, gofmt, build, test | The lead's allow list. |
| `deny_read` | `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.netrc`, `~/.docker`, `~/.kube`, `~/.claude.json` | Paths that the sandbox hides from workers and reviewers. An empty list means no path. |
| `allow_write` | The user cache folder and `~/go/pkg/mod`. On macOS also the per-user temp folder from `getconf DARWIN_USER_TEMP_DIR`, without its trailing slash | Paths outside the workspace that the sandbox lets workers and reviewers write. An empty list means no path. |
| `pass_env` | none | Names of environment variables that workers and reviewers keep. |
| `no_merge` | `false` | When true, the lead leaves approved branches for the operator. A per-repo file can set it per repo. |
| `draft_pr` | `false` | When true, the push note tells the worker that any PR it opens must be a draft. A per-repo file can set it per repo. |
| `start` | `build` | The list that a task starts in, as the section The task process describes. |
| `agents.<name>` | the default agents | The agents of the task process. |
| `lists.<name>` | the default lists | The lists and branches of the task process. |
| `session_rules.lead`, `.worker`, `.reviewer` | none | Tables with an `allow` list and an `excluded_commands` list. sfy adds them to that kind's permission allow rules and sandbox excluded commands. The name of any `claude` agent of the process is a kind too. |
| `session_settings.lead`, `.worker`, `.reviewer` | none | Tables of extra keys. sfy merges them with its own keys in that kind's settings file. Worker and reviewer files hold `promptSuggestionEnabled = false` unless a table sets it. |

### Factory definition

A factory's definition is the `config.d/` folder, the `config.toml`, the `prompts/` folder and the `skills/` folder of its factory folder. The machine layer, `~/.sfy/config.d/`, `~/.sfy/config.toml` and `~/.sfy/prompts/`, applies to every factory. The operator owns the machine layer. For the default factory, the machine layer and the factory folder are one folder, and sfy reads each file once. A missing file or folder adds nothing. sfy reads no `.sfy/` folder in the working folder for the factory.

- **Settings.** sfy reads the config files in this order: the machine's `config.d/*.toml` in name order, the machine's `config.toml`, the factory's `config.d/*.toml` in name order, and the factory's `config.toml`. It parses each file into a table and merges the tables key by key at every depth. A later file's value replaces an earlier file's value. A list replaces the earlier list as a whole. So a factory that sets `session_rules.worker.allow` keeps the machine file's `session_rules.worker.excluded_commands`. sfy then decodes the merged table once and checks the values once. An unknown key in any file is an error. A value of the wrong type is an error that names its file.
- **`config.d/`.** The folder lets a factory take shared settings through links, such as `config.d/50-shared.toml`, and keep its own `config.toml` on top. sfy follows a link. A missing folder adds nothing. A file whose name does not end in `.toml` is skipped. A file that cannot be read or parsed is an error that names the file. The per-repo files have no `config.d/`.
- **Prompts.** The base `<kind>.md` comes from the last place that has one. Without one, the embedded default is the base. sfy collects the `.md` files of `prompts/<kind>.d/` from both places by file name. A factory file replaces the machine file of the same name. A file that holds only white space removes the file of that name from the prompt. sfy appends the rest in name order, each after a blank line. A missing folder adds nothing, a file without `.md` is skipped, and a file that cannot be read is an error.
- **When a change applies.** Settings apply at the next `sfy restart`. The merge guard reads them at its next run. A prompt file applies at the next start of a session of that kind. The restart note names `lead-prompt.md` when the lead's prompt changed. `sfy restart` prints each setting and each prompt file that changed since the last start, and the restart note holds the same lines.
- **Skills.** The `skills/` folder of the factory folder holds skill folders, each with a `SKILL.md`. Every session of the factory gets them, as the section Extending sfy describes. The machine layer has no `skills/`, because `~/.claude/skills` already reaches every session on the machine. For the default factory, `~/.sfy/skills/` is the factory's own folder. A change to a skill applies at the next start of a session.
- **`sfy config`** prints the flat result in six parts, after a header that names the factory and the two places. The settings come as TOML lines with dotted keys, and each line ends with a comment that names the file that set it last, or `default`. The process part names the start list, each list with its joined agents, its branches and the file that set its agents last, and each agent with its harness and `ends`. It marks a wide shell agent. The prompts part names the base file of each kind and each addition in append order. A removed addition names the file that removed it. The skills part names each folder in `skills/` that holds a `SKILL.md`. The factory files part lists each name in the factory folder that is neither state nor definition, as the State section describes. The old names part lists each old name of the lead that the factory still uses, as the section Old names of the lead describes, and stderr gets a warning line for each. The output prints the values of `session_settings.<kind>.env` as `<set>`, because such a value may hold a token. A factory repo that others can read must hold no token. Name the variables in `pass_env` and keep the values in the operator's environment.

### Session settings

A `session_settings.<kind>` table holds keys that sfy copies into the provider settings file of that kind of session. The provider decides what the keys mean, so sfy passes the values without a check. For example, this config stops notifications from workers and reviewers and leaves the lead's alone:

```toml
[session_settings.worker]
preferredNotifChannel = "notifications_disabled"

[session_settings.reviewer]
preferredNotifChannel = "notifications_disabled"
```

A table may set `permissions`, `hooks` and `sandbox`, and sfy merges them with its own keys. `permissions.allow` and `permissions.deny` get the table's rules after sfy's rules. Each event in `hooks` gets the table's hooks after sfy's hooks. A key that sfy does not write, such as a network allow list in `sandbox`, is added. Where sfy writes a value itself, sfy's value wins, at every depth. That keeps the identity token, the note delivery and the sandbox working. A factory that needs a different value sets the sfy key that controls it, such as `allow_write`. A kind other than `lead` or the name of a `claude` agent of the process, such as `worker` or `reviewer`, is a config error. Worker and reviewer settings files hold `"promptSuggestionEnabled": false` by default, which turns off Claude Code's prompt suggestions. Nobody reads those panes, so a suggestion would only look like a stuck message and cost a model request. A table can set the key to `true` to override the default. The lead's file stays at Claude Code's default. Without the tables, the settings files hold only sfy's keys and that default.

### Per-repo configuration

A per-repo file holds the settings of one repo. A `.sfy.toml` file can also cover every repo below one folder. Both hold only per-repo settings. A new per-repo setting is one typed field in `repoSettings`.

| Setting | Default | Meaning |
|---|---|---|
| `no_merge` | the value in `config.toml`, `false` | When true, `sfy task integrate` refuses, and the lead leaves the approved branch for the operator to merge by hand. |
| `draft_pr` | the value in `config.toml`, `false` | When true, the note that asks the worker to push also says that any PR it opens must be a draft, with `gh pr create --draft`. sfy opens no PR. |

- **Which folder counts.** For a task whose `## Repo` is a local path, the folder is that path. For a task whose `## Repo` is a URL, the folder is the clone that sfy keeps in `repos/` of the factory folder, which exists after the first task of that URL starts. So a `.sfy.toml` in `repos/` or in the factory folder covers URL repos. sfy never reads the task's worktree or branch, so a worker's commit cannot change settings before the operator merges it.
- **The `.sfy/` folder.** sfy reads `.sfy/config.toml` in that folder only, with no parent walk. The operator may keep `.sfy/` out of git with `core.excludesFile`, or commit it. In a URL clone, a committed `.sfy/` arrives with each fetch of the default branch, and the operator can also put an untracked one there by hand.
- **Lookup.** sfy reads `.sfy.toml` in that folder and in each parent folder up to `$HOME`. It stops at `$HOME`. A folder outside `$HOME` counts alone, with no parent. sfy reads the files again on each use, so an edit applies at once.
- **Precedence.** Key by key, the first file that sets the key wins. The order is `.sfy/config.toml` in the repo folder, then each `.sfy.toml` from the nearest to the farthest, then the factory's merged settings from its config files and those of `~/.sfy`.
- **Refusals.** A key that sfy does not know is an error. So is a global-only key, such as `multiplexer`, `max_workers` or `multiplexers`. The error names the file and the key. A file that does not parse is also an error. While a file has an error, `sfy task integrate` refuses for the repos below it, and the push note adds no draft rule.

For example, this file at `~/Developer/work/.sfy.toml` makes the operator the only one who merges in every work repo below it:

```toml
no_merge = true
```

A file in one repo, such as `~/Developer/work/sandbox/.sfy.toml`, can set `no_merge = false` to allow the lead to integrate there again.

## Multiplexer templates

A template set lives under `[multiplexers.<name>]` in `config.toml`. Each template is one command line. sfy fills in `{name}`, `{dir}`, `{command}`, `{text}` and `{home}`, and quotes each value for the shell. `{home}` is the absolute path of the factory folder of the running factory, and every template accepts it. sfy also sets `SFY_HOME` to that folder in the environment of every template command, also when `sfy up` finds the factory through the folder rules.

| Template | Required | What the command does |
|---|---|---|
| `start` | yes | Starts `{command}` in a new session called `{name}`, in the folder `{dir}`. |
| `send` | yes | Types `{text}` into session `{name}` and presses Enter. |
| `alive` | yes | Reports whether session `{name}` runs, with three results. Exit 0 means it runs. Exit 1 means it is gone. Any other exit code means the check failed, so sfy does not know. sfy never reads a failed check as gone. It logs the first failure of an agent, tells the lead after three ticks in a row ("sfy cannot tell whether worker-t1 runs: <error>"), and pauses the cleanup retries of a task while the check fails. |
| `stop` | yes | Ends session `{name}`. |
| `notify` | no | Draws the operator's attention to session `{name}`, with `{text}` as the message. |
| `show` | no | Makes session `{name}` visible to the operator. sfy runs it after each successful `start` of a worker or reviewer, and of the lead when `show_lead` is empty. A failure goes to `sfy.log` and does not fail the start. |
| `show_lead` | no | Makes the lead's session `{name}` visible to the operator. sfy runs it after the lead starts, in place of `show`. On an in-place start, sfy runs it after it records the address and before it replaces its own process. An empty `show` with a `show_lead` hides workers and reviewers. |
| `whoami` | no | Prints the address of the terminal that it runs in on one line. The address must work as `{name}` in the other templates. sfy runs it in the operator's terminal, so the terminal tool's own variables are set. |

A recorded address replaces `{name}` for the lead only. The `sfy up` row in the command table describes it.

sfy does not ask the multiplexer whether an agent waits for input. The provider adapter tells it.

## Extending sfy

A use of sfy extends it at these points, without changes to the core:

- **Prompts.** A prompt file in `prompts/` of the machine layer `~/.sfy` or of the factory folder replaces the default prompt for that kind of agent, and the factory's file wins. For a worker, `worker.md` replaces the common part and `worker-repo.md` replaces the repo part. The kinds are `lead`, `worker`, `reviewer` and `worker-repo`. The files that end in `.md` in `prompts/<kind>.d/` of both places append to that kind's prompt in name order, each after a blank line, to the default and to a replacing file alike. A factory file replaces the machine file of the same name, and an empty one removes it. A missing folder adds nothing, a file that cannot be read is an error, and other files are skipped. The lead's recorded prompt includes the additions.
- **Skills.** A skill folder in `skills/` of the factory folder reaches the lead, the workers and the reviewers of that factory. The Claude adapter writes a plugin into `sessions/<name>/plugin/` at each session start. The plugin holds the manifest `.claude-plugin/plugin.json` with the name `factory`, and a link `skills` to the factory's `skills/`. The adapter passes the folder to claude with `--plugin-dir`, so a skill shows as `factory:<skill>`. A plugin folder grants no edit rights, so a session cannot edit the skills without a permission grant. The lead gets the skills too, because it runs in the working folder and sees nothing in the factory folder. Without `skills/`, the adapter writes no plugin, removes an earlier one, and adds no flag.
- **Task files.** A task file may carry extra sections. sfy passes the whole file to the worker and the reviewer.
- **Programs.** A program sends a message or a task to the lead with a file in the drop folder, as the section Dropped messages describes. A program that runs the CLI with no token acts as the operator.
- **Process.** The `agents`, `lists` and `start` keys change the task process, as the section The task process describes.
- **Providers.** A new provider is a new adapter that implements the Go interface. The launcher keeps one adapter for each session harness, and a new session harness adds one entry to that map.
- **Multiplexers.** A new multiplexer is a new template set in `config.toml`.
- **Configuration.** `config.toml` in the machine layer and in the factory folder sets the models, the limits, the allow list and the sandbox paths.
- **Isolation.** A VM or a container for each task can replace the sandbox and its two path lists.

## Old names of the lead

An older sfy called the lead `main`. sfy still accepts each old name and reads it as the new one, until a later release removes the old names. That release waits until `sfy config` lists no old name on any factory, and until each tool that reads the old names has changed.

| Old name | How sfy reads it |
|---|---|
| `sfy send main <text>` | as the target `lead`. The command prints "sfy: main is now lead. Run: sfy send lead <text>." on stderr and exits as before |
| `SFY_AGENT=main` in a session that started under an older sfy | as the session `lead`. The token of `lead` proves the old name too |
| `models.main`, `session_rules.main`, `session_settings.main` and `multiplexers.<set>.show_main` | sfy renames each old key to its new name in each config file, before the merge. A file that sets both names fails to load, and the error names both keys |
| `prompts/main.md` | as `prompts/lead.md` when the same place has no `lead.md` |
| `prompts/main.d/*.md` | as additions of the lead, merged with `lead.d/` by file name. A `lead.d` file wins over a `main.d` file of the same name in the same place |
| the task source `main` in `state.json` | as `lead` |
| `From: main` in a stored message | it stays as written, because the message is a record |

`sfy up`, `sfy restart` and `sfy config` print one warning line on stderr for each old key and each old prompt file or folder, such as "<folder>/config.toml sets models.main. sfy reads it as models.lead. Rename the key."

### The move at start

A factory that an older sfy ran keeps the lead's files under `sessions/main/`, `inbox/main/` and `main-prompt.md`, and its lead may still run in the terminal `<prefix>main`. sfy moves the factory to the new names the first time it starts from a release with the new names. `sfy serve` does the move before its socket listens. `sfy`, `sfy up`, `sfy restart` and `sfy register` do it too, before they ask whether the lead runs, because they ask before the new sfy process starts.

1. When `sessions/main/` is a folder and `sessions/lead/` does not exist, sfy asks the `alive` template whether `<prefix>main` runs. When `alive` fails, sfy moves nothing and the command fails with the error, because a guess could start a second lead.
2. sfy renames `sessions/main/` to `sessions/lead/`, `inbox/main/` to `inbox/lead/` and `main-prompt.md` to `lead-prompt.md`. Each rename runs only when the old entry is no link and the new name does not exist.
3. sfy leaves a link with each old name to its new name, so an older sfy and an outside reader still find the files. The sfy process skips links when it looks for the owner of a token, so one token never matches two names.
4. When `<prefix>main` runs and `sessions/lead/address` does not exist, sfy writes `<prefix>main` there. The launcher then reaches the running lead through that address, as the `sfy up` row of the command table describes.

The running lead keeps its process, terminal and conversation. Its environment still holds `SFY_AGENT=main` and its token, and both still work. `conversation-id` and `registered.json` move with the folder, so the next start resumes the same conversation and a registered lead stays registered. The restart note names `lead-prompt.md` when the prompt text changed. When the old terminal ends, `alive` reports it gone and sfy removes the address record, so the next start uses `<prefix>lead`. A second start finds `sessions/lead/` and moves nothing. Workers and reviewers keep running, and their `sfy report blocked` reaches the lead.

### Rollback

The links let an older sfy read and write the moved files while the lead still runs as `<prefix>main`. Once the lead has started again as `<prefix>lead`, an older sfy finds no `<prefix>main` and would start a second lead. Its token lookup also does not skip links, so it would name the lead's token `main` and refuse `SFY_AGENT=lead`. A rollback therefore runs in this order:

1. Check with the `alive` template whether `<prefix>lead` runs. When it does, stop it with `sfy down`, which stops every session, or end the lead's conversation first.
2. Install the previous release and run `sfy restart`, or a plain `sfy` after `sfy down`. The older sfy starts the lead as `<prefix>main` and resumes its conversation through the linked `conversation-id`.

## Platforms

sfy runs on Linux and macOS, with tmux, or with another multiplexer that has a template set. See `docs/herdr.md`.

docs/rex.md has an example config for Rex.

A pushed tag `v*` starts a release. GoReleaser builds sfy for darwin and linux on amd64 and arm64, publishes a GitHub release, and writes the Homebrew cask to `JacobJNilsson/homebrew-tap`. Each release holds `sfy_<version>_<os>_<arch>.tar.gz` archives and `checksums.txt`, which `sfy restart --update` reads on a non-brew install. The binary reports the tag with `sfy --version`, and a build without the release flags reports `dev`.
