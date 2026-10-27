#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 ]]; then
  printf 'usage: %s <base-commit> <head-commit> [--compatibility-ack reason]\n' "$0" >&2
  exit 2
fi

base="$1"
head="$2"
shift 2
compatibility_ack=''
if [[ $# -gt 0 ]]; then
  if [[ "$1" != '--compatibility-ack' || $# -ne 2 || -z "$2" ]]; then
    printf 'invalid compatibility acknowledgement\n' >&2
    exit 2
  fi
  compatibility_ack="$2"
fi

declare -A version_files=(
  [acquire]='internal/plugins/acquire/plugin.go'
  [davinci]='internal/plugins/davinci/engine.go'
  [splendor]='internal/plugins/splendor/plugin.go'
  [yinsh]='internal/plugins/yinsh/plugin.go'
  [othello]='internal/plugins/othello/plugin.go'
  [fugitive]='internal/plugins/fugitive/plugin.go'
  [warchest]='internal/plugins/warchest/plugin.go'
  [quoridor]='internal/plugins/quoridor/plugin.go'
  [loveletter]='internal/plugins/loveletter/plugin.go'
)
declare -A changed_games=()
declare -A engine_changes=()

compatibility_only_definition_change() {
  local path="$1"
  ! git diff --unified=0 "$base" "$head" -- "$path" |
    awk '
      /^@@/ {
        replay_hunk = ($0 ~ /ProjectHistory/ || $0 ~ /BuildReplaySnapshot/ || $0 ~ /DecodeReplayState/)
        next
      }
      (substr($0, 1, 1) == "+" || substr($0, 1, 1) == "-") &&
      substr($0, 1, 3) != "+++" &&
      substr($0, 1, 3) != "---" &&
      $0 !~ /^[+-][[:space:]]*$/ &&
      !replay_hunk &&
      $0 !~ /LegacyReplay/ &&
      $0 !~ /ReplaySchemaVersion/ {
        print
      }
    ' |
    grep -q .
}

while IFS= read -r path; do
  [[ -z "$path" ]] && continue
  case "$path" in
    internal/plugins/acquire/testdata/replay_frame_golden.json)
      changed_games[acquire]=1
      continue
      ;;
    internal/plugins/splendor/testdata/replay_frame_golden.json)
      changed_games[splendor]=1
      continue
      ;;
    internal/plugins/yinsh/testdata/replay_frame_golden.json)
      changed_games[yinsh]=1
      continue
      ;;
    internal/plugins/othello/testdata/replay_frame_golden.json)
      changed_games[othello]=1
      continue
      ;;
    internal/plugins/warchest/testdata/replay_frame_golden.json)
      changed_games[warchest]=1
      continue
      ;;
    internal/plugins/quoridor/testdata/replay_frame_golden.json)
      changed_games[quoridor]=1
      continue
      ;;
    internal/plugins/loveletter/testdata/replay_frame_golden.json)
      changed_games[loveletter]=1
      continue
      ;;
    internal/plugins/acquire/history.go)
      changed_games[acquire]=1
      continue
      ;;
    internal/plugins/splendor/history.go)
      changed_games[splendor]=1
      continue
      ;;
    internal/plugins/yinsh/history.go)
      changed_games[yinsh]=1
      continue
      ;;
    internal/plugins/othello/history.go)
      changed_games[othello]=1
      continue
      ;;
    internal/plugins/warchest/history.go)
      changed_games[warchest]=1
      continue
      ;;
    internal/plugins/quoridor/history.go)
      changed_games[quoridor]=1
      continue
      ;;
    *_test.go|*/testdata/*)
      continue
      ;;
    internal/plugins/acquire/*)
      changed_games[acquire]=1
      if [[ "$path" == "${version_files[acquire]}" ]] &&
        compatibility_only_definition_change "$path"; then
        continue
      fi
      engine_changes[acquire]=1
      ;;
    internal/plugins/davinci/*)
      changed_games[davinci]=1
      engine_changes[davinci]=1
      ;;
    internal/plugins/splendor/*)
      changed_games[splendor]=1
      if [[ "$path" == "${version_files[splendor]}" ]] &&
        compatibility_only_definition_change "$path"; then
        continue
      fi
      engine_changes[splendor]=1
      ;;
    internal/plugins/yinsh/*)
      changed_games[yinsh]=1
      if [[ "$path" == "${version_files[yinsh]}" ]] &&
        compatibility_only_definition_change "$path"; then
        continue
      fi
      engine_changes[yinsh]=1
      ;;
    internal/plugins/othello/*)
      changed_games[othello]=1
      if [[ "$path" == "${version_files[othello]}" ]] &&
        compatibility_only_definition_change "$path"; then
        continue
      fi
      engine_changes[othello]=1
      ;;
    internal/plugins/fugitive/*)
      changed_games[fugitive]=1
      engine_changes[fugitive]=1
      ;;
    internal/plugins/warchest/*)
      changed_games[warchest]=1
      if [[ "$path" == "${version_files[warchest]}" ]] &&
        compatibility_only_definition_change "$path"; then
        continue
      fi
      engine_changes[warchest]=1
      ;;
    internal/plugins/quoridor/*)
      changed_games[quoridor]=1
      if [[ "$path" == "${version_files[quoridor]}" ]] &&
        compatibility_only_definition_change "$path"; then
        continue
      fi
      engine_changes[quoridor]=1
      ;;
    internal/plugins/loveletter/*)
      changed_games[loveletter]=1
      if [[ "$path" == "${version_files[loveletter]}" ]] &&
        compatibility_only_definition_change "$path"; then
        continue
      fi
      engine_changes[loveletter]=1
      ;;
  esac
done < <(git diff --name-only "$base" "$head")

constant_at() {
  local ref="$1"
  local file="$2"
  local name="$3"
  local content
  if ! content="$(git show "${ref}:${file}" 2>/dev/null)"; then
    return 0
  fi
  printf '%s\n' "$content" |
    sed -nE "s/^[[:space:]]*(const[[:space:]]+)?${name}[[:space:]]*=[[:space:]]*\"([^\"]+)\".*/\\2/p" |
    head -n 1
}

