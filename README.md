# simple factory

sfy runs coding agents as interactive sessions in terminal panes. A main agent hands out tasks, and a fresh reviewer session checks each result.

SPEC.md holds the requirements.

## Install a new build

Run `make install` to build sfy and install it with `go install ./cmd/sfy`. The running sfy process keeps the old code until you restart it. Run `sfy restart` from a terminal outside the agent sessions, or ask the main agent to run it. It stops the sfy process and starts it from the installed binary. The main agent, workers and reviewers keep running, and their tasks stay on disk. The main agent gets a note, which says when its prompt changed. `sfy restart` does not build or install sfy.
