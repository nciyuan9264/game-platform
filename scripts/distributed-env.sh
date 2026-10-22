#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PRIMARY_HOST="${PRIMARY_HOST:-10.37.223.1}"
SECONDARY_HOST="${SECONDARY_HOST:-10.37.4.66}"
SECONDARY_SSH="${SECONDARY_SSH:-$SECONDARY_HOST}"
SECONDARY_DOCKER_HOST="${SECONDARY_DOCKER_HOST:-ssh://$SECONDARY_SSH}"
export DISTRIBUTED_BIND_ADDR="${DISTRIBUTED_BIND_ADDR:-$PRIMARY_HOST}"
export ENTRYPOINT_BIND_ADDR="${ENTRYPOINT_BIND_ADDR:-$PRIMARY_HOST}"
ENTRYPOINT_HOST="${ENTRYPOINT_HOST:-$PRIMARY_HOST}"
ENTRYPOINT_PORT="${ENTRYPOINT_PORT:-18000}"
CONTROL_HTTP_PORT="${CONTROL_HTTP_PORT:-18080}"
PLATFORM_API_A_PORT="${PLATFORM_API_A_PORT:-18083}"
PLATFORM_API_B_PORT="${PLATFORM_API_B_PORT:-18083}"
GATEWAY_A_PORT="${GATEWAY_A_PORT:-18081}"
GATEWAY_B_PORT="${GATEWAY_B_PORT:-18081}"

compose_primary() {
  docker compose -f docker-compose.distributed.primary.yml "$@"
}

compose_secondary() {
  docker compose -f docker-compose.distributed.secondary.yml "$@"
}

compose_secondary_remote() {
  DOCKER_HOST="$SECONDARY_DOCKER_HOST" docker compose -f docker-compose.distributed.secondary.yml "$@"
}

secondary_images() {
  printf '%s\n' \
    game-platform-distributed-secondary-platform-rpc-2 \
    game-platform-distributed-secondary-platform-api-2 \
    game-platform-distributed-secondary-gateway-2 \
    game-platform-distributed-secondary-roomhost-2
}

build_secondary_local() {
  compose_secondary build
}

load_secondary_images_remote() {
  local images
  images="$(secondary_images | tr '\n' ' ')"
  # The secondary host does not need source code or registry access; it only needs
  # the final runtime images streamed from primary.
  docker save $images | ssh "$SECONDARY_SSH" 'docker load'
}

up_secondary_remote_from_loaded_images() {
  build_secondary_local
  load_secondary_images_remote
  compose_secondary_remote up -d --no-build
}

escape_sed_replacement() {
  printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'
}

render_traefik() {
  mkdir -p .generated
  local primary secondary control platform_a platform_b gateway_a gateway_b
  primary="$(escape_sed_replacement "$PRIMARY_HOST")"
  secondary="$(escape_sed_replacement "$SECONDARY_HOST")"
  control="$(escape_sed_replacement "$CONTROL_HTTP_PORT")"
  platform_a="$(escape_sed_replacement "$PLATFORM_API_A_PORT")"
  platform_b="$(escape_sed_replacement "$PLATFORM_API_B_PORT")"
  gateway_a="$(escape_sed_replacement "$GATEWAY_A_PORT")"
  gateway_b="$(escape_sed_replacement "$GATEWAY_B_PORT")"
  sed \
    -e "s/__PRIMARY_HOST__/$primary/g" \
    -e "s/__SECONDARY_HOST__/$secondary/g" \
    -e "s/__CONTROL_HTTP_PORT__/$control/g" \
    -e "s/__PLATFORM_API_A_PORT__/$platform_a/g" \
    -e "s/__PLATFORM_API_B_PORT__/$platform_b/g" \
    -e "s/__GATEWAY_A_PORT__/$gateway_a/g" \
    -e "s/__GATEWAY_B_PORT__/$gateway_b/g" \
    deployments/traefik-distributed.yml.tpl > .generated/traefik-distributed.yml
  echo "rendered .generated/traefik-distributed.yml"
}

trust_secondary_host() {
  mkdir -p ~/.ssh
  chmod 700 ~/.ssh
  if ssh-keygen -F "$SECONDARY_HOST" >/dev/null 2>&1; then
    echo "secondary host key already exists for $SECONDARY_HOST"
    return 0
  fi
  ssh-keyscan -H "$SECONDARY_HOST" >> ~/.ssh/known_hosts
  chmod 600 ~/.ssh/known_hosts
  echo "added secondary host key for $SECONDARY_HOST"
}

check_secondary_ssh() {
  ssh -o BatchMode=yes -o ConnectTimeout=5 "$SECONDARY_SSH" \
    'hostname && whoami && docker version --format "remote docker server={{.Server.Version}}"'
  DOCKER_HOST="$SECONDARY_DOCKER_HOST" docker version --format 'remote docker via client={{.Client.Version}} server={{.Server.Version}}'
}

wait_entrypoint() {
  local base="http://${ENTRYPOINT_HOST}:${ENTRYPOINT_PORT}"
  for _ in {1..90}; do
    if curl -fsS "$base/platform/healthz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "entrypoint is not ready: $base" >&2
  return 1
}

