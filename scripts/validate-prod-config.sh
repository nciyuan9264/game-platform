#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

config="$(docker compose -f docker-compose.prod.yml config)"

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
reject 'CHANGE_ME' 'placeholder values in .env must be replaced'
reject 'sslmode=disable' 'PostgreSQL TLS must not be disabled'
reject '^[[:space:]]{2}(redis|postgres|nats|auth-rpc|auth-schema-migrate):[[:space:]]*$' 'shared infrastructure and Auth must not be deployed by Game Platform'
reject 'POSTGRES_DSN:.*@(postgres|localhost|127\.0\.0\.1)(:|/)' 'PostgreSQL must use the remote RDS endpoint'
reject 'local-(redis|auth|control|gateway|roomhost|maintenance)' 'local fallback credential is forbidden'
reject 'COOKIE_SECURE:[[:space:]]*"?false"?' 'secure cookies are required'
reject 'AUTH_DEV_CODE_(LOG|RESPONSE):[[:space:]]*"?true"?' 'development verification codes are forbidden'
reject 'ACQUIRE_LOADTEST_ENABLED:[[:space:]]*"?true"?' 'load-test routes are forbidden'
reject '--api\.insecure=true' 'insecure Traefik dashboard is forbidden'
reject '(platform-api|gateway|roomhost)-[0-9]+:' 'single-server production must not use numbered platform instances'
reject '(platform-api|gateway)-lb:' 'single-server production must not deploy internal load balancers'
require 'REDIS_ADDR:[[:space:]]*global-redis:6379' 'shared Redis must use global-redis'
require 'NATS_URL:[[:space:]]*nats://global-nats:4222' 'shared NATS must use global-nats'
require 'platform-rpc:' 'platform-rpc service is required'
require 'GAME_PLATFORM_RPC_PSM:[[:space:]]*game\.platform\.rpc' 'platform-rpc PSM must be game.platform.rpc'
require '^[[:space:]]{2}platform-api:[[:space:]]*$' 'one platform-api instance is required'
require '^[[:space:]]{2}gateway:[[:space:]]*$' 'one realtime gateway instance is required'
require '^[[:space:]]{2}roomhost:[[:space:]]*$' 'one roomhost instance is required'
require 'game-platform-gateway' 'realtime gateway global-network alias is required'

printf 'production compose configuration passed validation\n'
