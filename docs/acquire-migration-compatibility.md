# Acquire Migration Compatibility

The source project under `../game` is migration input only and must remain
unchanged.

## Commands

| Command | Platform support |
| --- | --- |
| `connect` / `disconnect` | RoomHost actor + Acquire player metadata |
| `match_ready` | Supported |
| `match_begin` | Supported, 2–6 players |
| `match_add_ai` | Supported, owner only |
| `match_remove_player` | Supported with targeted error + connection close |
| `game_ready` | Supported |
| `game_place_tile` | Original rule implementation migrated |
| `game_create_company` | Original rule implementation migrated |
| `game_merging_selection` | Original rule implementation migrated |
| `game_merging_settle` | Original rule implementation migrated |
| `game_buy_stock` | Original rule implementation migrated |
| `turn_timeout` | RoomHost scheduled command |
| `game_play_audio` | RoomHost transient broadcast effect |
| `game_restart_game` | Supported without creating a second runtime |

## State And Views

- `MATCH_SYNC` retains owner, player, online, AI, and ready fields.
- `ROOM_SYNC` retains:
  - private `playerData`;
  - company information;
  - board tiles;
  - current player and game status;
  - turn deadline and timeout;
  - merge temporary data;
  - final result.
- Random tile draws use a serialized deterministic RNG seed/counter so
  snapshot recovery and command replay cannot diverge.

## AI

- Heuristic policy migrated.
- Runtime search, simulator, evaluator, and fixed online weights migrated.
- AI delay is requested by the plugin and executed by RoomHost.
- Search failure falls back to `turn_timeout`.

## Timers And Lifecycle

- Human turn deadlines are stored in plugin state.
- RoomHost owns timers and validates timer generation/key.
- Restore re-arms the pending deadline.
- No-human cleanup closes the platform room rather than deleting historical
  metadata.
- Daily cleanup uses a runtime close command.

## History And Replay

- `games`, `game_players`, and `game_events` are the completed history/read
  model.
- Every recordable command stores a compressed authoritative state blob.
- New replay reads state blobs directly.
- Imported records fall back to legacy JSON snapshots.
- Stats and average-rank leaderboard retain the original response contract.
- `scripts/migrate-acquire-history.sh` copies old data without modifying it.

## Frontend

- Existing Acquire components, styles, sounds, and 2D/3D assets are reused in
  `game-platform-game-frontend`.
- Routes:
  - `/game/acquire`
  - `/game/acquire/match`
  - `/game/acquire/replay/:gameId`
- Room APIs use `/platform/rooms`.
- History/ranking APIs use `/platform/history` and `/platform/rankings`.
- WebSocket uses `/ws` and the Platform API login cookie/token.

## Tooling

- Go load runner and k6 scripts are copied under `loadtest/acquire`.
- Loadtest helper APIs use the normal Control Plane scheduler and are guarded
  by `ACQUIRE_LOADTEST_ENABLED` plus `ACQUIRE_LOADTEST_TOKEN`.
- WS load tests require real Auth access tokens via
  `ACQUIRE_ACCESS_TOKENS`.

## Verification Mapping

| Source test family | Target |
| --- | --- |
| `domain/data` | `internal/acquire/domain/data` |
| `domain/game` | `internal/acquire/rules` |
| `domain/roompkg/ai*` | `internal/acquire/ai` |
| snapshot codec | rules + plugin round trip |
| RoomService actor tests | plugin + RoomHost tests |
| replay tests | history state blob/API tests |
| AI search/evaluator tests | `internal/acquire/ai` |
| load runner tests | `loadtest/acquire/runner` |
