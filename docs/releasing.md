# Releasing sfy

Each pushed tag `v*` triggers a release. GoReleaser builds sfy for macOS and Linux on amd64 and arm64, publishes a GitHub release, and updates the cask in `JacobJNilsson/homebrew-tap`.

The workflow needs the repository secret `HOMEBREW_TAP_GITHUB_TOKEN`, a token with write access to the tap.

## Install and upgrade

On Linux, install `bubblewrap` and `socat` before the first task, and on Ubuntu 24.04 and later add the AppArmor profile for bwrap. The README section "Linux sandbox" lists the commands.

- Install from the tap with `brew install jacobjnilsson/tap/sfy`. Upgrade with `brew upgrade sfy`.
- On Linux, download the tarball for amd64 or arm64 from GitHub Releases. Upgrade with `sfy restart --update`, which reads `checksums.txt` from the release. Keep the archive names `sfy_<version>_<os>_<arch>.tar.gz`, because that command builds them.
- From source, run `make install`. It builds sfy and runs `go install ./cmd/sfy`.

Run `sfy --version` to see the installed version. A build from source prints `dev`.

A new build does not change the running sfy process. See "Restart" in `docs/configuration.md`.
