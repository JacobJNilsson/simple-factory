# Configuration and operation

This page holds the details of running sfy. SPEC.md holds the full behaviour. The state folder is `~/.sfy` unless `SFY_HOME` names another one.

## Restart

Run `sfy restart` from a terminal outside the agent sessions, or ask the main agent to run it. It stops the sfy process and starts it from the installed binary. The main agent, workers and reviewers keep running, and their tasks stay on disk. The main agent gets a note, which says when its prompt changed. `sfy restart` does not build or install sfy.

`sfy restart --update` first upgrades sfy and restarts only when that succeeds. When Homebrew installed the running binary, it updates the tap and runs `brew upgrade sfy`. Otherwise it downloads the newest release from GitHub, checks the SHA-256 sum, writes the binary next to the running one and renames it over the running one. It never calls sudo. If the folder is not writable, such as a root-owned `/usr/local/bin`, it prints a `sudo install` command with the path of the downloaded file and exits with an error. A user-writable location such as `~/.local/bin/sfy` avoids this.

The new sfy process gets the environment of the shell that ran `sfy restart`, including its `PATH`. A restart from a login shell therefore gives the workers the same `PATH`, so they find `claude`.

## Stop and start

Run `sfy down` from a terminal outside the agent sessions. It stops the main agent, the workers and the reviewers, then stops the sfy process. It uses the terminal tool settings that the sfy process loaded at start, so a later change to `~/.sfy/config.toml` does not matter. Tasks stay on disk in their current state. Without a sfy process, `sfy down` prints "sfy is not running." Agents cannot run it. It does not stop a registered main agent, because sfy did not start it. It clears the registration and says so.

Run `sfy` or `sfy up` from any terminal. Both do the same, and `sfy up` pairs with `sfy down`. A later `sfy` starts the sfy process and the main agent. It tells the main agent about each task whose worker is gone.

If the main agent's session ended, sfy resumes the main agent's last Claude conversation. It records the conversation id in `sessions/main/conversation-id` in the state folder. The resumed agent gets a short note to read `notes.md` and `status.md`, and keeps its settings and system prompt. If the conversation cannot resume, sfy starts a new one, and the output says which happened. If the main agent's session still runs, `sfy` runs the `show_main` template again, so the main agent appears in your terminal. Workers and reviewers always start new conversations.

## Register a running agent

A Claude Code session that already runs can become the main agent. This needs a template set with a `whoami` template, such as the tmux set. Do these steps once and then each time:

1. Run `sfy hooks install` once from a terminal outside the agent sessions. It adds sfy's idle, permission request and turn end hooks to `~/.claude/settings.json`, or to `$CLAUDE_CONFIG_DIR/settings.json`. It keeps every other setting, changes nothing when you run it again, and prints what it changed. The hooks run `sfy hook --user <event>`. They act only for the registered session and exit at once for every other session.
2. In the session, ask the agent to run `sfy register`. It refuses when a main agent runs or when the set has no `whoami` template. Otherwise it starts the sfy process, records the address and the session id in `sessions/main/` in the state folder, and prints the main prompt, with the additions from `prompts/main.d/`. The agent reads the prompt as its role.

From then on the agent's own `sfy` commands run as `main`, and the operator-only commands `sfy`, `sfy up`, `sfy down` and `sfy hooks install` refuse for it. The prompt arrives as a tool result and not as a system prompt, so a long conversation may drop it. If the agent forgets the role, tell it to read `main-prompt.md` in the state folder. A registered agent gets typed notes and not delivered ones, because its hooks come from the user settings and not from a session folder. `sfy restart` keeps the registered agent and sends it the restart note. When the address is gone, sfy clears the registration, and the next `sfy` starts a main agent as usual. It does not resume the registered conversation.

`docs/register-main.md` holds the design notes.

## Prompts

A file `<home>/prompts/<kind>.md` replaces the default prompt of a kind of agent. The kinds are `main`, `worker`, `reviewer` and `worker-repo`. To add lines without a copy of the whole prompt, put files that end in `.md` in `<home>/prompts/<kind>.d/`. sfy appends them in name order, each after a blank line, to the default or to the replacing prompt. A missing folder adds nothing, and files without `.md` are skipped. `sfy restart` tells the main agent when an addition to its prompt changed.

## A second state folder

Each state folder holds its own tasks, notes, config, sfy process and main agent. To run a second main agent, put a `config.toml` in a new folder and start sfy with that folder:

```
SFY_HOME=~/.sfy-work sfy up
```

Use the same `SFY_HOME` for every other sfy command that targets that folder, such as `sfy down`.

