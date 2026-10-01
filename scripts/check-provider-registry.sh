#!/usr/bin/env sh
set -eu
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
# Compare in memory: checking generated assets must never rewrite the worktree.
(cd "$ROOT" && go run ./cmd/providerregistry -check)
