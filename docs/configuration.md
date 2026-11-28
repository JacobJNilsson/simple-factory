# Configuration and operation

This page holds the details of running sfy. SPEC.md holds the full behaviour. The factory folder is `~/.sfy` unless a working folder or `SFY_HOME` names another one.

## Restart

Run `sfy restart` from a terminal outside the agent sessions, or ask the lead to run it. It stops the sfy process and starts it from the installed binary. The lead, workers and reviewers keep running, and their tasks stay on disk. The lead gets a note, which says when its prompt changed. `sfy restart` does not build or install sfy.

At each start, the sfy process writes the flat result of the definition to `definition.toml` in the factory folder. Before it stops the old process, `sfy restart` compares the new definition with that file and prints each change. The lead's note holds the same lines:

```
$ sfy restart
Setting models.worker changed from "sonnet" to "opus".
The worker prompt file worker.d/writing.md was added.
Stopped the sfy process.
Started the sfy process.
Told the lead.
```

When nothing changed, it prints "The definition did not change since the last start." The first start writes the file and reports nothing. The file and the report show the values of `session_settings.<kind>.env` as `<set>`.

`sfy restart --update` first upgrades sfy and restarts only when that succeeds. When Homebrew installed the running binary, it updates the tap and runs `brew upgrade sfy`. Otherwise it downloads the newest release from GitHub, checks the SHA-256 sum, writes the binary next to the running one and renames it over the running one. It never calls sudo. If the folder is not writable, such as a root-owned `/usr/local/bin`, it prints a `sudo install` command with the path of the downloaded file and exits with an error. A user-writable location such as `~/.local/bin/sfy` avoids this.

The new sfy process gets the environment of the shell that ran `sfy restart`, including its `PATH`. A restart from a login shell therefore gives the workers the same `PATH`, so they find `claude`.

## Stop and start

Run `sfy down` from a terminal outside the agent sessions. It stops the workers and the reviewers, then stops the sfy process. It never stops the lead, whether sfy started it, it runs in your terminal, or you registered it. The lead's records stay, so the next `sfy` finds the lead. It uses the terminal tool settings that the sfy process loaded at start, so a later change to `~/.sfy/config.toml` does not matter. Tasks stay on disk in their current state. Without a sfy process, `sfy down` prints "sfy is not running." Agents cannot run it.

Run `sfy` or `sfy up` from any terminal. Both do the same, and `sfy up` pairs with `sfy down`. A later `sfy` starts the sfy process. When the lead still runs, the lead gets the note "sfy restarted." When the lead's session ended, `sfy` starts it again. The new sfy process tells the lead about each task whose worker or reviewer is gone.

If the lead's session ended, sfy resumes the lead's last Claude conversation. It records the conversation id in `sessions/lead/conversation-id` in the factory folder. The resumed agent gets a short note to follow the start rules of its memory section, and keeps its settings and system prompt. If the conversation cannot resume, sfy starts a new one, and the output says which happened. If the lead's session still runs, `sfy` runs the `show_lead` template again, so the lead appears in your terminal. Every worker and reviewer start records a new conversation. sfy resumes it only to wake a closed session, as the section Wake a closed session describes.

## Old names of the lead

An older sfy called the lead `main`. sfy still reads each old name as the new one, and prints a warning on stderr:

| Old name | New name |
|---|---|
| `sfy send main <text>` | `sfy send lead <text>` |
| `models.main` | `models.lead` |
| `[session_rules.main]` | `[session_rules.lead]` |
| `[session_settings.main]` | `[session_settings.lead]` |
| `show_main` in a template set | `show_lead` |
| `prompts/main.md` | `prompts/lead.md` |
| `prompts/main.d/` | `prompts/lead.d/` |

A config file that sets both names of one key fails to load. A `lead.d` file wins over a `main.d` file of the same name in the same folder. Run `sfy config` to list each old name that the factory still uses, under "Old names", and rename them. A later release removes the old names.

