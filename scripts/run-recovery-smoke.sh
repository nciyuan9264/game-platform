#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOTOOLCHAIN=local
export GOCACHE="${GOCACHE:-/tmp/game-platform-gocache}"
export GOMODCACHE="${GOMODCACHE:-/tmp/game-platform-gomodcache}"

BIN_DIR="$ROOT/.bin"
LOG_DIR="$ROOT/.logs"
mkdir -p "$BIN_DIR" "$LOG_DIR"

REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"
REDIS_PASSWORD="${REDIS_PASSWORD:-local-redis}"
REDIS_DB="${REDIS_DB:-0}"
POSTGRES_DSN="${POSTGRES_DSN:-postgres://game:game@localhost:5432/game_platform?sslmode=disable}"
AUTH_POSTGRES_DSN="${AUTH_POSTGRES_DSN:-postgres://auth:auth@localhost:5432/auth_center?sslmode=disable}"
ETCD_ENDPOINTS="${ETCD_ENDPOINTS:-localhost:2379}"
NATS_URL="${NATS_URL:-nats://localhost:4222}"
PLATFORM_API_SERVER_ADDR="${PLATFORM_API_SERVER_ADDR:-:19000}"
CONTROL_SERVER_ADDR="${CONTROL_SERVER_ADDR:-:18080}"
CONTROL_RPC_ADDR="${CONTROL_RPC_ADDR:-localhost:18091}"
GATEWAY1_SERVER_ADDR="${GATEWAY1_SERVER_ADDR:-:18081}"
GATEWAY2_SERVER_ADDR="${GATEWAY2_SERVER_ADDR:-:18082}"
AUTH_HTTP_URL="${AUTH_HTTP_URL:-http://localhost:19000}"
CONTROL_HTTP_URL="${CONTROL_HTTP_URL:-http://localhost:19000}"
GATEWAY1_HTTP_URL="${GATEWAY1_HTTP_URL:-http://localhost:18081}"
GATEWAY2_HTTP_URL="${GATEWAY2_HTTP_URL:-http://localhost:18082}"
GATEWAY_LB_HTTP_URL="${GATEWAY_LB_HTTP_URL:-http://localhost:18000}"
GW1_WS="${GW1_WS:-ws://localhost:18081/ws}"
GW2_WS="${GW2_WS:-ws://localhost:18082/ws}"
GW_LB_WS="${GW_LB_WS:-ws://localhost:18000/ws}"
USE_GATEWAY_LB="${USE_GATEWAY_LB:-1}"
REUSE_INFRA="${REUSE_INFRA:-0}"
AUTH_RPC_ADDR="${AUTH_RPC_ADDR:-localhost:19090}"

echo "[1/7] build binaries"
(cd "$ROOT/../auth/backend" && GOPATH=/tmp/auth-kitex-gopath GOCACHE="${GOCACHE:-/tmp/auth-gocache}" GOMODCACHE="${GOMODCACHE:-/tmp/auth-gomodcache}" GOPROXY="$GOPROXY" GOTOOLCHAIN=local go build -buildvcs=false -o "$BIN_DIR/auth-rpc" ./cmd/auth-rpc)
(cd "$ROOT/../auth/backend" && GOPATH=/tmp/auth-kitex-gopath GOCACHE="${GOCACHE:-/tmp/auth-gocache}" GOMODCACHE="${GOMODCACHE:-/tmp/auth-gomodcache}" GOPROXY="$GOPROXY" GOTOOLCHAIN=local go build -buildvcs=false -o "$BIN_DIR/auth-migrate-schema" ./cmd/migrate-schema)
go build -buildvcs=false -o "$BIN_DIR/migrate-schema" ./cmd/migrate-schema
go build -buildvcs=false -o "$BIN_DIR/control-plane" ./cmd/control-plane
go build -buildvcs=false -o "$BIN_DIR/platform-api" ./cmd/platform-api
go build -buildvcs=false -o "$BIN_DIR/gateway" ./cmd/gateway
go build -buildvcs=false -o "$BIN_DIR/roomhost" ./cmd/roomhost
go build -buildvcs=false -o "$BIN_DIR/smoke-test" ./cmd/smoke-test

echo "[2/7] start redis/postgres/etcd/nats containers"
started_infra=0
if [[ "$REUSE_INFRA" == "1" ]]; then
  echo "reuse existing redis/postgres/etcd/nats"
