#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
SRC="$ROOT/web/static/js/grok-tools.js"
OUT="$ROOT/web/static/js/grok-tools.min.js"

npx --yes terser "$SRC" \
  --compress passes=2,drop_console=false \
  --mangle \
  --output "$OUT"

printf 'minified %s -> %s\n' "$SRC" "$OUT"
