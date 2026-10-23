# Legacy game-backend Synchronization Audit

## Scope

This audit compares `/game/game-backend` with `game-platform` without modifying
or migrating the legacy worktree.

- Commit range reviewed: 2026-10-15 through 2026-10-23.
- Legacy commits reviewed: `8253eb6`, `86c77ef`, `352be32`, `70d2635`,
  `a2c1a64`, `23ebd88`, and `ede24a1`.
- Legacy dirty worktree reviewed: 14 tracked changes and 12 untracked Splendor
  source or test files.
- Platform areas reviewed: game plugins and rules, RoomHost, gateway, control
  plane, Postgres store, history RPC, and realtime transport.

No legacy files were changed by this audit.

## Status Definitions

| Status | Meaning |
| --- | --- |
| `DIRECT` | The behavior or rule is present as directly corresponding code. |
| `EQUIVALENT` | The platform architecture provides the same capability through a different implementation. |
| `MISSING` | A required behavior or meaningful regression coverage is absent. |
| `INTENTIONAL` | Legacy infrastructure should not be copied into the platform. |
| `NEEDS_DECISION` | Both implementations are valid candidates but expose different semantics. |

## Commit-Level Matrix

| Legacy change | Status | Platform evidence | Risk / recommendation |
| --- | --- | --- | --- |
| `8253eb6` Acquire authenticated rooms, room protocol, replay pagination, snapshots, and visibility | `EQUIVALENT` | `internal/rpc/room.go`, `internal/gateway/server.go`, `internal/roomhost/host.go`, `internal/store/history_store.go`, `internal/acquire/plugin.go` | The platform owns these concerns centrally. Do not copy Gin routers, per-game WebSocket ownership, or replay services. Address the two command/projection gaps listed below. |
| `86c77ef` lightweight auth principal parsing | `EQUIVALENT` | PAM Gateway authenticates users and forwards trusted identity metadata; RPC middleware consumes that identity. | Keep auth parsing outside game plugins. |
| `352be32` leaderboard sort parameter | `EQUIVALENT` | Generic history RPC/store performs authorized paged history and leaderboard reads. | Preserve backend-before-pagination sorting and explicit invalid-sort rejection in future changes. |
| `70d2635` DaVinci reliable room protocol | `EQUIVALENT` | RoomHost command ACKs, durable command log, authoritative sequence, scheduled commands, reconnect snapshots, and source validation. | Do not restore process-local protocol state. Platform tests should remain the contract owner. |
| `a2c1a64` legacy Splendor preview origins | `INTENTIONAL` | Public routing and CORS belong to Kong/Gateway and deployment configuration. | Do not copy service-local origin exceptions. |
| `23ebd88` disable legacy deployment | `INTENTIONAL` | Confirms the legacy service is no longer the production runtime. | Retain the repository only as migration evidence until audit findings are resolved. |
| `ede24a1` legacy production database alignment | `INTENTIONAL` | `game-platform` has its own RDS configuration and validation workflow. | Do not copy legacy compose or database bootstrap wiring. |

## Dirty Splendor Worktree Matrix

| Legacy evidence | Status | Platform evidence | Notes |
| --- | --- | --- | --- |
| `domain/game/action_payload.go` | `DIRECT` | `internal/splendor/rules/action_payload.go` | Diff is limited to package and repository import paths. |
| `domain/game/validation.go` | `DIRECT` | `internal/splendor/rules/validation.go` | Diff is limited to package and repository import paths. |
| `domain/game/step1.go` | `DIRECT` | `internal/splendor/rules/step1.go` | Atomic gem take/return, reserve payload, and reserved-card purchase behavior match after package normalization. |
| Room state, deck counts, hidden-card protection, player order, visibility fields | `DIRECT` | `internal/splendor/domain/types.go`, `internal/splendor/plugin.go`, `internal/splendor/catalog` | Core state fields and public-view filtering are present. |
| `roompkg/protocol.go`, request IDs, ACKs, phase/turn guards | `EQUIVALENT` | `internal/roomhost/host.go`, `internal/transport/nats.go`, authoritative `seq` | Platform sequencing replaces legacy `phaseVersion` and `turnNumber` as the transport concurrency contract. |
| Process-local timers and AI timeout commands | `EQUIVALENT` | RoomHost scheduled command generation/key and persisted command flow | Keep scheduling centralized in RoomHost. |
| `snapshot.go`, replay engine, paged history service | `EQUIVALENT` | generic snapshots, `ListSnapshots`, `HistoryReplayPageForUser`, state blobs | Do not migrate per-game replay infrastructure. |
| Gin room router, private lookup, room list, owner quota | `EQUIVALENT` | control/RPC room APIs and `ReserveRoomCreation` | Platform performs global authorization and quota enforcement. |
| Legacy rule/protocol/AI/replay/router tests | `MISSING` | Platform has plugin and rule tests, but not the complete dirty-worktree matrix | Port behavior-focused cases, not legacy HTTP/process infrastructure tests. |

