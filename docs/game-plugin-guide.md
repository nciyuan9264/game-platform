# Game Plugin Guide

This platform uses compile-time Go plugins: each game implements the stable
`internal/game` interfaces and is registered in `internal/plugins`.

## Contract

A game plugin must provide a `game.Definition`:

```go
func Definition() game.Definition {
	return game.Definition{
		GameType:          "my-game",
		Version:           "my-game-v1",
		DefaultMaxPlayers: 4,
		Engine:            NewPlugin(),
		AI:                NewAIPlanner(), // optional
	}
}
```

The engine must implement:

- `InitRoom`: create initial room state from the supplied private
  `RoomConfig.RandomSeed`; reject a missing seed for games with hidden
  randomness.
- `ApplyCommand`: validate and apply one room command.
- `EncodeState` / `DecodeState`: persist and restore snapshots.
- `BuildPlayerView`: return the private view for a single player.
- `GameType` / `Version`: identify the game and snapshot format.

Optional AI support is provided by `game.AIPlanner`. The planner returns a
normal `contracts.RoomCommand`; RoomHost applies that command through the same
engine path as human actions.

## Registration

Add the game package to `internal/plugins/registry.go`:

```go
func Registry() *game.Registry {
	return game.NewRegistry(
		davinci.Definition(),
		mygame.Definition(),
	)
}
```

Control Plane, RoomHost, Gateway, recovery, snapshots, command logs, and
broadcast delivery do not need game-specific changes after the definition is
registered.

## Required Tests

Each game plugin should include focused tests for:

- Initial state created from `game.RoomConfig`.
- Valid and invalid command handling.
- Snapshot encode/decode round trips.
- Player-private view filtering.
- Different private seeds produce different hidden outcomes, while replaying
  the same seed and counter reproduces the same state.
- Public object IDs and player/spectator views do not reveal hidden values or
  the random seed.
- Recovery replay compatibility when a snapshot is restored and later commands
  are replayed.

The platform-level tests in `internal/game`, `internal/control`, and
`internal/roomhost` verify that definitions can be registered, rooms are
created through the plugin interface, command logs can be inspected, and
RoomHost recovery replays commands after the latest snapshot.
