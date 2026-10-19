#!/usr/bin/env bash
# Fail a commit or push when the shared Go hooks are not loaded.
#
# Git hooks are shared by every worktree of a repository, so lefthook runs in
# a new worktree too. The shared jobs, though, come from the git-ignored
# lefthook-local.yml, which a new clone or worktree does not have. Without
# it, lefthook loads only lefthook.yml and the commit goes through unchecked.
# The shared pre-commit job runs scripts/lint-packages.sh, so its absence from
# what lefthook loads means the shared hooks are missing.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

if mise exec -- lefthook dump 2>/dev/null | grep -q 'scripts/lint-packages\.sh'; then
  exit 0
fi
cat >&2 <<'MSG'
The shared Go hooks are not loaded in this clone or worktree, so nothing
formats, lints or tests this commit. Install them (see CONTRIBUTING.md):

  ~/code/robocode/tooling/install-local-hooks "$(git rev-parse --show-toplevel)" go

If you cannot install them, skip this check with
LEFTHOOK_EXCLUDE="shared hooks loaded" and run the checks in CONTRIBUTING.md
yourself before you push.
MSG
exit 1
