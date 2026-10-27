#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if docker compose version >/dev/null 2>&1; then
  compose=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  compose=(docker-compose)
else
  printf 'Docker Compose is required\n' >&2
  exit 1
fi

config="$("${compose[@]}" -f docker-compose.prod.yml config)"

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

require_source() {
  local pattern="$1"
  local message="$2"
  if ! grep -Eq -- "$pattern" docker-compose.prod.yml; then
    printf 'production config rejected: %s\n' "$message" >&2
    exit 1
  fi
}

reject '/var/run/docker\.sock' 'raw Docker socket mount is forbidden'
reject 'POSTGRES_DSN:.*(CHANGE_ME|your-instance|example\.com)' 'PostgreSQL placeholder values must be replaced'
reject 'neon\.tech' 'legacy Neon endpoints are forbidden'
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
reject '^[[:space:]]{2}platform-api:[[:space:]]*$' 'legacy Platform API must not be deployed in production'
reject '^[[:space:]]{2}roomhost:[[:space:]]*$' 'legacy all-games RoomHost must not be deployed in production'
require 'POSTGRES_DSN:.*\.rds\.aliyuncs\.com(:5432)?/gamebus_prod\?[^[:space:]]*sslmode=require' 'PostgreSQL must use the shared Alibaba Cloud RDS gamebus_prod database with TLS'
require 'REDIS_ADDR:[[:space:]]*global-redis:6379' 'shared Redis must use global-redis'
require 'NATS_URL:[[:space:]]*nats://global-nats:4222' 'shared NATS must use global-nats'
require 'platform-rpc:' 'platform-rpc service is required'
require 'GAME_PLATFORM_RPC_PSM:[[:space:]]*game\.platform\.rpc' 'platform-rpc PSM must be game.platform.rpc'
require 'AUTH_RPC_ADDR:[[:space:]]*auth-backend:19090' 'Auth RPC must be configured for public profile resolution'
require 'AUTH_RPC_TIMEOUT:[[:space:]]*[0-9]+(ms|s|m)' 'Auth RPC timeout must be explicit'
require 'PROFILE_REFRESH_INTERVAL:[[:space:]]*[0-9]+(ms|s|m)' 'gateway profile refresh interval must be explicit'
require '^[[:space:]]{2}gateway:[[:space:]]*$' 'one realtime gateway instance is required'
require 'game-platform-gateway' 'realtime gateway global-network alias is required'

for game in acquire davinci splendor yinsh othello fugitive warchest quoridor hex loveletter sixnimmt forsale; do
  for slot in a b; do
    service="roomhost-${game}-${slot}"
    require "^[[:space:]]{2}${service}:[[:space:]]*$" "${service} slot is required"
    require "NODE_ID:[[:space:]]*${service}" "${service} must use its unique node ID"
  done
done

require_source '\$\{ACQUIRE_ROOMHOST_A_IMAGE:\?' 'Acquire A must use a complete independent image reference'
require_source '\$\{ACQUIRE_ROOMHOST_B_IMAGE:\?' 'Acquire B must use a complete independent image reference'
require_source '\$\{DAVINCI_ROOMHOST_A_IMAGE:\?' 'DaVinci A must use a complete independent image reference'
require_source '\$\{DAVINCI_ROOMHOST_B_IMAGE:\?' 'DaVinci B must use a complete independent image reference'
require_source '\$\{SPLENDOR_ROOMHOST_A_IMAGE:\?' 'Splendor A must use a complete independent image reference'
require_source '\$\{SPLENDOR_ROOMHOST_B_IMAGE:\?' 'Splendor B must use a complete independent image reference'
require_source '\$\{YINSH_ROOMHOST_A_IMAGE:\?' 'YINSH A must use a complete independent image reference'
require_source '\$\{YINSH_ROOMHOST_B_IMAGE:\?' 'YINSH B must use a complete independent image reference'
require_source '\$\{OTHELLO_ROOMHOST_A_IMAGE:\?' 'Othello A must use a complete independent image reference'
require_source '\$\{OTHELLO_ROOMHOST_B_IMAGE:\?' 'Othello B must use a complete independent image reference'
require_source '\$\{FUGITIVE_ROOMHOST_A_IMAGE:\?' 'Fugitive A must use a complete independent image reference'
require_source '\$\{FUGITIVE_ROOMHOST_B_IMAGE:\?' 'Fugitive B must use a complete independent image reference'
require_source '\$\{WARCHEST_ROOMHOST_A_IMAGE:\?' 'War Chest A must use a complete independent image reference'
require_source '\$\{WARCHEST_ROOMHOST_B_IMAGE:\?' 'War Chest B must use a complete independent image reference'
require_source '\$\{QUORIDOR_ROOMHOST_A_IMAGE:\?' 'Quoridor A must use a complete independent image reference'
require_source '\$\{QUORIDOR_ROOMHOST_B_IMAGE:\?' 'Quoridor B must use a complete independent image reference'
require_source '\$\{HEX_ROOMHOST_A_IMAGE:\?' 'Hex A must use a complete independent image reference'
require_source '\$\{HEX_ROOMHOST_B_IMAGE:\?' 'Hex B must use a complete independent image reference'
require_source '\$\{LOVELETTER_ROOMHOST_A_IMAGE:\?' 'Love Letter A must use a complete independent image reference'
require_source '\$\{LOVELETTER_ROOMHOST_B_IMAGE:\?' 'Love Letter B must use a complete independent image reference'
require_source '\$\{SIXNIMMT_ROOMHOST_A_IMAGE:\?' 'Six Nimmt A must use a complete independent image reference'
require_source '\$\{SIXNIMMT_ROOMHOST_B_IMAGE:\?' 'Six Nimmt B must use a complete independent image reference'
require_source '\$\{FORSALE_ROOMHOST_A_IMAGE:\?' 'For Sale A must use a complete independent image reference'
require_source '\$\{FORSALE_ROOMHOST_B_IMAGE:\?' 'For Sale B must use a complete independent image reference'
for prefix in ACQUIRE DAVINCI SPLENDOR YINSH OTHELLO FUGITIVE WARCHEST QUORIDOR HEX LOVELETTER SIXNIMMT FORSALE; do
  for slot in A B; do
    require_source \
      "\\\$\\{${prefix}_ROOMHOST_${slot}_VERSION:\\?" \
      "${prefix} ${slot} must advertise an independent artifact version"
  done
done

duplicate_node_ids="$(
  grep -E '^[[:space:]]+NODE_ID:' <<<"$config" |
    sed -E 's/.*NODE_ID:[[:space:]]*//' |
    sort |
    uniq -d
)"
if [[ -n "$duplicate_node_ids" ]]; then
  printf 'production config rejected: duplicate node IDs: %s\n' "$duplicate_node_ids" >&2
  exit 1
fi

printf 'production compose configuration passed validation\n'
