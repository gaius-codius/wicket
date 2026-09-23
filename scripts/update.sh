#!/usr/bin/env bash
# Reinstall wicket (same options as install.sh).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
exec "$ROOT/install.sh" "$@"
