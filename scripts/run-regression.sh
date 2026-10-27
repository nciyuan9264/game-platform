#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRONTEND_ROOT="${GAME_FRONTEND_ROOT:-$ROOT/../game-frontend}"
MODE="${1:-quality}"
RUN_ID="${REGRESSION_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
REPORT_ROOT="${REGRESSION_REPORT_ROOT:-$ROOT/.artifacts/regression/$RUN_ID}"
GAMES="${REGRESSION_GAMES:-acquire,davinci,splendor,yinsh,othello,warchest,quoridor,onitama}"

run_step() {
  local name="$1"
  shift
  local started
  started="$(date +%s)"
  printf '[regression] %s\n' "$name"
  "$@"
  printf '[regression] %s completed in %ss\n' "$name" "$(( $(date +%s) - started ))"
}

run_quality() {
  (
    cd "$ROOT"
    run_step "Go vet" go vet ./...
  ) &
  local vet_pid=$!
  (
    cd "$ROOT"
    run_step "Go tests" go test ./...
  ) &
  local test_pid=$!
  (
    cd "$FRONTEND_ROOT"
    run_step "frontend lint" pnpm lint
    run_step "frontend tests" pnpm test
    run_step "frontend generated clients" pnpm check:generated
    run_step "frontend build" pnpm build
  ) &
  local frontend_pid=$!
  wait "$vet_pid"
  wait "$test_pid"
  wait "$frontend_pid"
}

run_go_regression() {
  local mode="$1"
  mkdir -p "$REPORT_ROOT/$mode"
  (
    cd "$ROOT"
    go run ./cmd/regression \
      --mode "$mode" \
      --games "$GAMES" \
      --report-dir "$REPORT_ROOT/$mode"
  )
}

case "$MODE" in
  quality)
    run_quality
    ;;
  probe)
    run_step "production probe" run_go_regression probe
    ;;
  canary)
    run_step "production canary" run_go_regression canary
    ;;
  all)
    run_quality
    run_step "production probe" run_go_regression probe
    run_step "production canary" run_go_regression canary
    (
      cd "$FRONTEND_ROOT"
      run_step "production browser regression" pnpm test:e2e:production
    )
    ;;
  *)
    printf 'usage: %s quality|probe|canary|all\n' "$0" >&2
    exit 2
    ;;
esac

printf '[regression] reports: %s\n' "$REPORT_ROOT"
