#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

cmd="${1:-help}"

compose() {
  docker compose "$@"
}

case "$cmd" in
  up-infra)
    compose -f docker-compose.integration.infra.yml up -d --build
    ;;
  up-a)
    compose -f docker-compose.integration.stack-a.yml up -d --build
    ;;
  up-b)
    compose -f docker-compose.integration.stack-b.yml up -d --build
    ;;
  down)
    compose -f docker-compose.integration.stack-b.yml down --remove-orphans || true
    compose -f docker-compose.integration.stack-a.yml down --remove-orphans || true
    compose -f docker-compose.integration.infra.yml down --remove-orphans || true
    ;;
  ps)
    compose -f docker-compose.integration.infra.yml ps
    compose -f docker-compose.integration.stack-a.yml ps
    compose -f docker-compose.integration.stack-b.yml ps
    ;;
  logs)
    target="${2:-}"
    if [[ -n "$target" ]]; then
      docker logs -f "$target"
    else
      echo "Pass a container name, for example: $0 logs game-platform-a-gateway-1"
    fi
    ;;
  smoke-lb)
    export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
    export GOTOOLCHAIN=local
    export GOCACHE="${GOCACHE:-/tmp/game-platform-gocache}"
    export GOMODCACHE="${GOMODCACHE:-/tmp/game-platform-gomodcache}"
    go build -buildvcs=false -o .bin/smoke-test ./cmd/smoke-test
    for _ in {1..60}; do
      if curl -fsS http://localhost:18000/platform/healthz >/dev/null 2>&1 &&
        curl -fsS http://localhost:18000/platform/instances >/dev/null 2>&1; then
        break
      fi
      sleep 1
    done
    AUTH_URL=http://localhost:18000 \
      CONTROL_URL=http://localhost:18000 \
      GW_LB_WS=ws://localhost:18000/ws \
      ./.bin/smoke-test
    ;;
  help|*)
    cat <<'EOF'
Usage:
  scripts/integration-env.sh up-infra   # Redis/Postgres/etcd/NATS/Traefik/Frontend
  scripts/integration-env.sh up-a       # first server: auth/control/platform-api/gateway/roomhost
  scripts/integration-env.sh up-b       # second server: platform-api/gateway/roomhost
  scripts/integration-env.sh smoke-lb   # run smoke through Traefik on :18000
  scripts/integration-env.sh ps
  scripts/integration-env.sh logs <container>
  scripts/integration-env.sh down

Open:
  Frontend console: http://localhost:18000
  CloudBeaver database console: http://localhost:18030
EOF
    ;;
esac
