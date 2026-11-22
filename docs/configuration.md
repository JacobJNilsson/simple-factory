# Configuration and operation

This page holds the details of running sfy. SPEC.md holds the full behaviour. The factory folder is `~/.sfy` unless a working folder or `SFY_HOME` names another one.

## Restart

Run `sfy restart` from a terminal outside the agent sessions, or ask the main agent to run it. It stops the sfy process and starts it from the installed binary. The main agent, workers and reviewers keep running, and their tasks stay on disk. The main agent gets a note, which says when its prompt changed. `sfy restart` does not build or install sfy.

`sfy restart --update` first upgrades sfy and restarts only when that succeeds. When Homebrew installed the running binary, it updates the tap and runs `brew upgrade sfy`. Otherwise it downloads the newest release from GitHub, checks the SHA-256 sum, writes the binary next to the running one and renames it over the running one. It never calls sudo. If the folder is not writable, such as a root-owned `/usr/local/bin`, it prints a `sudo install` command with the path of the downloaded file and exits with an error. A user-writable location such as `~/.local/bin/sfy` avoids this.

The new sfy process gets the environment of the shell that ran `sfy restart`, including its `PATH`. A restart from a login shell therefore gives the workers the same `PATH`, so they find `claude`.

## Stop and start

Run `sfy down` from a terminal outside the agent sessions. It stops the main agent, the workers and the reviewers, then stops the sfy process. It uses the terminal tool settings that the sfy process loaded at start, so a later change to `~/.sfy/config.toml` does not matter. Tasks stay on disk in their current state. Without a sfy process, `sfy down` prints "sfy is not running." Agents cannot run it. It does not stop a registered main agent, because sfy did not start it. It clears the registration and says so.

Run `sfy` or `sfy up` from any terminal. Both do the same, and `sfy up` pairs with `sfy down`. A later `sfy` starts the sfy process and the main agent. It tells the main agent about each task whose worker is gone.

If the main agent's session ended, sfy resumes the main agent's last Claude conversation. It records the conversation id in `sessions/main/conversation-id` in the factory folder. The resumed agent gets a short note to read `notes.md` and `status.md`, and keeps its settings and system prompt. If the conversation cannot resume, sfy starts a new one, and the output says which happened. If the main agent's session still runs, `sfy` runs the `show_main` template again, so the main agent appears in your terminal. Workers and reviewers always start new conversations.

## Run headless

By default `sfy up` runs the sfy process in a window of your terminal tool, and the window shows the log. Set `headless = true` in `config.toml` to run it with no window:

```toml
# ~/.sfy/config.toml
headless = true
```

The key is global, and it defaults to `false`. With it, `sfy up` starts `sfy serve` as a detached process. The process leads its own session, reads stdin from `/dev/null`, runs in the factory folder and gets the same environment as a windowed process. Its stdout and stderr go to `sfy.log` in the factory folder, so `tail -f ~/.sfy/sfy.log` shows what the window showed. The main agent, the workers and the reviewers keep their windows. This works on Linux and macOS and needs no systemd or launchd.

- `sfy down` stops the agents and the sfy process, as without the setting.
- To stop the sfy process alone, run `kill $(cat ~/.sfy/sfy.pid)`. The process handles `SIGTERM` by removing its socket and lock file. The agents keep running, and the next `sfy` starts the sfy process again.
- `sfy restart` and `sfy restart --update` stop the old process, wait until it releases the socket and the lock, start the new process detached and wait until it answers. They print the new process as without the setting. A call that arrives in the short gap fails with "the sfy process does not run". The gap ends when the new process answers.
- When the running process sits in a window and the config now says headless, `sfy restart` ends the window with the old process and starts the new one detached. Remove the setting to move it back into a window the same way.
- A second `sfy` finds the process through its socket and the lock `sfy.lock` in the factory folder. It starts nothing, and a second `sfy serve` refuses with "a sfy process already runs". `sfy.pid` holds the pid of the process that holds the lock.

## Register a running agent

A Claude Code session that already runs can become the main agent. This needs a template set with a `whoami` template, such as the tmux set. Do these steps once and then each time:

1. Run `sfy hooks install` once from a terminal outside the agent sessions. It adds sfy's idle, permission request and turn end hooks to `~/.claude/settings.json`, or to `$CLAUDE_CONFIG_DIR/settings.json`. It keeps every other setting, changes nothing when you run it again, and prints what it changed. The hooks run `sfy hook --user <event>`. They act only for the registered session and exit at once for every other session.
2. In the session, ask the agent to run `sfy register`. It refuses when a main agent runs or when the set has no `whoami` template. Otherwise it starts the sfy process, records the address and the session id in `sessions/main/` in the factory folder, and prints the main prompt, with the additions from `prompts/main.d/`. The agent reads the prompt as its role.

