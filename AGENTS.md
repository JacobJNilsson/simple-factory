# Working in this repository

Read this file before the first commit.

## Start here

1. Read SPEC.md first. It holds the requirements.
2. Run `make hooks` once per repository. It sets `core.hooksPath` to the tracked `.githooks/` directory in the shared repo config, so it covers every worktree.
3. Run `make check` before every commit. The hook and CI run the same target.
4. Format Go files with gofmt. Run `gofmt -w` after every edit and never format by hand.

## The gate

`make check` is the one definition of green. It runs these steps in order:

1. A gofmt check.
2. The tests of the neutrality script.
3. The neutrality check, which fails when a file name, staged text, or a commit message names a private project.
4. `go vet ./...`.
5. `golangci-lint run ./...`.
6. The anti-slop-go analyzers.
7. `go test -race ./...`.

The anti-slop-go analyzers need Go 1.26 or newer. With the default `GOTOOLCHAIN=auto`, the go command downloads a newer toolchain.

sfy is a general tool. Describe the general need in code, docs, and commits. Never name a private project.

## Commits

- Every commit builds and passes the gate on its own.
- Commit subjects follow Conventional Commits.
- A subject describes one change and needs no "and".

## Writing rules

These rules apply to all text that stays: code comments, commit messages, PR text, and docs.

- Use active voice.
- Write at most 25 words in a sentence.
- Use no em dashes and no semicolons.
- Use no idioms. Use plain words.
- Write headings in sentence case.
- Write code comments in one sentence. Use two at most.
