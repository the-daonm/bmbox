# Contributing to bmbox

Thanks for helping. Bug reports, fixes, features and docs are all welcome.

## Before you start

- For a bug, open an issue with `bmbox --version`, `sudo bmbox doctor`
  output, the topology (without passwords) and what you expected.
- For a larger change, open an issue first so we can agree on the approach.

## Development setup

You need Go (see `go.mod`) and, for the end-to-end test, a Linux host with
KVM, libvirt and sudo:

```bash
make build && sudo bin/bmbox setup   # once per host
make lint test                       # before every pull request
make e2e                             # when you touch anything that runs on the host
```

The end-to-end test creates and destroys its own lab, and fails if it
changed anything else on the host. It is safe on a host with other VMs.

## Pull requests

- One purpose per commit: a fix, a feature, a refactor, docs. Split
  unrelated changes into separate commits.
- Commit messages: `area: what changed` on the first line (areas are
  package names, `cli`, `e2e`, `docs`, `ci`...), then why, wrapped at 72
  columns.
- Add or update unit tests with the code. Anything that changes what
  happens on the host also needs an end-to-end check in `scripts/e2e.sh`.
- Keep the user-facing behaviour documented in the README.
- `gofmt`, `go vet` and `staticcheck` must pass (`make lint`).

## Design rules

- `up` converges and can be run again at any time. It must never wipe a
  disk or NVRAM that exists.
- bmbox only removes what it can prove it created: resources carry an owner
  tag (bridge alias, domain metadata, unit description, rule comment).
- Check everything that can fail before changing the host, so a bad
  topology leaves nothing half-created.
- Secrets are written readable by root only, and never logged.

By contributing you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
