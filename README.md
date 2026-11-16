# simple factory

sfy runs coding agents as interactive sessions in terminal panes. A main agent hands out tasks, and a fresh reviewer session checks each result.

SPEC.md holds the requirements.

## Install with Homebrew

Install sfy from the tap with `brew install jacobjnilsson/tap/sfy`. Upgrade it with `brew upgrade sfy`. Run `sfy --version` to see the installed version. A build from source prints `dev`.

Each pushed tag `v*` triggers a release. GoReleaser builds sfy for macOS and Linux on amd64 and arm64, publishes a GitHub release, and updates the cask in `JacobJNilsson/homebrew-tap`. The workflow needs the repository secret `HOMEBREW_TAP_GITHUB_TOKEN`, a token with write access to the tap.

## Install a new build

Run `make install` to build sfy and install it with `go install ./cmd/sfy`. The running sfy process keeps the old code until you restart it. Run `sfy restart` from a terminal outside the agent sessions, or ask the main agent to run it. It stops the sfy process and starts it from the installed binary. The main agent, workers and reviewers keep running, and their tasks stay on disk. The main agent gets a note, which says when its prompt changed. `sfy restart` does not build or install sfy.

## Terminal tools

sfy runs each agent in a multiplexer. It ships a set for tmux. Any other terminal tool plugs in through a template set under `[multiplexers.<name>]` in `~/.sfy/config.toml`.

The `show_main` template makes the main agent's session visible. sfy runs `show` for workers and reviewers. An empty `show` hides them. Without `show_main`, the main agent uses `show`.

`docs/herdr.md` holds an example set for Herdr. See SPEC.md for every template.
