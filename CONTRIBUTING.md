# Contributing

Bug reports and patches are welcome. Wicket is small and opinionated, so if
you are planning something substantial, open an issue first, it is cheaper
than finding out after the fact that it does not fit.

## Issue tracking

Work is tracked in GitHub issues on the
[project board](https://github.com/users/gaius-codius/projects/3), which holds
every open issue with a Status (Todo, In Progress, Done) and a Priority (High,
Medium, Low).

Every issue has exactly one type label:

| Label | Use it for |
|-------|------------|
| `type:epic` | A body of work; its native sub-issues deliver it |
| `type:feature` | Changes what Wicket does for a user |
| `type:research` | Answers a question; the output is a note, not code |
| `type:bug` | Incorrect behavior |
| `type:chore` | No behavior change: upgrades, refactors, CI, tooling |

Order between issues is a native "blocked by" link, not "depends on" prose.
Repo milestones group phases inside an epic and are prefixed with its area
(`settings: Toggles and choices`). Titles start with the area (`import: …`),
and epics with `epic: `. Pull requests say `Closes #N`.

New issues: start from a template, link the issue under its epic, add any
blocked-by links, then put it on the board. The commands are in
[AGENTS.md](AGENTS.md).

## What you need

- [mise](https://mise.jdx.dev). `mise.toml` pins Go (1.27.2, matching the
  `toolchain` line in `go.mod`), golangci-lint, govulncheck, deadcode, gopls, shellcheck and lefthook. Run
  `mise install` after cloning and whenever `mise.toml` changes. The shared
  git hooks are installed locally, once per clone and worktree, into
  `lefthook-local.yml`, which git ignores: on commit they format and lint the
  staged packages, on push they run the tests. `lefthook.yml` holds none, so
  without them no hooks run; `lefthook dump` shows what is loaded. Put hooks
  of your own in `lefthook-local.yml` too. Building needs Go 1.26.9 or newer;
  the pins are what CI runs.
- Linux or macOS. Linux uses the Secret Service keyring; macOS uses Keychain
  (cgo). The BSDs still compile against the D-Bus store but have no session
  bus integration tests.
- On Linux, `dbus-daemon` on `PATH`. The `internal/secret` tests start a
  private session bus and run an in-process Secret Service on it.
- On macOS, set `WICKET_KEYCHAIN_TEST=1` to exercise the Keychain backend
  against a throwaway keychain (never the login keychain).
- A FreeRDP 3 SDL client only if you want to run Wicket against a real host.
  The tests never do: they use a stub client.

## Before you push

In a mise-activated shell, or with each line prefixed by `mise exec --`:

```
go test ./...
go test -race ./...
go vet ./...
golangci-lint run ./...      # formatting (gofumpt, goimports) included
./scripts/govulncheck.sh
go build ./cmd/wicket/
```

CI runs the same on Linux through the shared
[go-ci](https://github.com/gaius-codius/go-ci) workflow, plus short runs
of `FuzzSave` and the importer's `FuzzParseRDP` and `FuzzParseRemmina`, and
a `go mod tidy` check, shellcheck on `install.sh`, a deadcode report (warnings only), and lint and the tests with the Keychain backend on macOS.
`.golangci.yml` is go-ci's baseline with wicket's exclusions, each with its
reason; suppress a single finding only with `//nolint:<linter> // reason`. A
reachable vulnerability fails `govulncheck.sh` unless
`scripts/govulncheck-allow.txt` lists it with a reason and the issue that
removes it.

## Tests

Tests must not touch your real config, state or keyring. Every package calls
`testutil.Sandbox` from its `TestMain`, which redirects `WICKET_CONFIG`,
`WICKET_STATE`, the XDG directories and `DBUS_SESSION_BUS_ADDRESS` somewhere
harmless. Do not work around it.

`install_test.go` runs `install.sh` against a fake release on localhost with
each of curl, GNU wget, busybox and wget2, skipping any that is not
installed. `WICKET_INSTALL_TESTS=all` makes a missing one fail instead, as
CI's `install-sh` job does.

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
linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 binaries, and
publishes them with `SHA256SUMS` as a GitHub release, which is what
`install.sh` downloads. Linux builds are static (`CGO_ENABLED=0`); darwin
builds use cgo against Security.framework.

Any other tag, such as `v0.4.0-rc.1`, is published the same way but as a
prerelease. GitHub never marks a prerelease latest, so `install.sh` and
the AUR package carry on with the last plain release, and an rc is
installed only by name (`--version v0.4.0-rc.1`).

The workflow also attests each tarball, and `install.sh` refuses a release
after v0.3.1 that has no attestation when `gh` is available. A tag runs the
`release.yml` at its own commit, so tag only a commit that has the attest
step. Never add a new release to `UNATTESTED` in `install.sh`.

```
git tag -a v0.1.0 -m v0.1.0 && git push origin v0.1.0
```

### Release notes

There is no CHANGELOG. The workflow publishes the release with GitHub's
generated list of merged PRs. Once it is up, write a short summary of what
changed for users since the last tag, put it above that list, and replace the
notes:

```
gh release view vX.Y.Z --json body --jq .body > notes.md   # add the summary at the top
gh release edit vX.Y.Z --notes-file notes.md
```

### After the release

When the workflow has finished, check what it published before announcing it:

1. Download the assets and check them against `SHA256SUMS` (on macOS, use
   `shasum -a 256 -c SHA256SUMS`):

   ```
   gh release download vX.Y.Z --dir rel && (cd rel && sha256sum -c SHA256SUMS)
   ```

2. Verify the attestation of each tarball:

   ```
   for t in rel/*.tar.gz; do
     GH_HOST=github.com gh attestation verify "$t" --repo gaius-codius/wicket \
       --cert-identity https://github.com/gaius-codius/wicket/.github/workflows/release.yml@refs/tags/vX.Y.Z
   done
   ```

   `install.sh` runs the same check on the tarball for the platform it
   installs, so a tarball that fails it fails closed for every install of
   that platform with gh 2.68 or later.

3. Install the release end to end into a scratch directory:

   ```
   WICKET_BINDIR="$(mktemp -d)" sh install.sh --version vX.Y.Z
   ```

   With gh 2.68 or later on `PATH` it should print `verified provenance`,
   then `installed wicket X.Y.Z`.

### AUR

`packaging/aur/wicket-bin/PKGBUILD` is the template for the `wicket-bin` AUR
package. When the `AUR_SSH_PRIVATE_KEY` secret is set, the `release` workflow
sets `pkgver`, `pkgrel=1` and the checksums from the tag and `SHA256SUMS`,
test-builds the package, and pushes `PKGBUILD` and a fresh `.SRCINFO` to the
AUR, in an `update-aur` job after the release. Without the secret the job
skips itself, as it does until the AUR package is first published (#56).
It also skips any tag that is not a plain `vX.Y.Z`. If the push fails,
re-run that job alone from the Actions page.

First-time setup:

1. Register an AUR account and add an SSH public key to it.
2. Create the package with a first push:

   ```
   git -c init.defaultBranch=master clone ssh://aur@aur.archlinux.org/wicket-bin.git
   cp packaging/aur/wicket-bin/{PKGBUILD,.SRCINFO} wicket-bin/
   cd wicket-bin && git add PKGBUILD .SRCINFO
   git commit -m "Initial import" && git push origin master
   ```

3. Add the matching private key as the `AUR_SSH_PRIVATE_KEY` repository
   secret.

Manual update, if the workflow fails: in a clone of the AUR repo, bump
`pkgver`, reset `pkgrel=1`, then

```
updpkgsums
makepkg --printsrcinfo > .SRCINFO
git commit -am "Update to vX.Y.Z" && git push
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