From then on the agent's own `sfy` commands run as `main`, and the operator-only commands `sfy`, `sfy up`, `sfy down` and `sfy hooks install` refuse for it. The prompt arrives as a tool result and not as a system prompt, so a long conversation may drop it. If the agent forgets the role, tell it to read `main-prompt.md` in the factory folder. A registered agent gets typed notes and not delivered ones, because its hooks come from the user settings and not from a session folder. `sfy restart` keeps the registered agent and sends it the restart note. When the address is gone, sfy clears the registration, and the next `sfy` starts a main agent as usual. It does not resume the registered conversation.

`docs/register-main.md` holds the design notes.

## Prompts

sfy reads prompts from two places: the machine layer `~/.sfy/prompts/`, which reaches every factory, and `prompts/` in the factory folder. A file `prompts/<kind>.md` replaces the default prompt of a kind of agent, and the factory's file wins over the machine's. The kinds are `main`, `worker`, `reviewer` and `worker-repo`.

To add lines without a copy of the whole prompt, put files that end in `.md` in `prompts/<kind>.d/` of either place. sfy merges the two folders by file name. A factory file replaces the machine file with the same name, and an empty factory file removes it. sfy appends the result in name order, each after a blank line, to the default or to the replacing prompt. A missing folder adds nothing, and files without `.md` are skipped. `sfy restart` tells the main agent when an addition to its prompt changed.

For example, a writing rule for every factory goes in `~/.sfy/prompts/worker.d/writing.md`. A factory that needs another rule puts its own `writing.md` in `~/.sfy/factories/acme/prompts/worker.d/`. A factory that needs no rule puts an empty `writing.md` there.

## A factory per folder

A factory has a factory folder with its own tasks, notes, sfy process and main agent. It also has a working folder, which you work in and the main agent runs in. Stand in a folder and run `sfy up` to get the factory of that working folder. For example, `sfy up` in `~/Developer/acme` creates the factory `acme` and starts its main agent with `~/Developer/acme` as its working directory.

sfy writes no file in the working folder. It keeps the factory folder in `~/.sfy/factories/<name>/`. The name is the base name of the working folder in lower case, with each character outside `a-z0-9-` replaced by `-`. A second working folder with the same base name gets a suffix, as in `web-2`. The file `folder` in the factory folder records the absolute path of the working folder.

Each command picks its factory folder in this order:

1. `SFY_HOME`, when set. Agents get it from sfy.
2. The factory of the nearest registered working folder at or above the current folder. The home folder is never a working folder.
3. The default factory, `~/.sfy`.

So `sfy status` in `~/Developer/acme/web/src` shows `acme`, and `sfy up` in `~/Developer/acme/web` joins `acme` without a new factory. In the home folder or in an unrelated folder, a command uses `~/.sfy`. `sfy status` and `sfy task list` print `Factory: <name> (<folder>)` first, so you see which factory answered.

Every factory reads `~/.sfy/config.toml`. A `config.toml` in the factory folder is optional. The two files merge key by key at every depth, so the factory file can change one key of a table and keep the others. A list in the factory file replaces the machine file's list.

Each of the two places can also hold a `config.d/` folder. sfy reads its `.toml` files in name order before the `config.toml` of the same place, and they merge the same way. A link in `config.d/` lets several factories share one file and keep their own `config.toml` on top:

```
ln -s ~/shared/factory.toml ~/.sfy/factories/acme/config.d/50-shared.toml
```

sfy skips a file whose name does not end in `.toml`. A file that does not parse is an error that names the file. Run `sfy config` to see the merged result:

```
$ sfy config
# Factory: acme (/home/you/Developer/acme)
# Layers, lowest first: /home/you/.sfy, /home/you/.sfy/factories/acme

# Settings
max_workers = 1 # /home/you/.sfy/factories/acme/config.toml
review_rounds = 3 # default
...

# Prompts
# main: base default
# worker: base default
#   writing.md: /home/you/.sfy/prompts/worker.d/writing.md
...

# Factory files
# checks
```

Each settings line names the file that set it last, or `default`. The prompts part lists the files of each kind in the order that sfy appends them, and a removed file names the file that removed it. The factory files are the names in the factory folder that sfy neither owns as state nor reads as definition. `sfy config` reads files only and needs no sfy process. It exits with an error when a file does not load, so a script can run it as a check. It prints the values of `session_settings.<kind>.env` as `<set>`.

To pick any other folder as a factory folder, set `SFY_HOME`:

```
SFY_HOME=~/.sfy-work sfy up
```