The folder name sets a prefix. The default folder `~/.sfy` has an empty prefix. Any other folder gets its base name without a leading dot, in lower case, with each character outside `a-z0-9-` replaced by `-`, and a final `-`. The folder `~/.sfy-work` gives `sfy-work-`.

The prefix goes in front of every terminal session name, so the main agent of `~/.sfy-work` runs as `sfy-work-main` and its first worker as `sfy-work-worker-t1`. It also goes in the branch of a task that started before branches took the form `<type>/<slug>`, so that task is `sfy/sfy-work-t1`. New branches carry no prefix. Agent names in messages, notes, `sfy send` and `status.md` stay as they are.

## Session settings

sfy writes a settings file for each agent session. To add keys to the file of one kind of session, use a `[session_settings.<kind>]` table in `~/.sfy/config.toml`. The kinds are `main`, `worker` and `reviewer`. sfy copies the keys to the top level of that kind's file, and the provider decides what they mean. This example stops notifications from workers and reviewers:

```toml
[session_settings.worker]
preferredNotifChannel = "notifications_disabled"

[session_settings.reviewer]
preferredNotifChannel = "notifications_disabled"
```

A table that sets `permissions`, `hooks` or `sandbox` is a config error, because sfy writes those keys itself. An unknown kind is also a config error.

## Per-repo config

A `.sfy.toml` file sets per-repo keys. sfy reads it in the repo folder and in each parent folder up to `$HOME`. The nearest file wins, key by key, and all of them apply on top of `~/.sfy/config.toml`. For a task whose repo is a URL, the repo folder is the clone in `repos/` of the state folder. SPEC.md describes the lookup in full.

The only key so far is `no_merge`. With `no_merge = true`, `sfy task integrate` refuses, and the approval note tells the main agent to leave the branch for you. You merge it by hand, then the main agent runs `sfy task complete`. One file covers every work repo below its folder:

```toml
# ~/Developer/work/.sfy.toml
no_merge = true
```

A key that sfy does not know, and a global-only key such as `max_workers` or `multiplexer`, is an error that names the file. In a no-merge repo, a hook in the main agent's settings also blocks a hand `git push`, `git merge`, `git rebase` and `gh pr merge`. Other repos get no extra rule. To let the main agent run `gh pr merge` where it may merge, add `allow = ["Bash(gh pr merge *)"]` to `[session_rules.main]`. The hook does not follow a subshell, a script, `eval`, `bash -c`, an alias or `gh --repo`. SPEC.md lists what it misses.

## Session rules

A `[session_rules.<kind>]` table adds rules to the settings file of one kind of session. The kinds are `main`, `worker` and `reviewer`. Its `allow` list adds permission rules. Its `excluded_commands` list adds command patterns that the session runs outside the sandbox. sfy adds no rule for any tool beyond git by itself.

For example, a setup can let workers open a pull request and let the main agent merge it. Put the rules in the config and the instructions in two prompt additions:

```toml
[session_rules.worker]
allow = ["Bash(<tool> pr create *)"]
excluded_commands = ["<tool> *"]
```

The worker instructions go in `prompts/worker-repo.d/pr.md`. They tell the worker to open the pull request after `git push`. The main agent instructions go in `prompts/main.d/pr.md`. They tell it to merge the pull request instead of running `sfy task integrate`. Replace `<tool>` with the command that talks to your hosting service. The main agent's settings deny `gh pr merge`, so the tool must be another command, or you merge the pull request yourself.

## Terminal tools

sfy runs each agent in a multiplexer. It ships a set for tmux. Any other terminal tool plugs in through a template set under `[multiplexers.<name>]` in `~/.sfy/config.toml`. `docs/herdr.md` and `docs/rex.md` hold example sets. SPEC.md lists every template.

The `show_main` template makes the main agent's session visible. sfy runs `show` for workers and reviewers. An empty `show` hides them. Without `show_main`, the main agent uses `show`. When sfy starts the main agent in place, it runs `show_main` after it records the address and before it hands the terminal to the main agent, so a template set can arrange the terminal around it, for example to open side panes. A failed `show_main` prints a warning and the main agent still starts.

The `whoami` template prints the address of the terminal that it runs in. With it, `sfy` and `sfy up` start the main agent in the terminal where you run them, and do not open a new session. The tmux set has a `whoami` template. A set without one starts the main agent through the `start` template. If `whoami` fails or prints nothing, sfy does the same.

In that case sfy records the address in `sessions/main/address` in the state folder. It then uses the address as `{name}` for the main agent in the `send`, `notify`, `alive`, `stop`, `show` and `show_main` templates. When `alive` reports the address gone, sfy removes the record, so the next `sfy` runs the main agent in place again. `sfy down` runs `stop` on the address, so it ends that terminal or session.
