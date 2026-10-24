#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

: "${POSTGRES_DSN:?POSTGRES_DSN is required}"
if [[ -z "${PLAYER_IDENTITY_MIGRATION_IMAGE:-}" ]]; then
  registry="${GAME_PLATFORM_IMAGE_REGISTRY:-sgccr.ccs.tencentyun.com/tengxuntcr}"
  tag="${GAME_PLATFORM_IMAGE_TAG:-latest}"
  PLAYER_IDENTITY_MIGRATION_IMAGE="${registry}/game-platform-identity-migration:${tag}"
fi
if [[ "$PLAYER_IDENTITY_MIGRATION_IMAGE" == *:latest ]]; then
  printf 'PLAYER_IDENTITY_MIGRATION_IMAGE must use an immutable tag\n' >&2
  exit 1
fi
export PLAYER_IDENTITY_MIGRATION_IMAGE

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
report_dir="/home/game-platform/backups/player-identity-${timestamp}"
mkdir -p "$report_dir"
chmod 700 "$report_dir"

if docker compose version >/dev/null 2>&1; then
  compose=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  compose=(docker-compose)
else
  printf 'Docker Compose is required\n' >&2
  exit 1
fi
compose+=(-f docker-compose.prod.yml --profile tools)
services=(
  gateway
  control-plane
  platform-rpc
  roomhost-acquire-a
  roomhost-acquire-b
  roomhost-davinci-a
  roomhost-davinci-b
  roomhost-splendor-a
  roomhost-splendor-b
)
maintenance=0
completed=0
legacy_stopped=0
legacy_containers=()

on_exit() {
  local status=$?
  if [[ "$status" -ne 0 ]]; then
    printf 'Migration failed. Reports: %s\n' "$report_dir" >&2
    for report in dry-run-live.json dry-run-stopped.json apply.json verify.json; do
      if [[ -s "$report_dir/$report" ]]; then
        printf '%s=' "$report" >&2
        jq -c . "$report_dir/$report" >&2 || true
      fi
    done
  fi
  if [[ "$maintenance" -eq 1 && "$completed" -ne 1 ]]; then
    {
      printf 'Migration did not complete. Write services remain stopped.\n'
      printf 'Review reports in %s before recovery.\n' "$report_dir"
      printf 'Recovery command: docker compose -f docker-compose.prod.yml up -d --no-deps'
      printf ' %q' "${services[@]}"
      printf '\n'
      if [[ "$legacy_stopped" -eq 1 ]]; then
        printf 'Legacy RoomHost recovery command: docker start'
        printf ' %q' "${legacy_containers[@]}"
        printf '\n'
      fi
    } >&2
  fi
  exit "$status"
}
trap on_exit EXIT

run_migration() {
  local output="$1"
  shift
  "${compose[@]}" run --rm --no-deps normalize-player-identities "$@" >"$output"
}

run_acceptance() {
  "${compose[@]}" run --rm --no-deps --entrypoint psql \
    normalize-player-identities "$POSTGRES_DSN" \
    --no-psqlrc --set=ON_ERROR_STOP=1 --tuples-only --no-align \
    --command="
      SELECT 'games=' || COUNT(*) FROM games;
      SELECT 'game_players=' || COUNT(*) FROM game_players;
      SELECT 'game_events=' || COUNT(*) FROM game_events;
      SELECT 'legacy_game_players=' || COUNT(*)
        FROM game_players
        WHERE (NOT is_ai AND (user_id IS NULL OR player_id <> user_id::text))
          OR (is_ai AND user_id IS NOT NULL);
      SELECT 'legacy_match_players=' || COUNT(*)
        FROM match_players
        WHERE (NOT is_ai AND player_id <> user_id)
          OR (is_ai AND user_id <> '');
      SELECT 'legacy_matches=' || COUNT(*)
        FROM matches
        WHERE (owner_user_id <> '' AND owner_player_id <> owner_user_id)
          OR (winner_user_id <> '' AND winner_player_id <> winner_user_id);
      SELECT 'legacy_rooms=' || COUNT(*)
        FROM rooms
        WHERE owner_user_id <> '' AND owner_player_id <> owner_user_id;
    " >"$report_dir/acceptance-sql.txt"
}