To upgrade a running factory, install the new release and run `sfy restart` in each factory, one at a time. The first start moves `sessions/main/`, `inbox/main/` and `main-prompt.md` to their new names, and leaves a link with each old name. When the lead still runs in its old terminal, `<prefix>main`, sfy records that terminal in `sessions/lead/address` and keeps using it. The lead keeps its conversation, and its old `SFY_AGENT=main` still works. When that terminal ends, the next start runs the lead as `<prefix>lead`.

To roll back, first check whether the lead runs as `<prefix>lead`. If it does, end the lead's conversation first, because an older sfy would not find it and would start a second lead. `sfy down` does not stop the lead. Then install the older release and run `sfy restart`, or a plain `sfy` after `sfy down`. SPEC.md, section Old names of the lead, gives the full rules.

## Run headless

By default `sfy up` runs the sfy process in a window of your terminal tool, and the window shows the log. Set `headless = true` in `config.toml` to run it with no window:

```toml
# ~/.sfy/config.toml
headless = true
```

The key is global, and it defaults to `false`. With it, `sfy up` starts `sfy serve` as a detached process. The process leads its own session, reads stdin from `/dev/null`, runs in the factory folder and gets the same environment as a windowed process. Its stdout and stderr go to `sfy.log` in the factory folder, so `tail -f ~/.sfy/sfy.log` shows what the window showed. The lead, the workers and the reviewers keep their windows. This works on Linux and macOS and needs no systemd or launchd.

For one start without the setting, run `sfy up --headless`. It prepares the factory folder as `sfy up` does and starts the sfy process detached, with its log in `sfy.log`. It starts no lead, and it leaves a running lead and a registered lead alone. When the sfy process already answers, it prints "The sfy process already runs." and starts nothing. A plain `sfy restart` follows the config, so it moves such a process back into a window when the config does not say headless, and it starts a missing lead. Run `sfy restart --headless` to keep the process detached. It starts the new process detached, starts no lead, and leaves a registered lead alone. A running lead still gets the note "sfy restarted." It also works with `--update`, in any order.

- `sfy down` stops the workers, the reviewers and the sfy process, as without the setting.
- To stop the sfy process alone, run `kill $(cat ~/.sfy/sfy.pid)`. The process handles `SIGTERM` by removing its socket and lock file. The agents keep running, and the next `sfy` starts the sfy process again.
- `sfy restart` and `sfy restart --update` stop the old process, wait until it releases the socket and the lock, start the new process detached and wait until it answers. They print the new process as without the setting. A call that arrives in the short gap fails with "the sfy process does not run". The gap ends when the new process answers.
- When the running process sits in a window and the config now says headless, `sfy restart` ends the window with the old process and starts the new one detached. Remove the setting to move it back into a window the same way.
- A second `sfy` finds the process through its socket and the lock `sfy.lock` in the factory folder. It starts nothing, and a second `sfy serve` refuses with "a sfy process already runs". `sfy.pid` holds the pid of the process that holds the lock.

## Register a running agent

A Claude Code session that already runs can become the lead. This needs a template set with a `whoami` template, such as the tmux set. Do these steps once and then each time:

1. Run `sfy hooks install` once from a terminal outside the agent sessions. It adds sfy's idle, permission request and turn end hooks to `~/.claude/settings.json`, or to `$CLAUDE_CONFIG_DIR/settings.json`. It keeps every other setting, changes nothing when you run it again, and prints what it changed. The hooks run `sfy hook --user <event>`. They act only for the registered session and exit at once for every other session.
2. In the session, ask the agent to run `sfy register`. It refuses when a lead runs or when the set has no `whoami` template. Otherwise it starts the sfy process, records the address and the session id in `sessions/lead/` in the factory folder, and prints the lead's prompt, with the additions from `prompts/lead.d/`. The agent reads the prompt as its role.

From then on the agent's own `sfy` commands run as `lead`, and the operator-only commands `sfy`, `sfy up`, `sfy down` and `sfy hooks install` refuse for it. The prompt arrives as a tool result and not as a system prompt, so a long conversation may drop it. If the agent forgets the role, tell it to read `lead-prompt.md` in the factory folder. A registered agent gets typed notes and not delivered ones, because its hooks come from the user settings and not from a session folder. `sfy restart` keeps the registered agent and sends it the restart note. When the address is gone, sfy clears the registration, and the next `sfy` starts a lead as usual. It does not resume the registered conversation.

