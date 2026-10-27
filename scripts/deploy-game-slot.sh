#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  printf 'usage: %s <acquire|davinci|splendor|yinsh|othello|fugitive|warchest|quoridor|hex|loveletter|sixnimmt> <immutable-image>\n' "$0" >&2
  exit 2
fi

game="$1"
target_image="$2"
case "$game" in
  acquire)
    image_prefix='ACQUIRE_ROOMHOST'
    default_max_players=6
    replay_schema_version=2
    ;;
  davinci)
    image_prefix='DAVINCI_ROOMHOST'
    default_max_players=2
    replay_schema_version=1
    ;;
  splendor)
    image_prefix='SPLENDOR_ROOMHOST'
    default_max_players=4
    replay_schema_version=2
    ;;
  yinsh)
    image_prefix='YINSH_ROOMHOST'
    default_max_players=2
    replay_schema_version=1
    ;;
  othello)
    image_prefix='OTHELLO_ROOMHOST'
    default_max_players=2
    replay_schema_version=1
    ;;
  fugitive)
    image_prefix='FUGITIVE_ROOMHOST'
    default_max_players=2
    replay_schema_version=1
    ;;
  warchest)
    image_prefix='WARCHEST_ROOMHOST'
    default_max_players=2
    replay_schema_version=1
    ;;
  quoridor)
    image_prefix='QUORIDOR_ROOMHOST'
    default_max_players=2
    replay_schema_version=1
    ;;
  hex)
    image_prefix='HEX_ROOMHOST'
    default_max_players=2
    replay_schema_version=1
    ;;
  loveletter)
    image_prefix='LOVELETTER_ROOMHOST'
    default_max_players=6
    replay_schema_version=1
    ;;
  sixnimmt)
    image_prefix='SIXNIMMT_ROOMHOST'
    default_max_players=10
    replay_schema_version=1
    ;;
  *)
    printf 'unsupported game: %s\n' "$game" >&2
    exit 2
    ;;
esac
if [[ ! "$target_image" =~ @sha256:[[:xdigit:]]{64}$ &&
  ! "$target_image" =~ :[^/:]+$ ]]; then
  printf 'target image must be an immutable full reference: %s\n' "$target_image" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"
ENV_FILE="${ENV_FILE:-.env}"
GAME_RELEASE_BIN="${GAME_RELEASE_BIN:-game-release}"
GAME_RELEASE_IMAGE="${GAME_RELEASE_IMAGE:-}"
DRAIN_TIMEOUT_SECONDS="${DRAIN_TIMEOUT_SECONDS:-60}"