else
  started_infra=1
  docker rm -f gp-redis gp-postgres gp-etcd gp-nats gp-gateway-lb >/dev/null 2>&1 || true
  docker run -d --name gp-redis -p 6379:6379 \
    -e REDIS_PASSWORD="$REDIS_PASSWORD" \
    redis:7-alpine sh -ec \
    'exec redis-server --appendonly yes --appendfsync everysec --requirepass "$REDIS_PASSWORD"' >/dev/null
  docker run -d --name gp-postgres -p 5432:5432 \
    -e POSTGRES_USER=game \
    -e POSTGRES_PASSWORD=game \
    -e POSTGRES_DB=game_platform \
    -e AUTH_DB_ADMIN_PASSWORD=auth \
    -e GAME_MIGRATOR_PASSWORD=local-game-migrator \
    -e GAME_RUNTIME_PASSWORD=local-game-runtime \
    -e AUTH_MIGRATOR_PASSWORD=local-auth-migrator \
    -e AUTH_RUNTIME_PASSWORD=local-auth-runtime \
    -v "$ROOT/deployments/postgres-init/001-create-databases-and-roles.sh:/docker-entrypoint-initdb.d/001-create-databases-and-roles.sh:ro" \
    postgres:16-alpine >/dev/null
  for _ in {1..30}; do
    if docker exec gp-postgres pg_isready -U game -d game_platform >/dev/null 2>&1; then
      break
    fi
    sleep 0.5
  done
  docker run -d --name gp-etcd -p 2379:2379 quay.io/coreos/etcd:v3.5.15 \
    /usr/local/bin/etcd \
    --name=etcd-1 \
    --data-dir=/etcd-data \
    --listen-client-urls=http://0.0.0.0:2379 \
    --advertise-client-urls=http://127.0.0.1:2379 \
    --listen-peer-urls=http://0.0.0.0:2380 \
    --initial-advertise-peer-urls=http://127.0.0.1:2380 \
    --initial-cluster=etcd-1=http://127.0.0.1:2380 \
    --initial-cluster-state=new >/dev/null
  docker run -d --name gp-nats -p 4222:4222 -p 8222:8222 nats:2-alpine -js -m 8222 -D -V >/dev/null
fi

pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do
    kill "$pid" >/dev/null 2>&1 || true
  done
  for _ in {1..20}; do
    local alive=0
    for pid in "${pids[@]:-}"; do
      if kill -0 "$pid" >/dev/null 2>&1; then
        alive=1
      fi
    done
    [[ "$alive" == "0" ]] && break
    sleep 0.1
  done
  if [[ "$started_infra" == "1" || "$USE_GATEWAY_LB" == "1" ]]; then
    docker rm -f gp-redis gp-postgres gp-etcd gp-nats gp-gateway-lb >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

start() {
  local name="$1"
  shift
  echo "start $name"
  ( "$@" >"$LOG_DIR/$name.log" 2>&1 ) &
  pids+=("$!")
}

echo "[3/7] migrate schemas"
POSTGRES_DSN="$POSTGRES_DSN" "$BIN_DIR/migrate-schema"
POSTGRES_DSN="$AUTH_POSTGRES_DSN" "$BIN_DIR/auth-migrate-schema"

echo "[4/7] start services with only roomhost-1"
AUTH_RPC_ADDR="$AUTH_RPC_ADDR" POSTGRES_DSN="$AUTH_POSTGRES_DSN" REDIS_ADDR="$REDIS_ADDR" REDIS_PASSWORD="$REDIS_PASSWORD" REDIS_DB=2 AUTH_DEV_CODE_LOG=true AUTH_DEV_CODE_RESPONSE=true JWT_PRIVATE_PATH="$ROOT/../auth/keys/jwt_private.pem" JWT_PUBLIC_PATH="$ROOT/../auth/keys/jwt_public.pem" start auth-rpc "$BIN_DIR/auth-rpc"
POSTGRES_DSN="$POSTGRES_DSN" ETCD_ENDPOINTS="$ETCD_ENDPOINTS" REDIS_ADDR="$REDIS_ADDR" REDIS_PASSWORD="$REDIS_PASSWORD" REDIS_DB="$REDIS_DB" NATS_URL="$NATS_URL" AUTH_RPC_ADDR="$AUTH_RPC_ADDR" SERVER_ADDR="$CONTROL_SERVER_ADDR" CONTROL_RPC_ADDR="$CONTROL_RPC_ADDR" NODE_ID=control-plane-1 start control-plane "$BIN_DIR/control-plane"
POSTGRES_DSN="$POSTGRES_DSN" REDIS_ADDR="$REDIS_ADDR" REDIS_PASSWORD="$REDIS_PASSWORD" REDIS_DB="$REDIS_DB" AUTH_RPC_ADDR="$AUTH_RPC_ADDR" CONTROL_RPC_ADDR="$CONTROL_RPC_ADDR" AUTH_DEV_CODE_RESPONSE=true SERVER_ADDR="$PLATFORM_API_SERVER_ADDR" COOKIE_DOMAIN="" COOKIE_SECURE=false start platform-api "$BIN_DIR/platform-api"
POSTGRES_DSN="$POSTGRES_DSN" ETCD_ENDPOINTS="$ETCD_ENDPOINTS" REDIS_ADDR="$REDIS_ADDR" REDIS_PASSWORD="$REDIS_PASSWORD" REDIS_DB="$REDIS_DB" NATS_URL="$NATS_URL" NODE_ID=roomhost-1 start roomhost-1 "$BIN_DIR/roomhost"
POSTGRES_DSN="$POSTGRES_DSN" ETCD_ENDPOINTS="$ETCD_ENDPOINTS" REDIS_ADDR="$REDIS_ADDR" REDIS_PASSWORD="$REDIS_PASSWORD" REDIS_DB="$REDIS_DB" NATS_URL="$NATS_URL" AUTH_RPC_ADDR="$AUTH_RPC_ADDR" SERVER_ADDR="$GATEWAY1_SERVER_ADDR" NODE_ID=gateway-1 start gateway-1 "$BIN_DIR/gateway"
POSTGRES_DSN="$POSTGRES_DSN" ETCD_ENDPOINTS="$ETCD_ENDPOINTS" REDIS_ADDR="$REDIS_ADDR" REDIS_PASSWORD="$REDIS_PASSWORD" REDIS_DB="$REDIS_DB" NATS_URL="$NATS_URL" AUTH_RPC_ADDR="$AUTH_RPC_ADDR" SERVER_ADDR="$GATEWAY2_SERVER_ADDR" NODE_ID=gateway-2 start gateway-2 "$BIN_DIR/gateway"