`docs/register-main.md` holds the design notes.

## Prompts

sfy reads prompts from every layer of a factory: its factory folder and each `.sfy/` folder on the walk, as the section Override a factory from a folder describes. A file `prompts/<kind>.md` replaces the default prompt of a kind of agent, and the file of the nearest layer wins. The kinds are `lead`, `lead-memory`, `worker`, `reviewer` and `worker-repo`.

The lead's prompt ends with the `lead-memory` part. The default part tells the lead to keep `notes.md` and to read it with `status.md` at each start. A factory whose lead keeps its memory elsewhere puts its own rules in `prompts/lead-memory.md`. The lead's first messages then point at those rules, and `sfy up` creates no `notes.md`.

To add lines without a copy of the whole prompt, put files that end in `.md` in `prompts/<kind>.d/` of any layer. sfy merges the folders by file name. A file of a nearer layer replaces the file with the same name in a farther layer, and an empty file removes it. sfy appends the result in name order, each after a blank line, to the default or to the replacing prompt. A missing folder adds nothing, and files without `.md` are skipped. `sfy restart` tells the lead when an addition to its prompt changed.

For example, a writing rule for every factory goes in `~/.sfy/prompts/worker.d/writing.md`. A working folder that needs another rule puts its own `writing.md` in `<working folder>/.sfy/prompts/worker.d/`. A working folder that needs no rule puts an empty `writing.md` there.

## Skills

To give the sessions of a factory their own skills, put skill folders in `skills/` of any layer: the factory folder or a `.sfy/` folder on the walk. Each skill folder holds a `SKILL.md`:

```
~/Developer/acme/.sfy/skills/release-notes/SKILL.md
```

The lead, the workers and the reviewers of that factory all get the skills of every layer. For a name that two layers hold, the nearest layer wins. Claude Code shows each skill as `factory:<skill>`, as in `factory:release-notes`. A skill in `~/.sfy/skills/` reaches every factory of the machine, because `~/.sfy` is the farthest override.

At each session start, sfy writes a small plugin into `sessions/<name>/plugin/` of the factory folder and passes it to claude with `--plugin-dir`. The plugin holds a manifest and a `skills/` folder with one link for each skill, so an edit to a skill applies at the next session start. A plugin folder gives a session no edit rights, so no session can change the skills without a permission grant. A link in `skills/` is fine. Run `sfy config` to see the skills that sfy found.

## A factory per folder

A factory has a factory folder with its own tasks, notes, sfy process and lead. It also has a working folder, which you work in and the lead runs in. Stand in a folder and run `sfy up` to get the factory of that working folder. For example, `sfy up` in `~/Developer/acme` creates the factory `acme` and starts its lead with `~/Developer/acme` as its working directory.

sfy writes no file in the working folder. It keeps the factory folder in `~/.sfy/factories/<name>/`. The name is the name prefix, then the base name of the working folder in lower case, with each character outside `a-z0-9-` replaced by `-`. A second working folder with the same base name gets a suffix, as in `web-2`. The file `folder` in the factory folder records the absolute path of the working folder.

The name prefix comes from the variable `SFY_NAME_PREFIX`, which a wrapper command around sfy can set. It holds lowercase letters, digits and `-`, ends in `-`, and has at most 16 characters, such as `ex-`. Without the variable, the name prefix is empty. A factory that sfy creates with a name prefix records it in the file `name-prefix` in its factory folder. So one working folder can have a factory `web` and a factory `ex-web`, each with its own tasks, lead and terminal session names. A factory folder without the file has the empty name prefix.

Each command picks its factory folder in this order:

1. `SFY_HOME`, when set. Agents get it from sfy.
2. The factory with the same name prefix of the nearest registered working folder at or above the current folder that has one. The home folder is never a working folder.
3. The default factory, `~/.sfy`, when the name prefix is empty. With a name prefix, the command fails with "no factory with the name prefix `<prefix>` here".

