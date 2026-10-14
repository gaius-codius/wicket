# Agent instructions

Read [CONTRIBUTING.md](CONTRIBUTING.md) for the toolchain, the checks to run
before pushing, test rules, code style, and commit conventions. Those rules
apply to agents too, including: no co-author, "generated with" or other tool
attribution in commits or pull requests.

## What to work on

Work is tracked in GitHub issues on the [project board](https://github.com/users/gaius-codius/projects/3).

- **Epics** are parent issues labelled `type:epic`. Work items are native sub-issues of an epic.
- **Types** are labels, exactly one per issue: `type:feature`, `type:research`, `type:bug`, `type:chore`.
- **Order** comes from native "blocked by" dependencies. Repo milestones group phases inside an epic.
- **Status and priority** are fields on the board. Put every new issue on the board.
- **Pull requests** say `Closes #N` so the issue closes and the board updates on merge.

Before starting an issue, check that nothing still blocks it. When you find a
bug or chore while working, file it under the same epic with its own type
label rather than widening the current change.

```sh
R=repos/gaius-codius/wicket
gh issue list --label type:epic --json number,title,state                    # open epics
gh api $R/issues/EPIC/sub_issues --jq '.[] | "#\(.number) \(.state) \(.title)"'  # what's left in an epic
gh api $R/issues/N/dependencies/blocked_by --jq '.[].number'                 # what blocks N
gh project item-list 3 --owner gaius-codius --format json                    # board status and priority
```

## Go style

Lint must pass before you hand work back. Follow the `go-style` skill (gaius-codius/skills), which covers [Effective Go](https://go.dev/doc/effective_go), [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments), the [Google Go style guide](https://google.github.io/styleguide/go/) and these house rules:

- Wrap errors with `%w` and add context; compare with `errors.Is`/`errors.As`.
- `context.Context` is the first parameter and is never stored in a struct.
- Suppress a finding only with `//nolint:<linter> // reason`.

Claude Code formats each `.go` file you edit and lints its package through a user-level hook, and reports findings back in the same session. Fix them before moving on.

## Before opening a PR

Run all of these and make sure they pass (in a mise-activated shell, or prefix each with `mise exec --`):

```sh
go test ./...
go test -race ./...
go vet ./...
golangci-lint run ./...
./scripts/govulncheck.sh
go build ./cmd/wicket/
```

## Reviews and worktrees

- **Reviews:** with `project-review`, never `team-pr-review`, which `.claude/settings.json` denies here. Its facts and lenses for this project are in `~/code/gaius-codius-skills/projects/wicket/`. Each review writes its record to `~/code/wicket/working-docs/reviews/`.
- **Worktrees:** make them under `~/code/wicket-worktrees/` with `git worktree add` from `origin/main`, then copy the untracked settings in: `mkdir -p <worktree>/.claude && cp ~/code/wicket/.claude/settings.json <worktree>/.claude/`.
