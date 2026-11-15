# simple factory

sfy runs coding agents as interactive sessions in terminal panes. A main agent hands out tasks, and a fresh reviewer session checks each result.

In Herdr, workers and reviewers run in the background and open no tab. Run `hc attach <name>` or use the `hc` picker to open one. Set `show_agents = true` in `config.toml` to open a tab for each worker and reviewer.

## Run sfy on Rex

Rex is a terminal app in beta. Set `multiplexer = "rex"` in `~/.sfy/config.toml`, then run `sfy up`. sfy starts the main agent, workers and reviewers as Rex sessions, and each one opens a window in the Rex app. `show_agents` has no effect on Rex.

sfy finds `rex` on the PATH, then in `$REX_BIN_DIR`, then in the Rex app bundle. Rex allows two sessions with one label, so sfy refuses to start a session whose label exists. Run `rex kill <label>` to remove a stale one. SPEC.md has the details.

SPEC.md holds the requirements.

## Install with Homebrew

Install sfy from the tap with `brew install jacobjnilsson/tap/sfy`. Upgrade it with `brew upgrade sfy`. Run `sfy --version` to see the installed version. A build from source prints `dev`.

Each pushed tag `v*` triggers a release. GoReleaser builds sfy for macOS and Linux on amd64 and arm64, publishes a GitHub release, and updates the cask in `JacobJNilsson/homebrew-tap`. The workflow needs the repository secret `HOMEBREW_TAP_GITHUB_TOKEN`, a token with write access to the tap.

## Install a new build

Run `make install` to build sfy and install it with `go install ./cmd/sfy`. The running sfy process keeps the old code until you restart it. Run `sfy restart` from a terminal outside the agent sessions, or ask the main agent to run it. It stops the sfy process and starts it from the installed binary. The main agent, workers and reviewers keep running, and their tasks stay on disk. The main agent gets a note, which says when its prompt changed. `sfy restart` does not build or install sfy.
