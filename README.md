# simple factory

sfy is a minimal core for software factories. A software factory implementation builds on it with its own config, prompts, skills and flows.

## What belongs in the core

The core stays small and knows nothing about any one kind of work. It holds the parts that every factory needs:

- The generic task lifecycle, from a queued task to a reviewed result.
- Agent sessions in terminal panes, behind a provider adapter and a set of multiplexer command templates.
- A fresh review of each result.
- Integration hooks, so that an implementation can add its own steps.

An implementation holds everything that is specific to a person or a company: rules, merge flows, models and prompts. A feature that only one implementation needs belongs in that implementation and not in sfy.

## Implementations

These implementations build on sfy. They are examples and not part of the core.

- [lagom-factory](https://github.com/JacobJNilsson/lagom-factory) (lfy) is the operator's software factory implementation.
- An orchestration product is planned. The operator intends to rebuild it on sfy.

## How sfy works

sfy runs coding agents as interactive sessions in terminal panes. You talk to one agent, the lead. It hands tasks to workers, and each worker does its task in its own git worktree. A fresh reviewer checks each result, and the lead integrates the branch with plain git when the review passes. You can watch every session in your terminal.

## Requirements

- Claude Code, logged in.
- git.
- A terminal tool. The default template set uses tmux. Other tools plug in through the config file.

### Linux sandbox

Workers and reviewers run in the Claude Code sandbox. On Linux the sandbox needs `bubblewrap` and `socat`. macOS needs nothing extra. Install both on Ubuntu or Debian:

```sh
sudo apt install bubblewrap socat
```

Ubuntu 24.04 and later also stop bwrap from creating user namespaces. Without a fix, Claude Code fails with `loopback: Failed RTM_NEWADDR: Operation not permitted`. Create `/etc/apparmor.d/bwrap` with this text:

```
abi <abi/4.0>,
include <tunables/global>

profile bwrap /usr/bin/bwrap flags=(unconfined) {
  userns,

  include if exists <local/bwrap>
}
```

Then load it:

```sh
sudo apparmor_parser -r /etc/apparmor.d/bwrap
```

sfy checks this before it starts a worker or a reviewer. It also checks that it can find `claude`, on PATH or at `~/.local/bin/claude`. When a check fails, sfy starts no session. It escalates the task, tells the lead and the operator the cause, and names the fix. After the fix, run `sfy task resume <id>`. sfy installs nothing itself, because that needs root.

## Install

- macOS: `brew install jacobjnilsson/tap/sfy`
- Linux: download the amd64 or arm64 tarball from GitHub Releases. Put `sfy` in a folder that you can write, such as `~/.local/bin`, so that `sfy restart --update` can replace it without sudo.
- From source: `go install ./cmd/sfy`

## Update

Run `sfy restart --update`. A brew install upgrades through brew. Any other install downloads the newest release from GitHub, checks its SHA-256 sum and replaces the binary. If the binary's folder is not writable, sfy prints a `sudo install` command for you to run and keeps the old process.

## Quick start

Run `sfy`, then talk to the lead. Describe what you want built and the lead creates tasks.

A task moves through these states:

1. `new`: the lead prepares the task.
2. `queued`: the task waits for a free worker. A task with an `## After` section also waits until every task that it names is `done`, and shows as `queued (after t6, t8)`. List the ids on separate lines or separate them with commas or spaces. A task may name only older tasks.
3. `working`: a worker does the task. On a task with a `## Check` section, sfy runs the check when the worker reports done, and a failure sends the output back to the worker. A failed review sends it back as `feedback`.
4. `review`: a reviewer checks the result.
5. `approved`: the reviewer passed the result.
6. `done`: the lead completed the task.

A task on a repo works on its own branch, named `<type>/<slug>`, as in `feat/organisation-locations`. The type is a Conventional Commits type. An optional `## Type` section in the task file sets it. Otherwise sfy reads it from a `fix:` style prefix or the first word of the title, and falls back to `feat`. The slug is the title in lower case ASCII words joined by `-`, cut at 50 characters on a word boundary. If the name exists in the repo or on a remote, sfy appends `-2`, `-3` and so on. sfy picks the name once and keeps it, and `sfy task show` prints it. Tasks that started earlier keep their `sfy/<id>` branch.

To work on an existing branch of the remote, such as the branch of your pull request, add a `## Branch` section with one line, the branch name. It needs a `## Repo` section. sfy refuses the default branch and a branch that the remote lacks, and an edit cannot change the branch once the task has a workspace. The worker checks out that branch, tracking `origin/<branch>`, and sfy records the remote sha. After the review passes, the worker may run only `git push --force-with-lease=<branch>:<sha> origin HEAD:<branch>`. The lease names the recorded sha, so git refuses the push when someone else pushed to the branch since. A bare `--force-with-lease` trusts the remote-tracking ref, which a `git fetch` silently updates, so sfy allows no bare lease. `## Push` with `hold` works as usual. After approval, `sfy task integrate` refuses and the lead runs `sfy task complete <id>`, also in a repo without no-merge.

To choose the model of a task, add a `## Model` section with one line, the model name of the task's worker, such as `opus` or `sonnet`. It overrides `[models] worker` in `config.toml` for that task only. A `## Reviewer model` section does the same for the task's reviewer and overrides `[models] reviewer`. An empty or missing section keeps the configured model. A name may hold letters, digits, `.`, `-`, `[` and `]`, and sfy refuses any other name. `sfy task show` and `status.md` show the models that a task sets. An edit may change them, even after the task has a workspace. The next agent start uses the new model. A planning task on `opus` and an implementation task on `sonnet` can follow each other with `## After`.

sfy keeps its state in `~/.sfy`:

- `tasks/` holds one folder for each task.
- `notes.md` holds the lead's notes.
- `status.md` holds the current state of all tasks.
- `sessions/<name>/note.md` holds the notes and messages that sfy delivers to a Claude Code session, until the session's hook reads them.
- `drop/` holds the files that programs drop for the lead, as the section Send from a program describes.
- `config.toml` holds your settings.

## Everyday commands

| Command | What it does |
|---|---|
| `sfy` or `sfy up` | Starts sfy and the lead. |
| `sfy down` | Stops all agents and the sfy process. |
| `sfy restart` | Restarts the sfy process and keeps the agents running. |
| `sfy status` | Prints `status.md`. |
| `sfy task list` | Lists the tasks. |
| `sfy config` | Prints the factory's merged settings and prompt files, with the file behind each. |
| `sfy send lead <text>` | Sends a message to the lead. The operator may send only to `lead`. The lead may send to any agent, and to `<factory>/lead`, the lead of another factory on the machine. That message names its sender as `<factory>/lead`. |
| `sfy task new --factory <factory> <file>` | Lets the lead create a task in another factory on the machine. The task starts in `new` there with the source `<factory>/lead`, and the command prints that factory's task id. |
| `sfy help` | Lists every command. |

## Send from a program

A program with no sfy session, such as a poller, a timer or a script, sends to the lead of a factory with a file. It needs no sfy binary, and the factory may be down. sfy shows the sender as `drop:<name>`, a name that nothing checks, and never as the operator.

A drop is a UTF-8 file of at most 1 MiB, with header lines, a blank line and the body:

```
To: lead
From: alerts
Id: alert-4711
Kind: task

# Disk full on db-2

## Description

The disk of db-2 is 95% full.

## Goal

The disk has 20% free space.
```

- `To` is `lead`.
- `From` is your program's name: 1 to 32 characters from `a-z`, `0-9` and `-`. sfy refuses `operator`, `sfy`, `lead` and `main`.
- `Id` is your id for this item, such as the id of the source item. It has 1 to 64 characters from `A-Z`, `a-z`, `0-9`, `.`, `_` and `-`.
- `Kind` is `message`, the default, or `task`. For `task`, the body is a task file. The task starts in `new`, and the lead prepares it.

To drop a file:

1. Pick the factory folder, such as `~/.sfy/factories/web`, or `~/.sfy` for the default factory. Never create `drop/` yourself. A missing folder means that the factory does not exist on this machine.
2. Name the file `<time>-<from>-<id>.md`, where `<time>` sorts, such as the output of `date +%s%N`.
3. Write the file to `drop/tmp/` under that name.
4. Rename it into `drop/new/` with `mv`. When the rename succeeds, the drop is delivered. sfy takes it at once, or at its next start, in name order.
5. To send the same item again, reuse its `Id`. sfy skips a second drop with the same `From` and `Id` for 30 days.

In shell:

```sh
dir=~/.sfy/factories/web/drop
file="$(date +%s%N)-alerts-$id.md"
printf 'To: lead\nFrom: alerts\nId: %s\nKind: task\n\n%s\n' "$id" "$body" > "$dir/tmp/$file"
mv "$dir/tmp/$file" "$dir/new/$file"
```

sfy moves a drop that fails a check to `drop/rejected/`, with the reason in a file beside it that ends in `.why`.

sfy answers in `drop/replies/<from>/`, in the same format. Each taken drop gets a receipt with `Kind: receipt` and `Re: <id>`, and a rejected drop gets `Kind: rejected` with the reason. When a task from a drop is done, cancelled or escalated, sfy writes `Kind: status` with `Task` and `State`. The lead can answer with `sfy send drop:<from> <text>`, which writes `Kind: message`. Read the folder at your next run, skip names that start with a dot, and delete what you read. sfy removes replies older than 30 days. SPEC.md, section Dropped messages, holds the full rules.

## Configure

See `docs/configuration.md` for the details of each item.

- Config file: `~/.sfy/config.toml` sets limits such as `max_workers` and defines terminal template sets for every factory. A `config.toml` in a factory folder sets single keys for that factory, and `~/.sfy` wins over it. `sfy config` prints the merged result and the file behind each key.
- Folder overrides: a `.sfy/` folder in a working folder or in a parent up to your home folder has the shape of a factory folder: `config.toml`, `config.d/`, `prompts/` and `skills/`. The nearest `.sfy/` wins key by key, and `~/.sfy` is the farthest. For example, `~/Developer/acme/.sfy/config.toml` with `max_workers = 3` changes the factory of `~/Developer/acme` only. sfy skips a `.sfy/` folder that you do not own or that others can write, and `sfy config` names it.
- Per-repo config: `.sfy/config.toml` in a repo, or in a parent folder of the repo below the walk, sets per-repo keys. `draft_pr = true` makes the push note ask for draft PRs. `no_merge = true` stops the lead from integrating approved branches, so you merge them by hand. For example, put `no_merge = true` in `~/Developer/work/.sfy/config.toml` to cover every work repo below it. The old `.sfy.toml` file still works, with a warning.
- Headless: `headless = true` in `~/.sfy/config.toml` runs the sfy process detached, with no terminal window. Its log goes to `~/.sfy/sfy.log`. Stop everything with `sfy down`, or stop only the sfy process with `kill $(cat ~/.sfy/sfy.pid)`. `sfy restart` keeps it headless, and moves a process from a window to headless. For one start without the setting, run `sfy up --headless`, which starts only the sfy process, detached. `sfy restart --headless` keeps it detached. See `docs/configuration.md`.
- Check timeout: `check_timeout` in `~/.sfy/config.toml` sets how long a task's check may run. The default is `20m`.
- Prompt additions: files in `~/.sfy/prompts/<kind>.d/` add lines to the prompt of an agent kind in every factory. Files in `prompts/<kind>.d/` of a factory folder or of a nearer `.sfy/` folder add lines for that factory, and a file with the name of a farther file replaces it.
- Session settings: `[session_settings.<kind>]` tables add keys to the settings file of a session.
- Session rules: `[session_rules.<kind>]` tables add permission rules and unsandboxed commands to a kind of session.
- Task process: `[agents.<name>]` and `[lists.<name>]` tables change the steps that every task runs through, for example a gate script before the check. A factory may define agents and lists of its own names, a shell agent that picks the next list, and an operator step that waits for `sfy task release <id>`. A `## List` section in a task file picks the list that the task starts in. `sfy config` prints the process. See `docs/configuration.md`.
- Terminal tools: a template set under `[multiplexers.<name>]` plugs in another tool. See `docs/herdr.md` and `docs/rex.md`.
- A factory per folder: `sfy up` in a working folder runs a separate factory for it, with no setup. Its factory folder is `~/.sfy/factories/<name>/`, and sfy writes nothing in the working folder. A wrapper command can set `SFY_NAME_PREFIX`, such as `ex-`, so that one working folder has a factory for each name prefix, and it can ask for the factory folder with `sfy factory-folder`. See `docs/configuration.md`.
- A second factory folder by hand: `SFY_HOME=~/.sfy-work sfy up` runs a separate lead.
- A running agent: `sfy register` turns a Claude Code session that already runs into the lead.
- Old names: an older sfy called the lead `main`. sfy still reads `models.main`, `session_rules.main`, `session_settings.main`, `show_main`, `prompts/main.md` and `prompts/main.d/` with a warning. `sfy config` lists each old name that a factory still uses, so you can rename it. The first start of this release moves a running factory to the new names and keeps its lead running. SPEC.md, section Old names of the lead, describes the move and the rollback.

## More

- `SPEC.md` holds the full behaviour.
- `docs/` holds the examples and details, including `docs/releasing.md`.
