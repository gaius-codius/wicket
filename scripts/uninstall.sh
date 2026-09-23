#!/usr/bin/env bash
# Remove the wicket binary. Leaves config, state, and keyring alone.
set -euo pipefail

BINDIR="${WICKET_BINDIR:-${HOME}/.local/bin}"
TARGET="$BINDIR/wicket"

usage() {
  cat <<'USAGE'
Usage: scripts/uninstall.sh

Removes $WICKET_BINDIR/wicket (default ~/.local/bin/wicket).
Does not delete config, state, or keyring entries. To remove those yourself:

  config:  ~/.config/wicket/   (or $WICKET_CONFIG)
  state:   ~/.local/state/wicket/
  keyring: Secret Service entries labelled for wicket (via your keyring UI)
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ ! -e "$TARGET" ]]; then
  echo "nothing to remove at $TARGET"
  exit 0
fi

rm -f "$TARGET"
echo "removed $TARGET"
echo "config, state, and keyring entries were left in place"