## Confirmed Differences

### Noble ordering

- Status: `NEEDS_DECISION`
- Legacy: `internal/games/splendor/domain/game/send_message.go` sorts noble IDs
  by string length and then lexically, producing `N1, N2, N10`.
- Platform: `internal/splendor/plugin.go::revealedNobles` sorts lexically,
  producing `N1, N10, N2`.
- Risk: deterministic snapshots differ across runtimes and tests.
- Recommendation: choose a canonical display order and add a platform test.
  Natural numeric order is the lower-surprise option.

### AI reserve at the ten-token limit

- Status: `NEEDS_DECISION`
- Legacy: dirty `internal/games/splendor/domain/roompkg/ai.go` returns the newly
  granted Gold token.
- Platform: `internal/splendor/plugin.go` selects an existing ordinary token
  to return before reserving.
- Risk: AI move selection and resulting bank state differ.
- Recommendation: decide whether AI should minimize state change by returning
  Gold or optimize strategically; then lock the behavior with an AI test.

### Duplicate identity connections

- Status: `INTENTIONAL`
- Legacy: rejects a second connection for the same player with
  `identity_conflict`.
- Platform: supports multiple gateway sessions and reconnect replacement while
  validating command source identity.
- Risk: mechanically porting the old rejection would break multi-tab/reconnect
  semantics.
- Recommendation: retain platform behavior and document the session policy.

## Missing Platform Behavior

### Acquire room settings command

- Status: `MISSING`
- Frontend evidence:
  `game-frontend/src/features/acquire/pages/Room/index.tsx` submits
  `room_update_settings`.
- Platform evidence: `internal/acquire/plugin.go` has no handler for that
  command, while `internal/splendor/plugin.go` does.
- Risk: the Acquire owner can submit a visibility change that the game plugin
  does not apply.
- Priority: **P0**. Add the command through the plugin contract and tests.

### Splendor visibility persistence

- Status: `MISSING`
- Platform evidence: `internal/splendor/plugin.go` updates in-memory
  `GameState.Visibility`; RoomHost continues broadcasting
  `RoomRecord.Visibility` and does not project the plugin change back to
  `rooms.visibility`.
- Risk: lobby visibility and recovered state can disagree with the live room.
- Priority: **P0**. Define a plugin-to-RoomHost room metadata effect and persist
  it with owner fencing.

### Regression coverage parity

- Status: `MISSING`
- Missing groups include natural noble ordering, AI full-token reserve,
  structured protocol rejection, mixed replay pages, and room visibility
  projection.
- Priority: **P1**. Port only behavior-level cases after the two P0 contracts
  are resolved.

## Recommended Follow-Up Order

1. **P0**: implement and persist room visibility through one shared metadata
   contract; cover both Acquire and Splendor.
2. **P0**: align the Acquire frontend command with the Acquire plugin handler.
3. **P1**: decide noble ordering and AI Gold-return semantics.
4. **P1**: port the relevant legacy regression tests into platform-owned test
   suites.
5. **P2**: document why legacy connection rejection, Gin routes, service-local
   auth, timers, and replay infrastructure are intentionally not migrated.

## Conclusion

The platform already contains the core Splendor rules and provides architectural
equivalents for the legacy control, realtime, scheduling, and replay layers.
The audit does not support a bulk code migration. The actionable remainder is
two room-visibility contract gaps, two Splendor semantic decisions, and targeted
test parity.
