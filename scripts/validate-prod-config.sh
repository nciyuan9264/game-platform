#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

config="$(docker compose -f docker-compose.yml -f docker-compose.prod.yml config)"

reject() {
  local pattern="$1"
  local message="$2"
  if grep -Eiq -- "$pattern" <<<"$config"; then
    printf 'production config rejected: %s\n' "$message" >&2
    exit 1
  fi
}

require() {
  local pattern="$1"
  local message="$2"
  if ! grep -Eiq -- "$pattern" <<<"$config"; then
    printf 'production config rejected: %s\n' "$message" >&2
    exit 1
  fi
}

reject '/var/run/docker\.sock' 'raw Docker socket mount is forbidden'
reject 'sslmode=disable' 'PostgreSQL TLS must not be disabled'
reject 'local-(redis|auth|control|gateway|roomhost|maintenance)' 'local fallback credential is forbidden'
reject 'COOKIE_SECURE:[[:space:]]*"?false"?' 'secure cookies are required'
reject 'AUTH_DEV_CODE_(LOG|RESPONSE):[[:space:]]*"?true"?' 'development verification codes are forbidden'
reject 'ACQUIRE_LOADTEST_ENABLED:[[:space:]]*"?true"?' 'load-test routes are forbidden'
reject '--api\.insecure=true' 'insecure Traefik dashboard is forbidden'
require 'platform-rpc:' 'platform-rpc service is required'
require 'GAME_PLATFORM_RPC_PSM:[[:space:]]*game\.platform\.rpc' 'platform-rpc PSM must be game.platform.rpc'
require 'game-platform-gateway' 'realtime gateway global-network alias is required'

printf 'production compose configuration passed validation\n'
