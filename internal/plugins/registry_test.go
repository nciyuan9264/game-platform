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
		contracts.GameHex,
		contracts.GameLoveLetter,
		contracts.GameSixNimmt,
		contracts.GameForSale,
		contracts.GameOnitama,
		contracts.GameHive,
		contracts.GameCantStop,
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
		contracts.GameHex,
		contracts.GameLoveLetter,
		contracts.GameSixNimmt,
		contracts.GameForSale,
		contracts.GameOnitama,
		contracts.GameHive,
		contracts.GameCantStop,
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

func TestAllGamePluginsPersistDistinctAINames(t *testing.T) {
	ctx := context.Background()
	for _, definition := range Registry().Definitions() {
		t.Run(definition.GameType, func(t *testing.T) {
			state, err := definition.Engine.InitRoom(ctx, game.RoomConfig{
				RoomID:        definition.GameType + "-ai-names",
				GameType:      definition.GameType,
				OwnerUserID:   "owner-user",
				OwnerPlayerID: "owner",
				MaxPlayers:    definition.DefaultMaxPlayers,
				RandomSeed:    1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := definition.Engine.ApplyCommand(ctx, state, contracts.RoomCommand{
				Type:     "connect",
				UserID:   "owner-user",
				PlayerID: "owner",
			}); err != nil {
				t.Fatal(err)
			}

			expectedAI := 2
			if definition.DefaultMaxPlayers < 3 {
				expectedAI = 1
			}
			for range expectedAI {
				if _, err := definition.Engine.ApplyCommand(ctx, state, contracts.RoomCommand{
					Type:     "match_add_ai",
					UserID:   "owner-user",
					PlayerID: "owner",
					Payload:  map[string]any{},
				}); err != nil {
					t.Fatal(err)
				}
			}

			provider, ok := definition.Engine.(game.RosterProvider)
			if !ok {
				t.Fatalf("%s engine does not provide a roster", definition.GameType)
			}
			roster, err := provider.Roster(ctx, state)
			if err != nil {
				t.Fatal(err)
			}
			names := make(map[string]string, expectedAI)
			for _, player := range roster {
				if !player.IsAI {
					continue
				}
				if player.DisplayName == "" || player.DisplayName == "AI" {
					t.Fatalf("AI %q has placeholder display name %q", player.PlayerID, player.DisplayName)
				}
				if previousID := names[player.DisplayName]; previousID != "" {
					t.Fatalf(
						"AI players %q and %q share display name %q",
						previousID,
						player.PlayerID,
						player.DisplayName,
					)
				}
				names[player.DisplayName] = player.PlayerID
			}
			if len(names) != expectedAI {
				t.Fatalf("AI names = %v, want %d distinct names", names, expectedAI)
			}
		})
	}
}
