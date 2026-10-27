#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT/scripts/changed-release-components.sh"
files="$(mktemp)"
trap 'rm -f "$files"' EXIT

assert_case() {
  local expected="$1"
  shift
  printf '%s\n' "$@" >"$files"
  actual="$(
    CHANGED_FILES_FILE="$files" "$SCRIPT" base head |
      jq -cS .
  )"
  expected="$(jq -cS . <<<"$expected")"
  if [[ "$actual" != "$expected" ]]; then
    printf 'changed component mismatch\nexpected: %s\nactual:   %s\n' \
      "$expected" "$actual" >&2
    exit 1
  fi
}

assert_case \
  '{"center":[],"games":["acquire"],"productionChange":true,"docsOnly":false}' \
  internal/plugins/acquire/rules/step1.go \
  internal/plugins/acquire/rules/step1_test.go

assert_case \
  '{"center":[],"games":["acquire","davinci","fugitive","onitama","othello","quoridor","splendor","warchest","yinsh"],"productionChange":true,"docsOnly":false}' \
  internal/roomhost/host.go

assert_case \
  '{"center":["platform-rpc"],"games":["acquire","davinci","fugitive","onitama","othello","quoridor","splendor","warchest","yinsh"],"productionChange":true,"docsOnly":false}' \
  internal/history/replay_codec.go

assert_case \
  '{"center":[],"games":[],"productionChange":false,"docsOnly":true}' \
  README.md \
  docs/game-plugin-guide.md

assert_case \
  '{"center":["control-plane","gateway","migrate-schema","platform-rpc"],"games":["acquire","davinci","fugitive","onitama","othello","quoridor","splendor","warchest","yinsh"],"productionChange":true,"docsOnly":false}' \
  internal/contracts/types.go

assert_case \
  '{"center":["control-plane","gateway","migrate-schema","platform-rpc"],"games":["acquire","davinci","fugitive","onitama","othello","quoridor","splendor","warchest","yinsh"],"productionChange":true,"docsOnly":false}' \
  docker-compose.prod.yml \
  .github/workflows/deploy.yml

assert_case \
  '{"center":[],"games":["yinsh"],"productionChange":true,"docsOnly":false}' \
  internal/plugins/yinsh/rules.go \
  internal/plugins/yinsh/rules_test.go

assert_case \
  '{"center":[],"games":["othello"],"productionChange":true,"docsOnly":false}' \
  internal/plugins/othello/rules.go \
  internal/plugins/othello/rules_test.go

assert_case \
  '{"center":[],"games":["fugitive"],"productionChange":true,"docsOnly":false}' \
  internal/plugins/fugitive/rules.go \
  internal/plugins/fugitive/rules_test.go

assert_case \
  '{"center":[],"games":["warchest"],"productionChange":true,"docsOnly":false}' \
  internal/plugins/warchest/rules.go \
  internal/plugins/warchest/rules_test.go

assert_case \
  '{"center":[],"games":["quoridor"],"productionChange":true,"docsOnly":false}' \
  internal/plugins/quoridor/rules.go \
  internal/plugins/quoridor/rules_test.go

assert_case \
  '{"center":[],"games":["onitama"],"productionChange":true,"docsOnly":false}' \
  internal/plugins/onitama/rules.go \
  internal/plugins/onitama/rules_test.go

printf 'changed release component tests passed\n'