build_smoke() {
  export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
  export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"
  export GOCACHE="${GOCACHE:-/tmp/game-platform-gocache}"
  export GOMODCACHE="${GOMODCACHE:-/tmp/game-platform-gomodcache}"
  mkdir -p .bin
  go build -buildvcs=false -o .bin/smoke-test ./cmd/smoke-test
}

run_smoke() {
  local scenario="${1:-}"
  local base="http://${ENTRYPOINT_HOST}:${ENTRYPOINT_PORT}"
  local ws="ws://${ENTRYPOINT_HOST}:${ENTRYPOINT_PORT}/ws"
  build_smoke
  wait_entrypoint
  if [[ -n "$scenario" ]]; then
    SCENARIO="$scenario" AUTH_URL="$base" CONTROL_URL="$base" GW_LB_WS="$ws" ./.bin/smoke-test
  else
    AUTH_URL="$base" CONTROL_URL="$base" GW_LB_WS="$ws" ./.bin/smoke-test
  fi
}

cmd="${1:-help}"
case "$cmd" in
  render-traefik)
    render_traefik
    ;;
  trust-secondary)
    trust_secondary_host
    ;;
  check-secondary)
    check_secondary_ssh
    ;;
  up-primary)
    render_traefik
    compose_primary up -d --build
    ;;
  up-secondary)
    compose_secondary up -d --build
    ;;
  up-secondary-remote)
    check_secondary_ssh
    up_secondary_remote_from_loaded_images
    ;;
  deploy-remote)
    render_traefik
    compose_primary up -d --build
    check_secondary_ssh
    up_secondary_remote_from_loaded_images
    run_smoke
    ;;
  restart-traefik)
    render_traefik
    compose_primary up -d --no-deps traefik
    ;;
  smoke)
    run_smoke
    ;;
  smoke-acquire)
    run_smoke acquire
    ;;
  ps-primary)
    compose_primary ps
    ;;
  ps-secondary)
    compose_secondary ps
    ;;
  ps-secondary-remote)
    compose_secondary_remote ps
    ;;
  logs-primary)
    target="${2:-}"
    if [[ -n "$target" ]]; then
      compose_primary logs -f "$target"
    else
      compose_primary logs -f
    fi
    ;;
  logs-secondary)
    target="${2:-}"
    if [[ -n "$target" ]]; then
      compose_secondary logs -f "$target"
    else
      compose_secondary logs -f
    fi
    ;;
  logs-secondary-remote)
    target="${2:-}"
    if [[ -n "$target" ]]; then
      compose_secondary_remote logs -f "$target"
    else
      compose_secondary_remote logs -f
    fi
    ;;
  down-primary)
    compose_primary down --remove-orphans
    ;;
  down-secondary)
    compose_secondary down --remove-orphans
    ;;
  down-secondary-remote)
    compose_secondary_remote down --remove-orphans
    ;;
  config-primary)
    render_traefik
    compose_primary config
    ;;
  config-secondary)
    compose_secondary config
    ;;
  config-secondary-remote)
    compose_secondary_remote config
    ;;
  help|*)
    cat <<'EOF'
Usage:
  scripts/distributed-env.sh deploy-remote       # run on primary: deploy both hosts and run smoke
  scripts/distributed-env.sh up-primary          # run on 10.37.223.1
  scripts/distributed-env.sh up-secondary        # run on 10.37.4.66 when code also exists there
  scripts/distributed-env.sh up-secondary-remote # run on primary: build locally, stream images, run secondary remotely
  scripts/distributed-env.sh trust-secondary     # add secondary SSH host key to known_hosts
  scripts/distributed-env.sh check-secondary     # verify SSH and remote Docker access
  scripts/distributed-env.sh smoke               # run from primary after up-primary/up-secondary
  scripts/distributed-env.sh smoke-acquire       # Acquire scenario through the same entrypoint
  scripts/distributed-env.sh ps-primary|ps-secondary|ps-secondary-remote
  scripts/distributed-env.sh logs-primary [service]
  scripts/distributed-env.sh logs-secondary [service]
  scripts/distributed-env.sh logs-secondary-remote [service]
  scripts/distributed-env.sh down-primary|down-secondary|down-secondary-remote
  scripts/distributed-env.sh config-primary|config-secondary|config-secondary-remote

Defaults:
  PRIMARY_HOST=10.37.223.1
  SECONDARY_HOST=10.37.4.66
  SECONDARY_SSH=10.37.4.66
  SECONDARY_DOCKER_HOST=ssh://10.37.4.66
  ENTRYPOINT=http://10.37.223.1:18000
  DISTRIBUTED_BIND_ADDR=10.37.223.1
  ENTRYPOINT_BIND_ADDR=10.37.223.1

For one-copy deployment, keep source only on primary and run:
  SECONDARY_SSH=<user>@10.37.4.66 ./scripts/distributed-env.sh deploy-remote

Required cross-host ports:
  primary inbound from secondary: 5432, 6379, 2379, 4222, 19090, 18091
  secondary inbound from primary: 18083, 18081, 18092, 18093
  browser/test inbound to primary: 18000
EOF
    ;;
esac