Use the same `SFY_HOME` for every other sfy command that targets that folder, such as `sfy down`.

The folder name sets a prefix. The default folder `~/.sfy` has an empty prefix. Any other folder gets its base name without a leading dot, in lower case, with each character outside `a-z0-9-` replaced by `-`, and a final `-`. The folder `~/.sfy-work` gives `sfy-work-`, and the factory `~/.sfy/factories/work` gives `work-`.

The prefix goes in front of every terminal session name, so the main agent of `~/.sfy-work` runs as `sfy-work-main` and its first worker as `sfy-work-worker-t1`. It also goes in the branch of a task that started before branches took the form `<type>/<slug>`, so that task is `sfy/sfy-work-t1`. New branches carry no prefix. Agent names in messages, notes, `sfy send` and `status.md` stay as they are.

## Session settings

sfy writes a settings file for each agent session. To add keys to the file of one kind of session, use a `[session_settings.<kind>]` table in `~/.sfy/config.toml` or in the factory's `config.toml`. The kinds are `main`, `worker` and `reviewer`. sfy copies the keys to the top level of that kind's file, and the provider decides what they mean. This example stops notifications from workers and reviewers:

```toml
[session_settings.worker]
preferredNotifChannel = "notifications_disabled"

[session_settings.reviewer]
preferredNotifChannel = "notifications_disabled"
```

A value can be any TOML value, including an array or a table. sfy writes it unchanged as JSON. This example sets Claude Code's `attribution` object for the main agent:

```toml
[session_settings.main]
attribution = { commit = "", pr = "" }
```

A table may also set `permissions`, `hooks` and `sandbox`, which sfy writes itself. sfy merges them with its own keys. The table's `permissions.allow` and `permissions.deny` rules follow sfy's rules, and its hooks follow sfy's hooks of the same event. A key that sfy does not write is added. Where sfy writes a value, sfy's value wins, so the identity token, the note delivery and the sandbox keep working. This example adds a deny rule and a network allow list for workers:

```toml
[session_settings.worker.permissions]
deny = ["Bash(curl *)"]

[session_settings.worker.sandbox.network]
allowedDomains = ["example.com"]
```

An unknown kind is a config error.

Worker and reviewer files set `"promptSuggestionEnabled": false` by default, which turns off Claude Code's prompt suggestions. To turn them back on, set `promptSuggestionEnabled = true` in `[session_settings.worker]` or `[session_settings.reviewer]`. The main agent keeps Claude Code's default.

## Per-repo config

Per-repo keys come from two kinds of file.

- `<repo folder>/.sfy/config.toml` holds the settings of one repo. You can keep `.sfy/` out of git with `core.excludesFile`, or commit it. sfy reads it only from the repo folder, never from a task's worktree or branch, so a worker cannot change its own settings.
- A `.sfy.toml` file in the repo folder or in a parent folder up to `$HOME` covers every repo below it.

For a task whose repo is a URL, the repo folder is the clone in `repos/` of the factory folder. A committed `.sfy/` arrives with each fetch of the default branch. You can also put an untracked one there by hand.

Key by key, the first file that sets a key wins. The order is `.sfy/config.toml`, then each `.sfy.toml` from the nearest up, then the factory `config.toml`, then `~/.sfy/config.toml`. A file that does not parse counts as an error, and the repo then counts as no-merge. SPEC.md describes the lookup in full.

The keys are `no_merge` and `draft_pr`. With `draft_pr = true`, the note that asks the worker to push also says that any PR must be a draft, with `gh pr create --draft`. sfy opens no PR itself. The default is `false`.

With `no_merge = true`, `sfy task integrate` refuses, and the approval note tells the main agent to leave the branch for you. You merge it by hand, then the main agent runs `sfy task complete`. One file covers every work repo below its folder:

```toml
# ~/Developer/work/.sfy.toml
no_merge = true
```

A key that sfy does not know, and a global-only key such as `max_workers` or `multiplexer`, is an error that names the file. In a no-merge repo, a hook in the main agent's settings also blocks a hand `git push`, `git merge`, `git rebase` and `gh pr merge`. Other repos get no extra rule. To let the main agent run `gh pr merge` where it may merge, add `allow = ["Bash(gh pr merge *)"]` to `[session_rules.main]`. The hook does not follow a subshell, a script, `eval`, `bash -c`, an alias or `gh --repo`. SPEC.md lists what it misses.

## Hold the push of a task

By default a worker on a repo with a remote pushes its branch after a review passes. To keep one task off the remote until you approve, add a `## Push` section to its task file:

```markdown
## Push

hold
```

The value `hold` is the only one. Any other value is a task file error. Other tasks of the same repo push as usual.