So `sfy status` in `~/Developer/acme/web/src` shows `acme`, and `sfy up` in `~/Developer/acme/web` joins `acme` without a new factory. In the home folder or in an unrelated folder, a command with an empty name prefix uses `~/.sfy`. `sfy status` and `sfy task list` print `Factory: <name> (<folder>)` first, so you see which factory answered.

When a folder has factories of other name prefixes only, `sfy up` and `sfy restart` refuse and create nothing. The error names each other factory folder and its name prefix. It tells you to run the wrapper that sets `SFY_NAME_PREFIX=<prefix>`, or to set `SFY_HOME`. This catches a plain `sfy` that you meant to run as a wrapper command. To make the factory on purpose, run `sfy up --new-factory`, or set `SFY_HOME`.

A wrapper command can ask for the factory folder with `sfy factory-folder`, or with `sfy factory-folder --create` to make it. The second form makes the factory also when the folder has factories of other name prefixes only, so a wrapper's first start works. It then prints one line on stderr that names the other factory folder. On stdout the command prints the folder and nothing else.

A program that must know whether a folder already has a factory runs `sfy factory-folder --check`. It prints the factory folder and exits 0 when a factory exists, also from a sub-folder of the working folder. It prints nothing and exits 4 when the folder has no factory with the name prefix. The default factory `~/.sfy` counts as none. Any other failure exits 1 with the error on stderr.

To give a sub-folder of a working folder its own factory, run `sfy factory-folder --create --exact` in the sub-folder. It ignores the factories of the folders above, so it creates a factory for the sub-folder even when the parent's factory has the same name prefix. On stdout it prints the factory folder and nothing else. A later run returns the same factory. After that, a command in the sub-folder finds the new factory, and a command in the parent folder still finds the parent's factory. `sfy factory-folder --check --exact` exits 0 only when a factory is registered for exactly the current folder, and 4 otherwise.

## Override a factory from a folder

A factory reads its definition from layers, lowest first: the built-in defaults of sfy, its factory folder, and the folder overrides on a walk. A folder override is a `.sfy/` folder with the shape of a factory folder: `config.toml`, `config.d/`, `prompts/` and `skills/`. The walk starts at the working folder and goes up through its parents to your home folder, and each folder adds its `.sfy/`. The nearest override wins, key by key. `~/.sfy` is the `.sfy/` of the home folder, so it is the farthest override. It reaches every factory, and it keeps the settings of the machine, such as the terminal template sets.

For example, the factory `acme` has the working folder `~/Developer/acme`. It reads these layers, lowest first:

1. The built-in defaults.
2. `~/.sfy/factories/acme/`, its factory folder.
3. `~/.sfy/`.
4. `~/Developer/.sfy/`, when it exists.
5. `~/Developer/acme/.sfy/`, when it exists.
6. `~/.sfy/factories/acme/local/`, its local layer, when it exists.

The local layer is the nearest layer of one factory. It wins over `~/.sfy` and over every folder override, and it needs no file in the working folder. A program that starts a factory can put that factory's own settings there. sfy skips it under the same rules as a folder override.

A file in `~/Developer/acme/.sfy/config.toml` that sets `max_workers = 3` changes only the factories whose working folder lies there or below:

```toml
# ~/Developer/acme/.sfy/config.toml
max_workers = 3
```

The files merge key by key at every depth, so an override can change one key of a table and keep the others. A list in an override replaces the farther layer's list as a whole. Each layer can also hold a `config.d/` folder. sfy reads its `.toml` files in name order before the `config.toml` of the same layer, and they merge the same way. A link in `config.d/` lets several factories share one file and keep their own `config.toml` on top:

```
ln -s ~/shared/factory.toml ~/Developer/acme/.sfy/config.d/50-shared.toml
```

sfy reads an override only when you own its `.sfy/` folder and no one else can write it. It skips any other override, and `sfy config` names it with the reason. `sfy up` and `sfy restart` print a warning for each. The `.sfy/` folder of a repo that is not on the walk follows the rule in the section Per-repo config. sfy writes no file in an override.

