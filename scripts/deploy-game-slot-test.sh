#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
deploy_script="$ROOT/scripts/deploy-game-slot.sh"

assert_supported() {
  local game="$1"
  local output
  local status

  set +e
  output="$("$deploy_script" "$game" invalid-reference 2>&1)"
  status=$?
  set -e

  if [[ "$status" -ne 2 ]] ||
    [[ "$output" != "target image must be an immutable full reference: invalid-reference" ]]; then
    printf 'expected %s to reach image validation, got status %s: %s\n' \
      "$game" "$status" "$output" >&2
    exit 1
  fi
}

for game in acquire davinci splendor yinsh othello fugitive warchest quoridor loveletter forsale; do
  assert_supported "$game"
done

set +e
unsupported_output="$("$deploy_script" unsupported invalid-reference 2>&1)"
unsupported_status=$?
set -e
if [[ "$unsupported_status" -ne 2 ]] ||
  [[ "$unsupported_output" != "unsupported game: unsupported" ]]; then
  printf 'unsupported game validation failed with status %s: %s\n' \
    "$unsupported_status" "$unsupported_output" >&2
  exit 1
fi

printf 'deploy game slot tests passed\n'
