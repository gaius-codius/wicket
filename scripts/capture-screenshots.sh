#!/usr/bin/env bash
# Capture a demo TUI screenshot for the README. Uses a throwaway config only.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-$ROOT/docs/screenshots}"
mkdir -p "$OUT"

DEMO="$(mktemp -d)"
cleanup() {
  if [[ -n "${WICKET_PID:-}" ]]; then kill "$WICKET_PID" 2>/dev/null || true; fi
  if [[ -n "${GHOSTTY_PID:-}" ]]; then kill "$GHOSTTY_PID" 2>/dev/null || true; fi
  rm -rf "$DEMO"
}
trap cleanup EXIT

cat > "$DEMO/config.toml" <<'TOML'
[ui]
theme = "wicket-dark"

[[profiles]]
name = "lab"
host = "rdp.example.com"
user = "demo"
client = "sdl-freerdp3"

[[profiles]]
name = "staging"
host = "bastion.example.net"
user = "operator"
domain = "EXAMPLE"
client = "xfreerdp3"

[[profiles]]
name = "home-lab"
host = "desk.example"
user = "me"
client = "sdl-freerdp3"
TOML

cat > "$DEMO/state.toml" <<'TOML'
[last_used]
lab = 2026-09-20T10:00:00Z
staging = 2026-09-18T08:30:00Z
TOML

BIN="$DEMO/wicket"
(cd "$ROOT" && go build -o "$BIN" ./cmd/wicket)

ghostty \
  --title=wicket-demo \
  --window-width=100 \
  --window-height=32 \
  -e env \
    WICKET_CONFIG="$DEMO/config.toml" \
    WICKET_STATE="$DEMO/state.toml" \
    WICKET_THEME=wicket-dark \
    "$BIN" &
GHOSTTY_PID=$!

addr=""
for _ in $(seq 1 50); do
  addr="$(hyprctl clients -j | python3 -c '
import json,sys
for c in json.load(sys.stdin):
    if c.get("title")=="wicket-demo" or c.get("initialTitle")=="wicket-demo":
        print(c["address"]); break
' || true)"
  [[ -n "$addr" ]] && break
  sleep 0.2
done
[[ -n "$addr" ]] || { echo "timed out waiting for wicket-demo window" >&2; exit 1; }

sleep 0.8
geom="$(hyprctl clients -j | python3 -c '
import json,sys
for c in json.load(sys.stdin):
    if c.get("address")==sys.argv[1]:
        x,y=c["at"]; w,h=c["size"]
        print(f"{x},{y} {w}x{h}"); break
' "$addr")"

grim -g "$geom" "$OUT/list-raw.png"
magick "$OUT/list-raw.png" -resize 1400x "$OUT/list.png"
rm -f "$OUT/list-raw.png"
echo "wrote $OUT/list.png ($geom)"
