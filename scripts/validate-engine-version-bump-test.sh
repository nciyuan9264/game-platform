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
mkdir -p internal/plugins/acquire/rules internal/plugins/acquire/testdata
cat >internal/plugins/acquire/plugin.go <<'EOF'
package acquire
const (
	EngineVersion = "acquire-v1"
	ReplaySchemaVersion = 1
)
EOF
printf 'package rules\nconst Value = 1\n' >internal/plugins/acquire/rules/rule.go
printf '{"schemaVersion":1}\n' >internal/plugins/acquire/testdata/replay_frame_golden.json
git add .
git commit -qm base
base="$(git rev-parse HEAD)"

printf 'package rules\nconst Value = 2\n' >internal/plugins/acquire/rules/rule.go
git add .
git commit -qm behavior
behavior="$(git rev-parse HEAD)"
if "$SCRIPT" "$base" "$behavior" >/dev/null 2>&1; then
  printf 'behavior change passed without a version bump\n' >&2
  exit 1
fi
"$SCRIPT" "$base" "$behavior" --compatibility-ack 'verified compatible' >/dev/null

sed -i 's/acquire-v1/acquire-v2/' internal/plugins/acquire/plugin.go
git add .
git commit -qm version
versioned="$(git rev-parse HEAD)"
"$SCRIPT" "$base" "$versioned" >/dev/null

printf '{"schemaVersion":2}\n' >internal/plugins/acquire/testdata/replay_frame_golden.json
git add .
git commit -qm fixture
fixture="$(git rev-parse HEAD)"
if "$SCRIPT" "$versioned" "$fixture" --compatibility-ack 'verified compatible' >/dev/null 2>&1; then
  printf 'fixture change passed without a replay schema bump\n' >&2
  exit 1
fi

sed -i 's/ReplaySchemaVersion = 1/ReplaySchemaVersion = 2/' internal/plugins/acquire/plugin.go
git add .
git commit -qm schema
schema="$(git rev-parse HEAD)"
"$SCRIPT" "$versioned" "$schema" --compatibility-ack 'replay schema only' >/dev/null

new_game_base="$schema"
mkdir -p internal/plugins/yinsh/testdata
cat >internal/plugins/yinsh/plugin.go <<'EOF'
package yinsh
const (
	EngineVersion = "yinsh-v1"
	ReplaySchemaVersion = 1
)
EOF
printf '{"schemaVersion":1}\n' >internal/plugins/yinsh/testdata/replay_frame_golden.json
git add .
git commit -qm new-game
new_game="$(git rev-parse HEAD)"
"$SCRIPT" "$new_game_base" "$new_game" >/dev/null

new_othello_base="$new_game"
mkdir -p internal/plugins/othello/testdata
cat >internal/plugins/othello/plugin.go <<'EOF'
package othello
const (
	EngineVersion = "othello-v1"
	ReplaySchemaVersion = 1
)
EOF
printf '{"schemaVersion":1}\n' >internal/plugins/othello/testdata/replay_frame_golden.json
git add .
git commit -qm new-othello
new_othello="$(git rev-parse HEAD)"
"$SCRIPT" "$new_othello_base" "$new_othello" >/dev/null

new_quoridor_base="$new_othello"
mkdir -p internal/plugins/quoridor/testdata
cat >internal/plugins/quoridor/plugin.go <<'EOF'
package quoridor
const (
	EngineVersion = "quoridor-v1"
	ReplaySchemaVersion = 1
)
EOF
printf '{"schemaVersion":1}\n' >internal/plugins/quoridor/testdata/replay_frame_golden.json
git add .
git commit -qm new-quoridor
new_quoridor="$(git rev-parse HEAD)"
"$SCRIPT" "$new_quoridor_base" "$new_quoridor" >/dev/null

new_hex_base="$new_quoridor"
mkdir -p internal/plugins/hex/testdata
cat >internal/plugins/hex/plugin.go <<'EOF'
package hex
const (
	EngineVersion = "hex-v1"
	ReplaySchemaVersion = 1
)
EOF
printf '{"schemaVersion":1}\n' >internal/plugins/hex/testdata/replay_frame_golden.json
git add .
git commit -qm new-hex
new_hex="$(git rev-parse HEAD)"
"$SCRIPT" "$new_hex_base" "$new_hex" >/dev/null

new_loveletter_base="$new_hex"
mkdir -p internal/plugins/loveletter/testdata
cat >internal/plugins/loveletter/plugin.go <<'EOF'
package loveletter
const (
	EngineVersion = "loveletter-v1"
	ReplaySchemaVersion = 1
)
EOF
printf '{"schemaVersion":1}\n' >internal/plugins/loveletter/testdata/replay_frame_golden.json
git add .
git commit -qm new-loveletter
new_loveletter="$(git rev-parse HEAD)"
"$SCRIPT" "$new_loveletter_base" "$new_loveletter" >/dev/null

new_sixnimmt_base="$new_loveletter"
mkdir -p internal/plugins/sixnimmt/testdata
cat >internal/plugins/sixnimmt/plugin.go <<'EOF'
package sixnimmt
const (
	EngineVersion = "sixnimmt-v1"
	ReplaySchemaVersion = 1
)
EOF
printf '{"schemaVersion":1}\n' >internal/plugins/sixnimmt/testdata/replay_frame_golden.json
git add .
git commit -qm new-sixnimmt
new_sixnimmt="$(git rev-parse HEAD)"
"$SCRIPT" "$new_sixnimmt_base" "$new_sixnimmt" >/dev/null

printf 'engine version validation tests passed\n'
