#!/usr/bin/env bash
# Format the given Go files and lint their packages. Run it on staged files from
# a pre-commit hook.
#
# Lint reports every finding in the touched packages, like CI.
# Exit 1 means formatting changed a file or lint found something.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
root="$(pwd -P)"

# Paths relative to the repository root, as lefthook passes them. No realpath:
# macOS's BSD realpath has no --relative-to.
files=()
for f in "$@"; do
  f="${f#"$root"/}"
  while [[ $f == ./* ]]; do f="${f#./}"; done
  [[ $f == *.go && -f $f ]] && files+=("$f")
done
((${#files[@]})) || exit 0

run() {
  if command -v "$1" >/dev/null 2>&1; then "$@"
  elif command -v mise >/dev/null 2>&1; then mise exec -- "$@"
  else echo "$1 not found (install mise and run 'mise install')" >&2; return 127
  fi
}

# gofumpt and goimports, as configured in .golangci.yml.
run golangci-lint fmt "${files[@]}"

dirs=()
while IFS= read -r d; do dirs+=("./$d"); done < <(for f in "${files[@]}"; do dirname "$f"; done | sort -u)

run golangci-lint run "${dirs[@]}"