sfy skips a file whose name does not end in `.toml`. A file that does not parse is an error that names the file. Run `sfy config` to see the merged result:

```
$ sfy config
# Factory: acme (/home/you/Developer/acme)
# Layers, lowest first:
# - built-in defaults
# - /home/you/.sfy/factories/acme (factory folder)
# - /home/you/.sfy (override)
# - /home/you/Developer/acme/.sfy (override)

# Settings
max_workers = 3 # /home/you/Developer/acme/.sfy/config.toml
review_rounds = 3 # default
...

# Process
# start = build # default
# list build = worker, check, reviewer # default
#   pass: if remote and hold held, if remote ship, otherwise land
#   fail: at check build with a new round, at reviewer build, otherwise escalated
...

# Prompts
# lead: base default
# worker: base default
#   writing.md: /home/you/.sfy/prompts/worker.d/writing.md
...

# Skills
# release-notes: /home/you/Developer/acme/.sfy/skills/release-notes

# Factory files
# checks

# Old names
```

Each settings line names the file that set it last, or `default`. The process part lists each list with its agents, its branches and the file that set its agents last, then each agent. The prompts part lists the files of each kind in the order that sfy appends them, and a removed file names the file that removed it. The header lists each layer with its role, and each skipped override with its reason. The skills part lists each skill with the folder of the nearest layer that holds it. The factory files are the names in the factory folder that sfy neither owns as state nor reads as definition. The old names part lists each old name of the lead that the factory still uses, as the section Old names of the lead describes. `sfy config` reads files only and needs no sfy process. It exits with an error when a file does not load, so a script can run it as a check. It prints the values of `session_settings.<kind>.env` as `<set>`.

To pick any other folder as a factory folder, set `SFY_HOME`:

```
SFY_HOME=~/.sfy-work sfy up
```

Use the same `SFY_HOME` for every other sfy command that targets that folder, such as `sfy down`.

The folder name sets a prefix. The default folder `~/.sfy` has an empty prefix. Any other folder gets its base name without a leading dot, in lower case, with each character outside `a-z0-9-` replaced by `-`, and a final `-`. The folder `~/.sfy-work` gives `sfy-work-`, and the factory `~/.sfy/factories/work` gives `work-`.

The prefix goes in front of every terminal session name, so the lead of `~/.sfy-work` runs as `sfy-work-lead` and its first worker as `sfy-work-worker-t1`. It also goes in the branch of a task that started before branches took the form `<type>/<slug>`, so that task is `sfy/sfy-work-t1`. New branches carry no prefix. Agent names in messages, notes, `sfy send` and `status.md` stay as they are.

## Session settings

sfy writes a settings file for each agent session. To add keys to the file of one kind of session, use a `[session_settings.<kind>]` table in the `config.toml` of any layer, such as `~/.sfy/config.toml`. The kinds are `lead`, `worker` and `reviewer`. sfy copies the keys to the top level of that kind's file, and the provider decides what they mean. This example stops notifications from workers and reviewers:

```toml
[session_settings.worker]
preferredNotifChannel = "notifications_disabled"

[session_settings.reviewer]
preferredNotifChannel = "notifications_disabled"
```

A value can be any TOML value, including an array or a table. sfy writes it unchanged as JSON. This example sets Claude Code's `attribution` object for the lead:

