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
  internal/acquire/rules/step1.go \
  internal/acquire/rules/step1_test.go

assert_case \
  '{"center":[],"games":["acquire","davinci","splendor"],"productionChange":true,"docsOnly":false}' \
  internal/roomhost/host.go

assert_case \
  '{"center":["platform-rpc"],"games":["acquire","davinci","splendor"],"productionChange":true,"docsOnly":false}' \
  internal/history/replay_codec.go

assert_case \
  '{"center":[],"games":[],"productionChange":false,"docsOnly":true}' \
  README.md \
  docs/game-plugin-guide.md

assert_case \
  '{"center":["control-plane","gateway","migrate-schema","platform-rpc"],"games":["acquire","davinci","splendor"],"productionChange":true,"docsOnly":false}' \
  internal/contracts/types.go

assert_case \
  '{"center":["control-plane","gateway","migrate-schema","platform-rpc"],"games":["acquire","davinci","splendor"],"productionChange":true,"docsOnly":false}' \
  docker-compose.prod.yml \
  .github/workflows/deploy.yml

assert_case \
  '{"center":[],"games":[],"productionChange":true,"docsOnly":false}' \
  deployments/IdentityMigration.Dockerfile

printf 'changed release component tests passed\n'
