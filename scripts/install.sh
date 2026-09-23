#!/usr/bin/env bash
# Install wicket to ~/.local/bin (or $WICKET_BINDIR).
set -euo pipefail

BINDIR="${WICKET_BINDIR:-${HOME}/.local/bin}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MODULE="github.com/gaius-codius/wicket/cmd/wicket"

usage() {
  cat <<'USAGE'
Usage: scripts/install.sh [--from-source|--from-module]

Installs the wicket binary into ~/.local/bin (override with WICKET_BINDIR).

  --from-source   build ./cmd/wicket from this clone (default when run from a clone)
  --from-module   go install github.com/gaius-codius/wicket/cmd/wicket@latest

Also checks that a FreeRDP 3 client is on PATH (sdl-freerdp3 or xfreerdp3).
Does not touch config, state, or the keyring.
USAGE
}

mode=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --from-source) mode=source; shift ;;
    --from-module) mode=module; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ -z "$mode" ]]; then
  if [[ -f "$REPO_ROOT/go.mod" ]] && grep -q 'module github.com/gaius-codius/wicket' "$REPO_ROOT/go.mod" 2>/dev/null; then
    mode=source
  else
    mode=module
  fi
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go is required on PATH" >&2
  exit 1
fi

mkdir -p "$BINDIR"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "building wicket ($mode)…"
if [[ "$mode" == source ]]; then
  (cd "$REPO_ROOT" && go build -o "$tmp/wicket" ./cmd/wicket)
else
  GOBIN="$tmp" go install "${MODULE}@latest"
fi

install -m 755 "$tmp/wicket" "$BINDIR/wicket"
echo "installed $BINDIR/wicket"
"$BINDIR/wicket" --version || true

case ":$PATH:" in
  *":$BINDIR:"*) ;;
  *)
    echo "note: $BINDIR is not on PATH; add it, e.g.:"
    echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
    ;;
esac

found=0
for c in sdl-freerdp3 xfreerdp3; do
  if command -v "$c" >/dev/null 2>&1; then
    echo "found FreeRDP client: $c"
    found=1
  fi
done
if [[ "$found" -eq 0 ]]; then
  echo "warning: no FreeRDP 3 client on PATH (need sdl-freerdp3 or xfreerdp3)" >&2
fi
