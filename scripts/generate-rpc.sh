#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKSPACE="$(cd "$ROOT/.." && pwd)"
PAM_IDL_ROOT="${PAM_IDL_ROOT:-$WORKSPACE/pam-idl}"
KITEX_BIN="${KITEX_BIN:-$WORKSPACE/.tools/bin/kitex}"

if [[ ! -x "$KITEX_BIN" ]]; then
  echo "kitex binary not found: $KITEX_BIN" >&2
  exit 1
fi

cd "$ROOT"
rm -rf kitex_gen/auth kitex_gen/platformcontrol kitex_gen/pam/auth kitex_gen/pam/platform/control
for relative_idl in auth/v1/service.thrift game_platform_control/v1/service.thrift; do
  idl="$PAM_IDL_ROOT/idl/$relative_idl"
  if [[ ! -f "$idl" ]]; then
    echo "PAM IDL not found: $idl" >&2
    exit 1
  fi
  "$KITEX_BIN" -module github.com/nciyuan9264/game-platform-server -I "$PAM_IDL_ROOT/idl" "$idl"
done
gofmt -w kitex_gen/pam/auth/v1 kitex_gen/pam/platform/control/v1
