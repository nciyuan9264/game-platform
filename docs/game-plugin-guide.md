# Game Plugin Guide

Games use compile-time Go interfaces, but production packages each game in an
independent RoomHost image. The shared runtime stays in `internal/roomhost` and
`internal/roomhostapp`; a production entrypoint imports exactly one game.

## Contract

A game provides a `game.Definition`:

```go
func Definition() game.Definition {
	plugin := NewPlugin()
	return game.Definition{
		GameType:          "my-game",
		Version:           "my-game-v1",
		DefaultMaxPlayers: 4,
		Engine:            plugin,
		AI:                plugin,
		History:           plugin,
		LegacyReplay:      plugin,
	}
}
```

`game.Engine` must implement initialization, command application, state codec,
and player view generation. Games with hidden randomness must reject a missing
private seed and persist the seed/counter needed for deterministic recovery.

`History` is the online write projection. It emits:

- `ReplaySchemaVersion`;
- a replay frame containing only `roomData`, `playersData`, and `result`;
- legacy state blobs retained for audit and migration.

`LegacyReplay` exists only for `cmd/backfill-replay`. Center services must never
load it.

## Production Entrypoint

Add one command:

```go
func main() {
	if err := roomhostapp.Run(mygame.Definition()); err != nil {
		log.Fatal(err)
	}
}
```

Add `roomhost-mygame` to the image matrix and A/B Compose slots. Do not add the
new game to Control Plane or Platform RPC. `internal/plugins` is only the local
all-games and backfill registry.

## Version Rules

`Definition.Version` is an engine compatibility contract, not an image tag.

- Change it when deterministic behavior, state encoding, recovery semantics,
  or rules change.
- Keep it unchanged for a compatible RoomHost infrastructure fix.
- Use the immutable Git SHA image tag to identify the artifact.
- Change `ReplaySchemaVersion` when the materialized replay frame shape changes.

`scripts/validate-engine-version-bump.sh` enforces these rules. A compatible
implementation-only change requires an explicit reason through
`--compatibility-ack`; a replay golden fixture change still requires a schema
version bump.

## Catalog Registration

Start the candidate RoomHost before changing desired state:

```bash
game-release register \
  --game my-game \
  --version my-game-v1 \
  --default-max-players 4 \
  --replay-schema-version 1 \
  --artifact registry.example/game-platform-roomhost-mygame:<git-sha>
```

For an existing game, switch new room traffic with CAS:

```bash
game-release promote \
  --game my-game \
  --version my-game-v2 \
  --expected-version my-game-v1 \
  --artifact registry.example/game-platform-roomhost-mygame:<git-sha>
```

The catalog is desired state. Redis RoomHost heartbeats are observed state.
Room creation requires both an enabled active catalog entry and a ready node
advertising the exact version.

## Required Tests

Each game must cover:

- initial state and invalid command handling;
- state codec round trip and deterministic replay;
- private view filtering and hidden-randomness non-disclosure;
- exact engine-version recovery;
- materialized replay frame generation;
- replay golden fixture compatibility;
- the single-game RoomHost registry containing only its definition.

Operational release and rollback steps are in
[`game-release-runbook.md`](game-release-runbook.md).