if [[ -f "$ENV_FILE" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi
for slot_name in A B; do
  image_name="${image_prefix}_${slot_name}_IMAGE"
  version_name="${image_prefix}_${slot_name}_VERSION"
  if [[ -z "${!image_name:-}" ]]; then
    printf -v "$image_name" '%s' "$target_image"
  fi
  if [[ -z "${!version_name:-}" ]]; then
    printf -v "$version_name" '%s' "$target_image"
  fi
  export "$image_name" "$version_name"
done
command -v jq >/dev/null
if [[ -z "$GAME_RELEASE_IMAGE" ]]; then
  command -v "$GAME_RELEASE_BIN" >/dev/null
fi

compose() {
  docker compose -f "$COMPOSE_FILE" "$@"
}

release_status() {
  if ! game_release status --game "$game" --json 2>/dev/null; then
    game_release status --json
  fi
}

game_release() {
  if [[ -n "$GAME_RELEASE_IMAGE" ]]; then
    docker run --rm \
      --network global-network \
      --env-file "$ENV_FILE" \
      "$GAME_RELEASE_IMAGE" \
      "$@"
    return
  fi
  "$GAME_RELEASE_BIN" "$@"
}

service_running() {
  compose ps --services --status running | grep -Fxq "$1"
}

node_active_rooms() {
  local status_json="$1"
  local node_id="$2"
  jq -r --arg node "$node_id" \
    '[.nodes[] | select(.nodeID == $node) | .activeRooms][0] // 0' \
    <<<"$status_json"
}

wait_for_candidate() {
  local node_id="$1"
  local consecutive=0
  local status_json
  for _ in $(seq 1 60); do
    status_json="$(release_status)"
    if jq -e --arg node "$node_id" --arg game "$game" '
      any(
        .nodes[];
        .nodeID == $node and
        .status == "ready" and
        (.gameVersions[$game] // "") != ""
      )
    ' <<<"$status_json" >/dev/null; then
      consecutive=$((consecutive + 1))
      if [[ "$consecutive" -ge 3 ]]; then
        jq -r --arg node "$node_id" --arg game "$game" '
          .nodes[] |
          select(.nodeID == $node) |
          .gameVersions[$game]
        ' <<<"$status_json"
        return 0
      fi
    else
      consecutive=0
    fi
    sleep 2
  done
  return 1
}

persist_image_reference() {
  local variable="$1"
  local value="$2"
  local temporary
  temporary="$(mktemp "${ENV_FILE}.XXXXXX")"
  if [[ -f "$ENV_FILE" ]]; then
    awk -v key="$variable" -v value="$value" '
      BEGIN { found = 0 }
      index($0, key "=") == 1 {
        print key "=" value
        found = 1
        next
      }
      { print }
      END {
        if (!found) {
          print key "=" value
        }
      }
    ' "$ENV_FILE" >"$temporary"
  else
    printf '%s=%s\n' "$variable" "$value" >"$temporary"
  fi
  mv "$temporary" "$ENV_FILE"
}

status_json="$(release_status)"
active_version="$(
  jq -r --arg game "$game" '
    .catalog[] |
    select(.gameType == $game) |
    .activeEngineVersion
  ' <<<"$status_json" |
    head -n 1
)"

candidate=''
for slot in a b; do
  service="roomhost-${game}-${slot}"
  if ! service_running "$service"; then
    candidate="$service"
    break
  fi
  if [[ "$(node_active_rooms "$status_json" "$service")" -eq 0 ]]; then
    candidate="$service"
    break
  fi
done
if [[ -z "$candidate" ]]; then
  printf '%s A/B slots both have active rooms; refusing a third live version\n' "$game" >&2
  exit 1
fi

slot="${candidate##*-}"
image_variable="${image_prefix}_${slot^^}_IMAGE"
version_variable="${image_prefix}_${slot^^}_VERSION"
export "${image_variable}=${target_image}"
export "${version_variable}=${target_image}"

printf 'self-testing %s with %s\n' "$candidate" "$target_image"
compose run --rm --no-deps "$candidate" self-test

if service_running "$candidate"; then
  compose kill -s SIGUSR1 "$candidate"
  for _ in $(seq 1 30); do
    status_json="$(release_status)"
    if [[ "$(node_active_rooms "$status_json" "$candidate")" -eq 0 ]]; then
      break
    fi
    sleep 2
  done
  if [[ "$(node_active_rooms "$status_json" "$candidate")" -ne 0 ]]; then
    printf 'candidate slot %s did not drain before replacement\n' "$candidate" >&2
    exit 1
  fi
  compose stop "$candidate"
  compose rm -f "$candidate"
fi

promotion_complete=0
registration_complete=0
rollback_on_error() {
  local exit_code=$?
  trap - ERR
  if [[ "$registration_complete" -eq 1 ]]; then
    printf '%s was registered; leaving its only ready candidate running\n' \
      "$game" >&2
  elif [[ "$promotion_complete" -eq 1 ]]; then
    for old_slot in a b; do
      old_service="roomhost-${game}-${old_slot}"
      if [[ "$old_service" != "$candidate" ]] && service_running "$old_service"; then
        compose kill -s SIGUSR2 "$old_service" || true
      fi
    done
    if game_release rollback \
      --game "$game" \
      --version "$active_version" \
      --expected-version "$candidate_version" \
      --artifact "$target_image"; then
      compose kill -s SIGUSR1 "$candidate" || true
    else
      printf 'rollback target is not ready; leaving candidate ready\n' >&2
    fi
  else
    compose kill -s SIGUSR1 "$candidate" || true
    status_json="$(release_status 2>/dev/null || true)"
    if [[ -n "$status_json" ]] &&
      [[ "$(node_active_rooms "$status_json" "$candidate")" -eq 0 ]]; then
      compose stop "$candidate" || true
      compose rm -f "$candidate" || true
    else
      printf '%s may have active rooms; leaving it draining after failure\n' \
        "$candidate" >&2
    fi
  fi
  exit "$exit_code"
}
trap rollback_on_error ERR

compose up -d --no-deps "$candidate"
candidate_version="$(wait_for_candidate "$candidate")"
printf '%s is ready with engine %s\n' "$candidate" "$candidate_version"

if [[ -z "$active_version" ]]; then
  game_release register \
    --game "$game" \
    --version "$candidate_version" \
    --default-max-players "$default_max_players" \
    --replay-schema-version "$replay_schema_version" \
    --artifact "$target_image"
  registration_complete=1
elif [[ "$candidate_version" != "$active_version" ]]; then
  game_release promote \
    --game "$game" \
    --version "$candidate_version" \
    --expected-version "$active_version" \
    --artifact "$target_image"
  promotion_complete=1
fi

for old_slot in a b; do
  old_service="roomhost-${game}-${old_slot}"
  if [[ "$old_service" == "$candidate" ]] || ! service_running "$old_service"; then
    continue
  fi
  compose kill -s SIGUSR1 "$old_service"
  deadline=$((SECONDS + DRAIN_TIMEOUT_SECONDS))
  while (( SECONDS < deadline )); do
    status_json="$(release_status)"
    if [[ "$(node_active_rooms "$status_json" "$old_service")" -eq 0 ]]; then
      compose stop "$old_service"
      break
    fi
    sleep 5
  done
  if service_running "$old_service"; then
    printf '%s remains draining with active rooms; retaining the occupied slot\n' \
      "$old_service" >&2
  fi
done

for slot_name in A B; do
  image_name="${image_prefix}_${slot_name}_IMAGE"
  version_name="${image_prefix}_${slot_name}_VERSION"
  persist_image_reference "$image_name" "${!image_name}"
  persist_image_reference "$version_name" "${!version_name}"
done
trap - ERR
printf '%s release complete: %s@%s on %s\n' \
  "$game" "$game" "$candidate_version" "$candidate"
