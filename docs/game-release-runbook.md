# Game Release Runbook

## Invariants

- `game_catalog` is desired state; Redis heartbeat is observed state.
- Promotion is compare-and-swap and changes only new-room traffic.
- A node with active rooms is never stopped.
- Engine version is compatibility; image SHA is artifact identity.
- Online replay reads never load a legacy game plugin.

## Status

```bash
game-release status --game acquire --json | jq .
```

Confirm the active catalog version, both slot node IDs, status, advertised
versions, artifact version, and active room counts.

## Normal Engine Release

1. Change `EngineVersion` for rule/state behavior changes.
2. Build and push `game-platform-roomhost-<game>:<git-sha>`.
3. Run:

```bash
GAME_RELEASE_IMAGE=<registry>/game-platform-game-release:<git-sha> \
  ./scripts/deploy-game-slot.sh \
  <game> \
  <registry>/game-platform-roomhost-<game>:<git-sha>
```

The script self-tests the image, chooses an empty A/B slot, waits for stable
ready heartbeats, promotes with CAS, drains the old slot, and stops it only at
zero active rooms.

## First Game Registration

After a new game's RoomHost slot advertises ready, register its catalog entry
once. For Love Letter:

```bash
game-release register \
  --game loveletter \
  --version loveletter-v1 \
  --default-max-players 6 \
  --replay-schema-version 1 \
  --artifact <loveletter-image-sha>
```

6 nimmt! 首次注册使用固定 10 席与 replay schema 1：

```bash
game-release register \
  --game sixnimmt \
  --version sixnimmt-v1 \
  --default-max-players 10 \
  --replay-schema-version 1 \
  --artifact <sixnimmt-image-sha>
```

Registration is a production operation. It is not part of build verification
or ordinary source delivery.

## First Onitama Registration

After an `onitama-v1` RoomHost slot advertises ready, register the catalog
entry once:

```bash
game-release register \
  --game onitama \
  --version onitama-v1 \
  --default-max-players 2 \
  --replay-schema-version 1 \
  --artifact <onitama-image-sha>
```

Run this only during the production rollout, after center services support the
Onitama DTOs and before the frontend entry is published.

## Compatible Runtime Fix

Keep `EngineVersion` unchanged only when state encoding, deterministic
behavior, and replay shape remain compatible. Supply an explicit
`compatibility_ack` to the manual deploy workflow. The slot script skips
catalog promotion and uses drain/resume for the artifact rollout.

## Rollback

For a manual catalog rollback:

```bash
game-release rollback \
  --game acquire \
  --version acquire-v2 \
  --expected-version acquire-v3 \
  --artifact <old-image-sha>
```

Then send `SIGUSR2` to the old compatible slot. Send `SIGUSR1` to the candidate
to stop new rooms. Do not stop the candidate while it has active v3 rooms.

Rollback does not migrate or terminate rooms. Existing rooms remain pinned to
their exact engine version.

## Slot Exhaustion

If A and B both have active rooms, abort the release. Do not reuse either slot
and do not end rooms. Record the blocking versions and room counts, then wait
for normal lifecycle cleanup. A third live engine version is unsupported.

## Long Drain

A long-running old slot remains `draining` and continues commands, lease
renewal, and compatible recovery. Alert on its room count. The deployment is
not allowed to send `SIGTERM` until the count is zero.

## Candidate Failure

- Before promotion: remove the candidate if it has no active rooms.
- After promotion: CAS rollback to the old version, drain the candidate, and
  resume the old node.
- If the candidate already owns rooms: retain it draining until those rooms
  finish.

## Replay Backfill

Run one explicit engine mapping at a time:

```bash
go run ./cmd/backfill-replay \
  --game-type acquire \
  --engine-version acquire-v2 \
  --replay-schema-version 1 \
  --batch-size 100
```

The tool fills only empty payloads and compares normalized legacy and
materialized JSON before each write. Unknown engine versions fail; they are
never guessed.

Do not cut or retain materialized-only reads unless both remaining counters are
zero for every replayable game.

## Replay Failure

- `replay_materialization_missing`: run or resume the explicit backfill.
- `replay_schema_unsupported`: deploy a center reader that supports the stored
  schema before promoting that game version.
- `replay_error`: quarantine the affected game ID and inspect the compressed
  payload; online code must not fall back to a plugin.
