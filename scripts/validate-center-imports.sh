#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

paths=(
  cmd/control-plane
  cmd/platform-rpc
  internal/control
  internal/rpc
)
pattern='github\.com/nciyuan9264/game-platform/internal/(acquire|davinci|splendor|yinsh|othello|quoridor|loveletter|onitama|plugins)'

if rg -n \
  --glob='*.go' \
  --glob='!*_test.go' \
  "$pattern" \
  "${paths[@]}"; then
  printf 'center service imports a game implementation package\n' >&2
  exit 1
fi

printf 'center import boundaries passed validation\n'
