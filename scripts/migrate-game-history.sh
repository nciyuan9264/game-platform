#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -z "${LEGACY_GAME_POSTGRES_DSN:-}" || -z "${POSTGRES_DSN:-}" ]]; then
  echo "LEGACY_GAME_POSTGRES_DSN and POSTGRES_DSN are required" >&2
  exit 1
fi

cd "$ROOT"
exec go run ./cmd/migrate-game-history "$@"
