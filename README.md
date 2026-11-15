# simple factory

sfy runs coding agents as interactive sessions in terminal panes. A main agent hands out tasks, and a fresh reviewer session checks each result.

SPEC.md holds the requirements.

## Install a new build

Run `make install` to build sfy and install it with `go install ./cmd/sfy`. The running sfy process and the main agent keep the old code until you restart them. Run `sfy restart` from a terminal outside the agent sessions. It stops the sfy process and the main agent, then starts both from the installed binary. Workers and reviewers keep running, and their tasks stay on disk. `sfy restart` does not build or install sfy.
