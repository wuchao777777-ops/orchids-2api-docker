#!/usr/bin/env sh
set -eu

# The generated minified asset is committed because the embedded admin UI is
# built without requiring Node.js. Regenerate it whenever grok-tools.js changes.

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
SRC="$ROOT/web/static/js/grok-tools.js"
OUT="$ROOT/web/static/js/grok-tools.min.js"

npx --yes terser "$SRC" \
  --compress passes=2,drop_console=false \
  --mangle \
  --output "$OUT"

printf 'minified %s -> %s\n' "$SRC" "$OUT"