```toml
[session_settings.lead]
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

Worker and reviewer files set `"promptSuggestionEnabled": false` by default, which turns off Claude Code's prompt suggestions. To turn them back on, set `promptSuggestionEnabled = true` in `[session_settings.worker]` or `[session_settings.reviewer]`. The lead keeps Claude Code's default.

## Per-repo config

Per-repo keys come from `.sfy/` folders.

- `<repo folder>/.sfy/config.toml` holds the settings of one repo. You can keep `.sfy/` out of git with `core.excludesFile`, or commit it. sfy reads it only from the repo folder, never from a task's worktree or branch, so a worker cannot change its own settings.
- A `.sfy/config.toml` in a parent folder of the repo covers every repo below it. sfy stops before the first folder on the walk of the factory's working folder. The `.sfy/` of a folder on the walk is a layer of the factory instead, as the section Override a factory from a folder describes, and its `no_merge` and `draft_pr` reach every repo of the factory.
- The old `.sfy.toml` file still works in the repo folder and in a parent folder up to `$HOME`. `sfy config` lists each one on the walk and warns until you move it to `.sfy/config.toml` in the same folder.

For a task whose repo is a URL, the repo folder is the clone in `repos/` of the factory folder. A committed `.sfy/` arrives with each fetch of the default branch. You can also put an untracked one there by hand.

Key by key, the first file that sets a key wins. The order is the `.sfy/config.toml` files from the nearest up, then each `.sfy.toml` from the nearest up, then the factory's layers. A file that does not parse counts as an error, and the repo then counts as no-merge. So does a per-repo `.sfy/` folder that you do not own or that others can write, and the error names the folder. sfy fails there instead of skipping the folder, because a skipped `no_merge = true` would let the lead merge. SPEC.md describes the lookup in full.

The keys are `no_merge` and `draft_pr`. With `draft_pr = true`, the note that asks the worker to push also says that any PR must be a draft, with `gh pr create --draft`. sfy opens no PR itself. The default is `false`.

With `no_merge = true`, `sfy task integrate` refuses, and the approval note tells the lead to leave the branch for you. You merge it by hand, then the lead runs `sfy task complete`. One file covers every work repo below its folder:

```toml
# ~/Developer/work/.sfy/config.toml
no_merge = true
```

A key that sfy does not know, and a global-only key such as `max_workers` or `multiplexer`, is an error that names the file. In a no-merge repo, a hook in the lead's settings also blocks a hand `git push`, `git merge`, `git rebase` and `gh pr merge`. Other repos get no extra rule. To let the lead run `gh pr merge` where it may merge, add `allow = ["Bash(gh pr merge *)"]` to `[session_rules.lead]`. The hook does not follow a subshell, a script, `eval`, `bash -c`, an alias or `gh --repo`. SPEC.md lists what it misses.

## Hold the push of a task

By default a worker on a repo with a remote pushes its branch after a review passes. To keep one task off the remote until you approve, add a `## Push` section to its task file:

```markdown
## Push

hold
```

The value `hold` is the only one. Any other value is a task file error. Other tasks of the same repo push as usual.

- A held task's worker has no push allow rule, and its settings deny every `git push`. The hold does not rely on the prompt.
- When the review passes, the task moves to the state `held`. sfy ends the sessions, keeps the workspace, and tells the lead the branch name, the repo folder, and that the push waits for you.
- Tell the lead to push. It runs `sfy task release <id>`, or its old name `sfy task push <id>`, and sfy starts a worker that pushes the branch. The task then moves to `approved`, and integrate or no-merge works as usual.
- `sfy task feedback <id> <text>` on a held task keeps the hold. Feedback after the release also restores it.
- A repo with no remote has nothing to push, so the hold changes nothing there.
- The hold works with a `## Branch` section too. The released worker then pushes to the existing branch with its lease.

## Session rules

A `[session_rules.<kind>]` table adds rules to the settings file of one kind of session. The kinds are `lead`, `worker`, `reviewer` and the name of each `claude` agent of the task process. Its `allow` list adds permission rules. Its `excluded_commands` list adds command patterns that the session runs outside the sandbox. sfy adds no rule for any tool beyond git by itself.

For example, a setup can let workers open a pull request and let the lead merge it. Put the rules in the config and the instructions in two prompt additions:

```toml
[session_rules.worker]
allow = ["Bash(<tool> pr create *)"]
excluded_commands = ["<tool> *"]
```

The worker instructions go in `prompts/worker-repo.d/pr.md`. They tell the worker to open the pull request after `git push`. The lead instructions go in `prompts/lead.d/pr.md`. They tell it to merge the pull request instead of running `sfy task integrate`. Replace `<tool>` with the command that talks to your hosting service. The lead's settings deny `gh pr merge`, so the tool must be another command, or you merge the pull request yourself.

## Note delivery

