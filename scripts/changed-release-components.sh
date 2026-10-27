#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  printf 'usage: %s <base-commit> <head-commit>\n' "$0" >&2
  exit 2
fi

base="$1"
head="$2"
declare -A centers=()
declare -A games=()
production_change=0

mark_all_games() {
  games[acquire]=1
  games[davinci]=1
  games[splendor]=1
  games[yinsh]=1
  games[othello]=1
  games[fugitive]=1
  games[warchest]=1
  games[quoridor]=1
  games[hex]=1
  games[loveletter]=1
  games[sixnimmt]=1
  games[forsale]=1
}

mark_all_centers() {
  centers[migrate-schema]=1
  centers[control-plane]=1
  centers[platform-rpc]=1
  centers[gateway]=1
}

mark_all() {
  mark_all_games
  mark_all_centers
}

if [[ -n "${CHANGED_FILES_FILE:-}" ]]; then
  mapfile -t files <"$CHANGED_FILES_FILE"
else
  mapfile -t files < <(git diff --name-only "$base" "$head")
fi

for path in "${files[@]}"; do
  [[ -z "$path" ]] && continue
  case "$path" in
    *.md|docs/*|.trae/*)
      continue
      ;;
    *_test.go|testdata/*|*/testdata/*)
      continue
      ;;
  esac
  production_change=1

  case "$path" in
    internal/plugins/acquire/*|cmd/roomhost-acquire/*)
      games[acquire]=1
      ;;
    internal/plugins/davinci/*|cmd/roomhost-davinci/*)
      games[davinci]=1
      ;;
    internal/plugins/splendor/*|cmd/roomhost-splendor/*)
      games[splendor]=1
      ;;
    internal/plugins/yinsh/*|cmd/roomhost-yinsh/*)
      games[yinsh]=1
      ;;
    internal/plugins/othello/*|cmd/roomhost-othello/*)
      games[othello]=1
      ;;
    internal/plugins/fugitive/*|cmd/roomhost-fugitive/*)
      games[fugitive]=1
      ;;
    internal/plugins/warchest/*|cmd/roomhost-warchest/*)
      games[warchest]=1
      ;;
    internal/plugins/quoridor/*|cmd/roomhost-quoridor/*)
      games[quoridor]=1
      ;;
    internal/plugins/hex/*|cmd/roomhost-hex/*)
      games[hex]=1
      ;;
    internal/plugins/loveletter/*|cmd/roomhost-loveletter/*)
      games[loveletter]=1
      ;;
    internal/plugins/sixnimmt/*|cmd/roomhost-sixnimmt/*)
      games[sixnimmt]=1
      ;;
    internal/plugins/forsale/*|cmd/roomhost-forsale/*)
      games[forsale]=1
      ;;
    internal/game/*|internal/roomhost/*|internal/roomhostapp/*|internal/gamelog/*)
      mark_all_games
      ;;
    internal/history/*)
      mark_all_games
      centers[platform-rpc]=1
      ;;
    internal/transport/*)
      mark_all
      ;;
    internal/store/*|internal/contracts/*)
      mark_all
      ;;
    internal/control/*|internal/controlclient/*|cmd/control-plane/*)
      centers[control-plane]=1
      ;;
    internal/rpc/*|cmd/platform-rpc/*)
      centers[platform-rpc]=1
      ;;
    internal/gateway/*|cmd/gateway/*)
      centers[gateway]=1
      ;;
    cmd/migrate-schema/*)
      centers[migrate-schema]=1
      ;;
    docker-compose.prod.yml|.github/workflows/deploy.yml|scripts/deploy-game-slot.sh)
      mark_all
      ;;
    cmd/game-release/*|cmd/backfill-replay/*|scripts/*|docker-compose*.yml|.github/workflows/*|.env.example)
      ;;
    deployments/Dockerfile|go.mod|go.sum|kitex_gen/*)
      mark_all
      ;;
    internal/*|cmd/*)
      mark_all
      ;;
    *)
      mark_all
      ;;
  esac
done

center_json="$(
  for value in "${!centers[@]}"; do
    printf '%s\n' "$value"
  done | sort | jq -Rsc 'split("\n") | map(select(length > 0))'
)"
game_json="$(
  for value in "${!games[@]}"; do
    printf '%s\n' "$value"
  done | sort | jq -Rsc 'split("\n") | map(select(length > 0))'
)"

jq -cn \
  --argjson center "$center_json" \
  --argjson games "$game_json" \
  --argjson productionChange "$production_change" \
  '{
    center: $center,
    games: $games,
    productionChange: ($productionChange == 1),
    docsOnly: ($productionChange == 0)
  }'
