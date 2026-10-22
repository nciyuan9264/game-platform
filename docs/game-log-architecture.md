# Game Log Architecture

Game Platform needs two log families. They serve different read paths and
should not be collapsed into one table or one stream.

## 1. Operation Log

Operation log is the semantic game timeline.

Writers:

- RoomHost records accepted Match commands.
- Game plugins emit semantic events through `game.LogSink`.

Readers:

- Live spectators.
- Replay service.
- Admin tools.
- Audit/export jobs.

Properties:

- Strictly ordered by `(matchID, seq)`.
- Carries `commandID`, `playerID`, `visibility`, `type`, and game-specific
  payload.
- May have public and private events. Spectator/replay readers must filter by
  visibility and viewer permissions.
- Stored in an append-only stream first; projected to query storage later.

Recommended storage:

- NATS JetStream for hot ordered delivery.
- Postgres projection for query/admin/history.
- Object storage for archived large replay files if needed.

## 2. State Snapshot

Snapshot is a point-in-time encoded state.

Writers:

- RoomHost saves periodic snapshots.
- Game plugins can request snapshots through `SaveSnapshotHint`.

Readers:

- RoomHost recovery.
- Reconnect fallback when reliable message backfill is too old.
- Replay seek.

Properties:

- Indexed by `roomID`, `matchID`, `seq`, `epoch`, and `engineVersion`.
- Encoded by the plugin through `game.Engine.EncodeState`.
- Decoded by the same game/version during recovery.
- Not a replacement for operation log. Snapshot gives a fast base state;
  operation log replays from that base to a target seq.

## Plugin API

Plugins should not import storage or transport packages. They receive logging
capability through context:

```go
sink := gamelog.FromContext(ctx)
_ = sink.AppendOperation(ctx, game.OperationEvent{
    Type:       "tile_placed",
    PlayerID:   cmd.PlayerID,
    Visibility: game.LogPublic,
    Payload:    map[string]any{"tile": "3A"},
})
```

Snapshot hints are advisory:

```go
_ = sink.SaveSnapshotHint(ctx, game.SnapshotHint{
    Reason: "round_end",
    Force:  true,
})
```

RoomHost decides when and where to persist the actual snapshot.

## Replay Read Model

Replay should load:

```text
latest snapshot at or before target seq
  + operation log entries after snapshot seq up to target seq
```

Live spectators must not subscribe to the operation stream directly. RoomHost
builds a full read-only spectator view after each committed command and writes
it to `spectator_snapshots`. PostgreSQL sets `available_at` from its own clock;
Platform API only serves snapshots after the fixed five-minute delay.

## Current Implementation Status

- Command log exists in JetStream as `cmdlog.<matchID>`.
- Match snapshots exist in Postgres `room_snapshots` keyed by `(match_id, seq)`.
- `GET /platform/command-log?matchID=<matchID>` exposes applied command log for
  inspection.
- `game.LogSink` and `gamelog.WithSink` define the plugin-facing logging
  boundary.
- RoomHost now collects plugin semantic events through `game.LogSink`, embeds
  them in the same fenced command-log record, and idempotently projects them to
  PostgreSQL `game_operation_events`.
- Projection failures are retried from the command log, so plugin events do not
  depend on a best-effort PostgreSQL write in the command hot path.
- `spectator_snapshots` and `spectator_sessions` provide the isolated delayed
  spectator read path without weakening participant-only replay authorization.