sfy delivers notes to a Claude Code session through a hook on the session's note file. Claude Code runs that hook between tool calls or at the end of a turn, so a long tool call delays a note. sfy types the note into the pane only when the session waits for input and the hook left the text unread. It never types into a busy pane.

`note_fallback_after` in `~/.sfy/config.toml` sets how long sfy waits for the hook before it types. The value is a Go duration, such as `90s` or `10m`. The default is `5m`. It covers a long tool call and a slow hook, so a note does not land in text that you are typing. A turn end starts the wait again. Lower the value only when your sessions have no working note hook and a late note costs you more than a typed one.

```toml
note_fallback_after = "10m"
```

## Wake a closed session

A worker or a reviewer session can end before its task does, for example after `sfy down` or a crash. When a message arrives for such a session, sfy resumes its Claude conversation in a new terminal session. The woken session gets the same settings as at its first start and a new token. Its first message tells it to run `sfy inbox`.

sfy wakes a worker only while its task is in `working`, `review` or `feedback` at that worker. It wakes a reviewer only while the task waits for that reviewer's verdict. A session of a finished, cancelled or escalated task never wakes. A wake that fails, or one that the cap stops, sends a message to the lead. The message stays unread in the session's inbox.

The `[wake]` table in `~/.sfy/config.toml` holds the settings. These are the defaults:

```toml
[wake]
lead = false
worker = true
reviewer = true
max_per_hour = 3
```

Set `worker` or `reviewer` to `false` to turn waking off for that kind. Then a `sfy send` to a gone session fails, as before. Set `lead` to `true` to wake the lead too. A woken lead keeps its permission dialogs, so sfy runs the `notify` template to make you look at it. A registered lead never wakes. `max_per_hour` caps the wakes of one session in the last hour, failed ones included. It must be 1 or more. A per-repo `.sfy.toml` file refuses the table.

## Notes about queued tasks

A task in `working`, `review` or `feedback` holds its worker slot, also while its worker waits for an answer or for a push. When a queued task has waited `queue_wait_note` for a slot, sfy tells the lead once. The note names each active task whose worker reported blocked or that waits for a push, with how long it has waited. sfy sends no note while every slot holds running work.

`queue_wait_note` in `~/.sfy/config.toml` sets the wait. The value is a Go duration. The default is `10m`, and `0` turns the note off. A per-repo `.sfy.toml` file refuses the key.

```toml
queue_wait_note = "30m"
```

## Run the task's check before review

A task may have a `## Check` section with one command line. When the worker reports done, sfy runs that command in the task's workspace. A pass starts the review. A failure goes back to the worker as a message, with the command, the error and the last 8 KiB of the output, and no reviewer starts. A failed check counts as a review round.

The command runs in a sandbox with the worker's `allow_write` and `deny_read` lists. It uses `bwrap` on Linux and `sandbox-exec` on macOS, and it leaves the network open. A check that needs another write path, such as a build cache, needs that path in `allow_write`.

`check_timeout` in `~/.sfy/config.toml` sets how long a check may run. The value is a Go duration. The default is `20m`. It is a global setting, so a per-repo file refuses it.

```toml
check_timeout = "40m"
```

## Add a step to the task process

Every task runs through a process of agents and lists. The default runs the worker, the check and the reviewer in the list `build`, then pushes and hands the branch to the lead. `sfy config` prints the whole process, and SPEC.md, section The task process, describes each key.

To run a script of the factory before the check, add a shell agent and put it in `build`. Put the script in the factory folder, so the worker cannot edit it:

```toml
# ~/.sfy/factories/acme/config.d/50-gate.toml
[agents.gate]
harness = "shell"
run = ["sh {factory}/checks/gate.sh {task_file}"]
show = "{from} (gate runs)"

[lists.build]
agents = ["worker", "gate", "check", "reviewer"]

[lists.build.then]
fail.gate = { list = "build", round = true }
```

The gate runs in the check sandbox after `sfy report done`. When it exits with a code other than 0, its output goes to the worker, and the fail counts as a round. `status.md` shows `working (gate runs)` while it runs. The other keys of `lists.build.then` keep their default, because the files merge key by key. A later file's `agents` list replaces the list as a whole.