if [[ "$USE_GATEWAY_LB" == "1" ]]; then
  docker run -d --name gp-gateway-lb -p 18000:8080 \
    --add-host=host.docker.internal:host-gateway \
    -v "$ROOT/deployments/nginx-local-gateway.conf:/etc/nginx/nginx.conf:ro" \
    nginx:1.27-alpine >/dev/null
fi

echo "[5/7] wait readiness"
ready() {
  curl -fsS "$AUTH_HTTP_URL/platform/healthz" >/dev/null 2>&1 &&
    curl -fsS "http://localhost${CONTROL_SERVER_ADDR}/healthz" >/dev/null 2>&1 &&
    curl -fsS "$GATEWAY1_HTTP_URL/healthz" >/dev/null 2>&1 &&
    curl -fsS "$GATEWAY2_HTTP_URL/healthz" >/dev/null 2>&1 &&
    { [[ "$USE_GATEWAY_LB" != "1" ]] || curl -fsS "$GATEWAY_LB_HTTP_URL/healthz" >/dev/null 2>&1; }
}
for i in {1..40}; do
  if ready; then
    break
  fi
  sleep 0.5
done
sleep 2

echo "[6/7] start smoke and force roomhost failover"
gateway_env=(GW_LB_WS="$GW_LB_WS")
if [[ "$USE_GATEWAY_LB" != "1" ]]; then
  gateway_env=(GW1_WS="$GW1_WS" GW2_WS="$GW2_WS")
fi
env RECOVERY_WAIT="${RECOVERY_WAIT:-38s}" AUTH_URL="$AUTH_HTTP_URL" CONTROL_URL="$CONTROL_HTTP_URL" "${gateway_env[@]}" "$BIN_DIR/smoke-test" >"$LOG_DIR/recovery-smoke.log" 2>&1 &
smoke_pid="$!"

for i in {1..40}; do
  if grep -q "waiting recovery" "$LOG_DIR/recovery-smoke.log" 2>/dev/null; then
    break
  fi
  sleep 0.5
done

echo "kill roomhost-1"
for idx in "${!pids[@]}"; do
  pid="${pids[$idx]}"
  if ps -p "$pid" -o command= | grep -q "/.bin/roomhost"; then
    kill "$pid" >/dev/null 2>&1 || true
    unset 'pids[$idx]'
    break
  fi
done

POSTGRES_DSN="$POSTGRES_DSN" ETCD_ENDPOINTS="$ETCD_ENDPOINTS" REDIS_ADDR="$REDIS_ADDR" REDIS_PASSWORD="$REDIS_PASSWORD" REDIS_DB="$REDIS_DB" NATS_URL="$NATS_URL" NODE_ID=roomhost-2 start roomhost-2 "$BIN_DIR/roomhost"
wait "$smoke_pid"
cat "$LOG_DIR/recovery-smoke.log"

if [[ "${SCENARIO:-}" != "acquire" ]] &&
  { ! grep -q "websocket connected" "$LOG_DIR/gateway-1.log" || ! grep -q "websocket connected" "$LOG_DIR/gateway-2.log"; }; then
  echo "gateway load balance check failed"
  echo "gateway-1 connections:"
  grep "websocket connected" "$LOG_DIR/gateway-1.log" || true
  echo "gateway-2 connections:"
  grep "websocket connected" "$LOG_DIR/gateway-2.log" || true
  exit 1
fi

echo "[7/7] recovery smoke done"