- A held task's worker has no push allow rule, and its settings deny every `git push`. The hold does not rely on the prompt.
- When the review passes, the task moves to the state `held`. sfy ends the sessions, keeps the workspace, and tells the main agent the branch name, the repo folder, and that the push waits for you.
- Tell the main agent to push. It runs `sfy task push <id>`, and sfy starts a worker that pushes the branch. The task then moves to `approved`, and integrate or no-merge works as usual.
- `sfy task feedback <id> <text>` on a held task keeps the hold. Feedback after the release also restores it.
- A repo with no remote has nothing to push, so the hold changes nothing there.
- The hold works with a `## Branch` section too. The released worker then pushes to the existing branch with its lease.

## Session rules

A `[session_rules.<kind>]` table adds rules to the settings file of one kind of session. The kinds are `main`, `worker` and `reviewer`. Its `allow` list adds permission rules. Its `excluded_commands` list adds command patterns that the session runs outside the sandbox. sfy adds no rule for any tool beyond git by itself.

For example, a setup can let workers open a pull request and let the main agent merge it. Put the rules in the config and the instructions in two prompt additions:

```toml
[session_rules.worker]
allow = ["Bash(<tool> pr create *)"]
excluded_commands = ["<tool> *"]
```

The worker instructions go in `prompts/worker-repo.d/pr.md`. They tell the worker to open the pull request after `git push`. The main agent instructions go in `prompts/main.d/pr.md`. They tell it to merge the pull request instead of running `sfy task integrate`. Replace `<tool>` with the command that talks to your hosting service. The main agent's settings deny `gh pr merge`, so the tool must be another command, or you merge the pull request yourself.

## Note delivery

sfy delivers notes to a Claude Code session through a hook on the session's note file. Claude Code runs that hook between tool calls or at the end of a turn, so a long tool call delays a note. sfy types the note into the pane only when the session waits for input and the hook left the text unread. It never types into a busy pane.

`note_fallback_after` in `~/.sfy/config.toml` sets how long sfy waits for the hook before it types. The value is a Go duration, such as `90s` or `10m`. The default is `5m`. It covers a long tool call and a slow hook, so a note does not land in text that you are typing. A turn end starts the wait again. Lower the value only when your sessions have no working note hook and a late note costs you more than a typed one.

```toml
note_fallback_after = "10m"
```

## Run the task's check before review

A task may have a `## Check` section with one command line. When the worker reports done, sfy runs that command in the task's workspace. A pass starts the review. A failure goes back to the worker as a message, with the command, the error and the last 8 KiB of the output, and no reviewer starts. A failed check counts as a review round.

The command runs in a sandbox with the worker's `allow_write` and `deny_read` lists. It uses `bwrap` on Linux and `sandbox-exec` on macOS, and it leaves the network open. A check that needs another write path, such as a build cache, needs that path in `allow_write`.

`check_timeout` in `~/.sfy/config.toml` sets how long a check may run. The value is a Go duration. The default is `20m`. It is a global setting, so `.sfy.toml` refuses it.

```toml
check_timeout = "40m"
```

## Terminal tools

sfy runs each agent in a multiplexer. It ships a set for tmux. Any other terminal tool plugs in through a template set under `[multiplexers.<name>]` in `~/.sfy/config.toml`. `docs/herdr.md` and `docs/rex.md` hold example sets. SPEC.md lists every template.

The `show_main` template makes the main agent's session visible. sfy runs `show` for workers and reviewers. An empty `show` hides them. Without `show_main`, the main agent uses `show`. When sfy starts the main agent in place, it runs `show_main` after it records the address and before it hands the terminal to the main agent, so a template set can arrange the terminal around it, for example to open side panes. A failed `show_main` prints a warning and the main agent still starts.

Every template can use `{home}`, the absolute path of the factory folder of the running factory. A `show_main` template can use it to open `notes.md` and `status.md` of the right factory. sfy also sets `SFY_HOME` to that folder in the environment of every template command, so a script that reads `SFY_HOME` finds the right factory, also when `sfy up` picks the factory from the folder rules.

The `whoami` template prints the address of the terminal that it runs in. With it, `sfy` and `sfy up` start the main agent in the terminal where you run them, and do not open a new session. The tmux set has a `whoami` template. A set without one starts the main agent through the `start` template. If `whoami` fails or prints nothing, sfy does the same.

In that case sfy records the address in `sessions/main/address` in the factory folder. It then uses the address as `{name}` for the main agent in the `send`, `notify`, `alive`, `stop`, `show` and `show_main` templates. When `alive` reports the address gone, sfy removes the record, so the next `sfy` runs the main agent in place again. `sfy down` runs `stop` on the address, so it ends that terminal or session.