A shell agent can write a note for the lead into the folder that `{drop}` names, in the format of a drop file. The lead gets it from `gate@<task id>`. Each agent's result lands in `tasks/<id>/results/`, and `{result}` names the file of the result before.

## Define your own agents, lists and branches

A factory may define agents and lists of any name. A name matches `[a-z][a-z0-9-]*` and is neither `lead` nor `main`. A `claude` agent reads its prompt from `prompts/<prompt>.md`, which defaults to the agent's name, and its model from `model` or `models.<name>`. Its session is named `<session_name>-<id>`, or `<session_name>-<id>-<round>` for `session = "round"`.

A task picks its start list with a `## List` section. This list drafts text and lands without a push:

```toml
[lists.draft]
agents = ["build"]

[lists.draft.then]
fail.check = { list = "draft", round = true }
fail.reviewer = "draft"
pass = "land"
```

A shell agent may pick the next list. sfy runs it after the list passes, and the last line of its output names the target:

```toml
[agents.route]
harness = "shell"
run = ["sh {factory}/checks/route.sh {task_file}"]

[lists.load.then]
pass = { agent = "route", to = ["production", "other"] }
```

An `operator` agent may stand anywhere in a list. The task then waits in `held` until the lead runs `sfy task release <id>` on your word. `sfy task push <id>` is the old name of that command.

A process change reaches a running task at its next agent. `sfy restart` refuses a change that removes the position of an open task. `sfy up`, `sfy restart` and `sfy config` also refuse the few keys that the engine cannot run yet, with an error that names the key. SPEC.md, section The task process, lists every key and rule.

## Terminal tools

sfy runs each agent in a multiplexer. It ships a set for tmux. Any other terminal tool plugs in through a template set under `[multiplexers.<name>]` in `~/.sfy/config.toml`. `docs/herdr.md` and `docs/rex.md` hold example sets. SPEC.md lists every template.

The optional `escape` template presses the Escape key in session `{name}`, and nothing else. `sfy unstick` and `sfy lead interrupt` run it. The tmux set has one. A set without it makes both commands fail with an error that names the missing line.

The `show_lead` template makes the lead's session visible. sfy runs `show` for workers and reviewers. An empty `show` hides them. Without `show_lead`, the lead uses `show`. When sfy starts the lead in place, it runs `show_lead` after it records the address and before it hands the terminal to the lead, so a template set can arrange the terminal around it, for example to open side panes. A failed `show_lead` prints a warning and the lead still starts.

Every template can use `{home}`, the absolute path of the factory folder of the running factory. A `show_lead` template can use it to open `notes.md` and `status.md` of the right factory. sfy also sets `SFY_HOME` to that folder in the environment of every template command, so a script that reads `SFY_HOME` finds the right factory, also when `sfy up` picks the factory from the folder rules.

The `whoami` template prints the address of the terminal that it runs in. With it, `sfy` and `sfy up` start the lead in the terminal where you run them, and do not open a new session. The tmux set has a `whoami` template. A set without one starts the lead through the `start` template. If `whoami` fails or prints nothing, sfy does the same.

In that case sfy records the address in `sessions/lead/address` in the factory folder. It also records the pid and the start time of the lead's process in `sessions/lead/pid`. It then uses the address as `{name}` for the lead in the `send`, `notify`, `alive`, `stop`, `show` and `show_lead` templates. When `alive` reports the address gone, or the lead's process has ended, sfy removes the record, so the next `sfy` runs the lead in place again. Your shell stays in the terminal after the lead exits, so sfy checks the process and not only the terminal. `sfy down` leaves the lead and its record alone.

A lead that you made with `sfy register` has no process record, so sfy asks only its terminal. When that agent exits and its terminal stays, sfy still counts the lead as running. `sfy down` keeps the registration.

The `alive` template decides alone for workers, reviewers and a lead that started through `start`. Exit 0 means the agent's command still runs, not only its terminal. A set that keeps a pane after its command exits, such as tmux with `remain-on-exit on`, needs an `alive` template that checks the command.