tables=(
  rooms
  matches
  match_players
  games
  game_players
  game_events
  player_identity_aliases
)
backup="$report_dir/game-platform-player-identities.dump"
backup_container="/backup/${report_dir##*/}/${backup##*/}"
pg_dump_args=(--format=custom --no-owner --no-privileges "--file=${backup_container}")
for table in "${tables[@]}"; do
  pg_dump_args+=("--table=public.${table}")
done
"${compose[@]}" run --rm --no-deps --entrypoint pg_dump \
  normalize-player-identities "${pg_dump_args[@]}" "$POSTGRES_DSN"
test -s "$backup"
sha256sum "$backup" >"$backup.sha256"
"${compose[@]}" run --rm --no-deps --entrypoint pg_restore \
  normalize-player-identities --list "$backup_container" \
  >"$report_dir/backup-contents.txt"

run_migration "$report_dir/dry-run-live.json" --dry-run
jq -e '
  .conflicts == 0 and
  .unknownReferences == 0 and
  .activeLegacyRooms == 0 and
  (.failedGameIDs | length) == 0
' "$report_dir/dry-run-live.json" >/dev/null

migratable="$(jq -r '.migratableGames' "$report_dir/dry-run-live.json")"
mutable_rows="$(jq -r '.mutableRows' "$report_dir/dry-run-live.json")"
if [[ "$migratable" -eq 0 && "$mutable_rows" -eq 0 ]]; then
  run_migration "$report_dir/verify.json" --verify-only
  jq -e '.remainingCompositeIDs == 0 and .replayV1Games == 0' \
    "$report_dir/verify.json" >/dev/null
  run_acceptance
  completed=1
  printf 'No migration was needed. Reports: %s\n' "$report_dir"
  exit 0
fi

printf '%s\n' "${services[@]}" >"$report_dir/stopped-services.txt"
"${compose[@]}" stop "${services[@]}"
mapfile -t legacy_containers < <(
  docker ps -q \
    --filter label=com.docker.compose.project=game-platform \
    --filter label=com.docker.compose.service=roomhost
)
if [[ "${#legacy_containers[@]}" -gt 0 ]]; then
  printf '%s\n' "${legacy_containers[@]}" >"$report_dir/stopped-legacy-containers.txt"
  docker stop "${legacy_containers[@]}"
  legacy_stopped=1
fi
maintenance=1

run_migration "$report_dir/dry-run-stopped.json" --dry-run
jq -e --argjson expected_games "$migratable" --argjson expected_rows "$mutable_rows" '
  .migratableGames == $expected_games and
  .mutableRows == $expected_rows and
  .conflicts == 0 and
  .unknownReferences == 0 and
  .activeLegacyRooms == 0 and
  (.failedGameIDs | length) == 0
' "$report_dir/dry-run-stopped.json" >/dev/null

run_migration "$report_dir/apply.json"
jq -e --argjson expected "$migratable" '
  .migratedGames == $expected and
  .remainingCompositeIDs == 0 and
  .replayV1Games == 0 and
  (.failedGameIDs | length) == 0
' "$report_dir/apply.json" >/dev/null

run_migration "$report_dir/verify.json" --verify-only
jq -e '.remainingCompositeIDs == 0 and .replayV1Games == 0' \
  "$report_dir/verify.json" >/dev/null
run_acceptance

"${compose[@]}" up -d --no-deps "${services[@]}"
if [[ "$legacy_stopped" -eq 1 ]]; then
  docker start "${legacy_containers[@]}"
  legacy_stopped=0
fi
maintenance=0
completed=1
printf 'Player identity migration completed. Reports: %s\n' "$report_dir"