integer_constant_at() {
  local ref="$1"
  local file="$2"
  local name="$3"
  local content
  if ! content="$(git show "${ref}:${file}" 2>/dev/null)"; then
    return 0
  fi
  printf '%s\n' "$content" |
    sed -nE "s/^[[:space:]]*(const[[:space:]]+)?${name}[[:space:]]*=[[:space:]]*([0-9]+).*/\\2/p" |
    head -n 1
}

failures=()
validated=()
for game_name in acquire davinci splendor yinsh othello fugitive warchest quoridor loveletter; do
  [[ -n "${changed_games[$game_name]:-}" ]] || continue
  file="${version_files[$game_name]}"
  old_version="$(constant_at "$base" "$file" EngineVersion)"
  new_version="$(constant_at "$head" "$file" EngineVersion)"
  if [[ -n "${engine_changes[$game_name]:-}" && -z "$new_version" ]]; then
    failures+=("${game_name}: EngineVersion is missing")
    continue
  fi
  if [[ -n "${engine_changes[$game_name]:-}" &&
    "$old_version" == "$new_version" &&
    -z "$compatibility_ack" ]]; then
    failures+=("${game_name}: implementation changed without an EngineVersion bump")
  fi

  fixture="internal/plugins/${game_name}/testdata/replay_frame_golden.json"
  if git diff --name-only "$base" "$head" -- "$fixture" | grep -q .; then
    old_schema="$(integer_constant_at "$base" "$file" ReplaySchemaVersion)"
    new_schema="$(integer_constant_at "$head" "$file" ReplaySchemaVersion)"
    if [[ -z "$new_schema" || "$old_schema" == "$new_schema" ]]; then
      failures+=("${game_name}: replay fixture changed without a ReplaySchemaVersion bump")
    fi
  fi
  validated+=("$game_name")
done

if [[ "${#failures[@]}" -gt 0 ]]; then
  printf '%s\n' "${failures[@]}" >&2
  exit 1
fi

validated_json="$(
  printf '%s\n' "${validated[@]:-}" |
    jq -Rsc 'split("\n") | map(select(length > 0))'
)"
jq -cn \
  --argjson games "$validated_json" \
  --arg compatibilityAck "$compatibility_ack" \
  '{
    games: $games,
    compatibilityAck: (
      if $compatibilityAck == "" then null else $compatibilityAck end
    )
  }'
