#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT/scripts/validate-engine-version-bump.sh"
repo="$(mktemp -d)"
trap 'rm -rf "$repo"' EXIT

cd "$repo"
git init -q
git config user.name test
git config user.email test@example.com
mkdir -p internal/acquire/rules internal/acquire/testdata
cat >internal/acquire/plugin.go <<'EOF'
package acquire
const (
	EngineVersion = "acquire-v1"
	ReplaySchemaVersion = 1
)
EOF
printf 'package rules\nconst Value = 1\n' >internal/acquire/rules/rule.go
printf '{"schemaVersion":1}\n' >internal/acquire/testdata/replay_frame_golden.json
git add .
git commit -qm base
base="$(git rev-parse HEAD)"

printf 'package rules\nconst Value = 2\n' >internal/acquire/rules/rule.go
git add .
git commit -qm behavior
behavior="$(git rev-parse HEAD)"
if "$SCRIPT" "$base" "$behavior" >/dev/null 2>&1; then
  printf 'behavior change passed without a version bump\n' >&2
  exit 1
fi
"$SCRIPT" "$base" "$behavior" --compatibility-ack 'verified compatible' >/dev/null

sed -i 's/acquire-v1/acquire-v2/' internal/acquire/plugin.go
git add .
git commit -qm version
versioned="$(git rev-parse HEAD)"
"$SCRIPT" "$base" "$versioned" >/dev/null

printf '{"schemaVersion":2}\n' >internal/acquire/testdata/replay_frame_golden.json
git add .
git commit -qm fixture
fixture="$(git rev-parse HEAD)"
if "$SCRIPT" "$versioned" "$fixture" --compatibility-ack 'verified compatible' >/dev/null 2>&1; then
  printf 'fixture change passed without a replay schema bump\n' >&2
  exit 1
fi

sed -i 's/ReplaySchemaVersion = 1/ReplaySchemaVersion = 2/' internal/acquire/plugin.go
git add .
git commit -qm schema
schema="$(git rev-parse HEAD)"
"$SCRIPT" "$versioned" "$schema" --compatibility-ack 'replay schema only' >/dev/null

new_game_base="$schema"
mkdir -p internal/yinsh/testdata
cat >internal/yinsh/plugin.go <<'EOF'
package yinsh
const (
	EngineVersion = "yinsh-v1"
	ReplaySchemaVersion = 1
)
EOF
printf '{"schemaVersion":1}\n' >internal/yinsh/testdata/replay_frame_golden.json
git add .
git commit -qm new-game
new_game="$(git rev-parse HEAD)"
"$SCRIPT" "$new_game_base" "$new_game" >/dev/null

new_othello_base="$new_game"
mkdir -p internal/othello/testdata
cat >internal/othello/plugin.go <<'EOF'
package othello
const (
	EngineVersion = "othello-v1"
	ReplaySchemaVersion = 1
)
EOF
printf '{"schemaVersion":1}\n' >internal/othello/testdata/replay_frame_golden.json
git add .
git commit -qm new-othello
new_othello="$(git rev-parse HEAD)"
"$SCRIPT" "$new_othello_base" "$new_othello" >/dev/null

new_quoridor_base="$new_othello"
mkdir -p internal/quoridor/testdata
cat >internal/quoridor/plugin.go <<'EOF'
package quoridor
const (
	EngineVersion = "quoridor-v1"
	ReplaySchemaVersion = 1
)
EOF
printf '{"schemaVersion":1}\n' >internal/quoridor/testdata/replay_frame_golden.json
git add .
git commit -qm new-quoridor
new_quoridor="$(git rev-parse HEAD)"
"$SCRIPT" "$new_quoridor_base" "$new_quoridor" >/dev/null

printf 'engine version validation tests passed\n'
