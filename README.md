# simple factory

sfy runs coding agents as interactive sessions in terminal panes. You talk to a main agent. It hands tasks to workers, and each worker does its task in its own git worktree. A fresh reviewer checks each result, and the main agent integrates the branch with plain git when the review passes. You can watch every session in your terminal.

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

sfy checks this before it starts a worker or a reviewer. It also checks that it can find `claude`, on PATH or at `~/.local/bin/claude`. When a check fails, sfy starts no session. It escalates the task, tells the main agent and the operator the cause, and names the fix. After the fix, run `sfy task resume <id>`. sfy installs nothing itself, because that needs root.

## Install

- macOS: `brew install jacobjnilsson/tap/sfy`
- Linux: download the amd64 or arm64 tarball from GitHub Releases. Put `sfy` in a folder that you can write, such as `~/.local/bin`, so that `sfy restart --update` can replace it without sudo.
- From source: `go install ./cmd/sfy`

## Update

Run `sfy restart --update`. A brew install upgrades through brew. Any other install downloads the newest release from GitHub, checks its SHA-256 sum and replaces the binary. If the binary's folder is not writable, sfy prints a `sudo install` command for you to run and keeps the old process.

## Quick start

Run `sfy`, then talk to the main agent. Describe what you want built and the main agent creates tasks.

A task moves through these states:

1. `new`: the main agent prepares the task.
2. `queued`: the task waits for a free worker. A task with an `## After` section also waits until every task that it names is `done`, and shows as `queued (after t6, t8)`. List the ids on separate lines or separate them with commas or spaces. A task may name only older tasks.
3. `working`: a worker does the task. A failed review sends it back as `feedback`.
4. `review`: a reviewer checks the result.
5. `approved`: the reviewer passed the result.
6. `done`: the main agent completed the task.

sfy keeps its state in `~/.sfy`:

- `tasks/` holds one folder for each task.
- `notes.md` holds the main agent's notes.
- `status.md` holds the current state of all tasks.
- `sessions/<name>/note.md` holds the notes and messages that sfy delivers to a Claude Code session, until the session's hook reads them.
- `config.toml` holds your settings.

## Everyday commands

| Command | What it does |
|---|---|
| `sfy` or `sfy up` | Starts sfy and the main agent. |
| `sfy down` | Stops all agents and the sfy process. |
| `sfy restart` | Restarts the sfy process and keeps the agents running. |
| `sfy status` | Prints `status.md`. |
| `sfy task list` | Lists the tasks. |
| `sfy help` | Lists every command. |

## Configure

See `docs/configuration.md` for the details of each item.

- Config file: `~/.sfy/config.toml` sets limits such as `max_workers` and defines terminal template sets.
- Prompt additions: files in `~/.sfy/prompts/<kind>.d/` add lines to the prompt of an agent kind.
- Session settings: `[session_settings.<kind>]` tables add keys to the settings file of a session.
- Session rules: `[session_rules.<kind>]` tables add permission rules and unsandboxed commands to a kind of session.
- Terminal tools: a template set under `[multiplexers.<name>]` plugs in another tool. See `docs/herdr.md` and `docs/rex.md`.
- A second state folder: `SFY_HOME=~/.sfy-work sfy up` runs a separate main agent.
- A running agent: `sfy register` turns a Claude Code session that already runs into the main agent.

## More

- `SPEC.md` holds the full behaviour.
- `docs/` holds the examples and details, including `docs/releasing.md`.
