package plugins

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestRegistryContainsAllGamePlugins(t *testing.T) {
	registry := Registry()
	for _, gameType := range []string{
		contracts.GameAcquire,
		contracts.GameDaVinci,
		contracts.GameSplendor,
		contracts.GameYinsh,
		contracts.GameOthello,
		contracts.GameFugitive,
		contracts.GameWarChest,
		contracts.GameQuoridor,
	} {
		definition, ok := registry.Get(gameType)
		if !ok || definition.Engine == nil || definition.Version == "" ||
			definition.DefaultMaxPlayers <= 0 {
			t.Fatalf("%s definition = %+v, %v", gameType, definition, ok)
		}
	}
}

func TestAllGamePluginsSupportRoomVisibilitySettings(t *testing.T) {
	registry := Registry()
	for _, gameType := range []string{
		contracts.GameAcquire,
		contracts.GameDaVinci,
		contracts.GameSplendor,
		contracts.GameYinsh,
		contracts.GameOthello,
		contracts.GameFugitive,
		contracts.GameWarChest,
		contracts.GameQuoridor,
	} {
		t.Run(gameType, func(t *testing.T) {
			definition, ok := registry.Get(gameType)
			if !ok {
				t.Fatalf("missing %s definition", gameType)
			}
			state, err := definition.Engine.InitRoom(
				context.Background(),
				game.RoomConfig{
					RoomID:        gameType + "-visibility",
					GameType:      gameType,
					OwnerPlayerID: "owner",
					MaxPlayers:    definition.DefaultMaxPlayers,
					RandomSeed:    1,
					Visibility:    contracts.VisibilityPrivate,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			provider, ok := definition.Engine.(game.RoomMetadataProvider)
			if !ok {
				t.Fatalf("%s engine does not provide room metadata", gameType)
			}
			metadata, err := provider.RoomMetadata(context.Background(), state)
			if err != nil {
				t.Fatal(err)
			}
			if metadata.Visibility != contracts.VisibilityPrivate {
				t.Fatalf("initial visibility = %q, want private", metadata.Visibility)
			}

			if _, err := definition.Engine.ApplyCommand(
				context.Background(),
				state,
				contracts.RoomCommand{
					Type:     "connect",
					PlayerID: "owner",
				},
			); err != nil {
				t.Fatal(err)
			}
			if _, err := definition.Engine.ApplyCommand(
				context.Background(),
				state,
				contracts.RoomCommand{
					Type:     "connect",
					PlayerID: "guest",
				},
			); err != nil {
				t.Fatal(err)
			}
			if _, err := definition.Engine.ApplyCommand(
				context.Background(),
				state,
				contracts.RoomCommand{
					Type:     "room_update_settings",
					PlayerID: "guest",
					Payload: map[string]any{
						"visibility": contracts.VisibilityPublic,
					},
				},
			); err == nil {
				t.Fatal("non-owner updated room visibility")
			}
			if _, err := definition.Engine.ApplyCommand(
				context.Background(),
				state,
				contracts.RoomCommand{
					Type:     "room_update_settings",
					PlayerID: "owner",
					Payload: map[string]any{
						"visibility": contracts.VisibilityPublic,
					},
				},
			); err != nil {
				t.Fatal(err)
			}
			metadata, err = provider.RoomMetadata(context.Background(), state)
			if err != nil {
				t.Fatal(err)
			}
			if metadata.Visibility != contracts.VisibilityPublic {
				t.Fatalf("updated visibility = %q, want public", metadata.Visibility)
			}
		})
	}
}
