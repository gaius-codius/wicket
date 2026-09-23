# Contributing

Bug reports and patches are welcome. Wicket is small and opinionated, so if
you are planning something substantial, open an issue first, it is cheaper
than finding out after the fact that it does not fit.

## What you need

- Go 1.24 or newer.
- Linux. Wicket compiles for macOS and the BSDs but has no keyring backend
  there; see [#1](https://github.com/gaius-codius/wicket/issues/1).
- `dbus-daemon` on `PATH`. The `internal/secret` tests start a private session
  bus and run an in-process Secret Service on it.
- A FreeRDP 3 SDL client only if you want to run Wicket against a real host.
  The tests never do: they use a stub client.

## Before you push

```
gofmt -l .        # must print nothing
go vet ./...
go test ./...
go test -race ./internal/secret
```

## Tests

Tests must not touch your real config, state or keyring. Every package calls
`testutil.Sandbox` from its `TestMain`, which redirects `WICKET_CONFIG`,
`WICKET_STATE`, the XDG directories and `DBUS_SESSION_BUS_ADDRESS` somewhere
harmless. Do not work around it.

A test that would be satisfied by the bug it is meant to catch is worth less
than no test. Where a fix is subtle, check that the new test fails when the
fix is reverted.

## Style

- Match the surrounding code. It favours small functions, few dependencies and
  comments that say *why* rather than *what*.
- Comment the non-obvious. A comment explaining which bug a line prevents is
  worth more than one restating the line.
- Keep the layers apart: `internal/tui` holds the Bubble Tea model, `internal/
  tui/actions.go` the use-case layer beneath it, and `internal/config`,
  `internal/rdp`, `internal/secret` and `internal/theme` know nothing about the
  TUI. `cmd/wicket` wires them together.

## Releasing

Push a `vX.Y.Z` tag from `main`. The `release` workflow runs the tests, builds
static linux/amd64 and linux/arm64 binaries, and publishes them with
`SHA256SUMS` as a GitHub release, which is what `install.sh` downloads.

```
git tag -a v0.1.0 -m v0.1.0 && git push origin v0.1.0
```

## Commits

One change per commit, and a message that explains the problem rather than
restating the diff. The subject is `package: what changed`, lower case, no
trailing period:

```
tui: keep a text input inside its column
```

The body should say what was wrong and why the fix is the right shape. Look at
`git log` for the register.

Please do not add co-author, "generated with" or other tool attribution
trailers to commits or pull requests.
