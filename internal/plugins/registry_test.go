package plugins

import (
	"context"
	"regexp"
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

func TestAllGamePluginsFollowAIIdentityContract(t *testing.T) {
	ctx := context.Background()
	canonicalID := regexp.MustCompile(`^ai_[0-9]{3}$`)
	namePools := map[string][]string{
		contracts.GameAcquire:    {"摩根", "诺兰", "薇拉", "里德", "艾弗里"},
		contracts.GameCantStop:   {"小满", "阿橙", "松子", "米拉"},
		contracts.GameDaVinci:    {"谜钥"},
		contracts.GameForSale:    {"小满", "阿橙", "松子", "米拉", "可可"},
		contracts.GameFugitive:   {"灰狐"},
		contracts.GameHex:        {"青岸"},
		contracts.GameHive:       {"行舟"},
		contracts.GameLoveLetter: {"艾琳", "奥利弗", "莉娅", "诺亚", "苏菲", "米娅"},
		contracts.GameOnitama:    {"弈心"},
		contracts.GameOthello:    {"墨白"},
		contracts.GameQuoridor:   {"行舟"},
		contracts.GameSixNimmt: {
			"阿麦", "栗子", "小野", "北北", "松果",
			"团团", "南风", "阿丘", "豆子", "小满",
		},
		contracts.GameSplendor: {"琥珀", "翡翠", "青金"},
		contracts.GameWarChest: {"渡鸦"},
		contracts.GameYinsh:    {"墨白"},
	}
	for _, definition := range Registry().Definitions() {
		t.Run(definition.GameType, func(t *testing.T) {
			names := namePools[definition.GameType]
			if len(names) < definition.DefaultMaxPlayers-1 {
				t.Fatalf(
					"name pool has %d entries, want at least %d",
					len(names),
					definition.DefaultMaxPlayers-1,
				)
			}
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

			provider, ok := definition.Engine.(game.RosterProvider)
			if !ok {
				t.Fatalf("%s engine does not provide a roster", definition.GameType)
			}
			apply := func(commandType string, payload map[string]any) {
				t.Helper()
				if _, err := definition.Engine.ApplyCommand(ctx, state, contracts.RoomCommand{
					Type:     commandType,
					UserID:   "owner-user",
					PlayerID: "owner",
					Payload:  payload,
				}); err != nil {
					t.Fatalf("%s: %v", commandType, err)
				}
			}
			aiRoster := func() map[string]game.RosterPlayer {
				t.Helper()
				roster, err := provider.Roster(ctx, state)
				if err != nil {
					t.Fatal(err)
				}
				result := map[string]game.RosterPlayer{}
				for _, player := range roster {
					if player.IsAI {
						result[player.PlayerID] = player
					}
				}
				return result
			}
			assertAI := func(playerID, displayName string, seatIndex int) {
				t.Helper()
				player, ok := aiRoster()[playerID]
				if !ok {
					t.Fatalf("AI %s is missing", playerID)
				}
				if !canonicalID.MatchString(player.PlayerID) {
					t.Fatalf("AI player ID = %q, want canonical three-digit ID", player.PlayerID)
				}
				if player.DisplayName != displayName ||
					player.DisplayName == player.PlayerID ||
					player.DisplayName == "AI" {
					t.Fatalf("AI %s display name = %q, want %q", playerID, player.DisplayName, displayName)
				}
				if player.SeatIndex != seatIndex {
					t.Fatalf("AI %s seat = %d, want %d", playerID, player.SeatIndex, seatIndex)
				}
			}

			lastSeat := definition.DefaultMaxPlayers - 1
			apply("match_add_ai", map[string]any{"seatIndex": lastSeat})
			assertAI("ai_001", names[0], lastSeat)

			if definition.DefaultMaxPlayers == 2 {
				apply("match_remove_player", map[string]any{"playerID": "ai_001"})
				apply("match_add_ai", map[string]any{"seatIndex": lastSeat})
				assertAI("ai_001", names[0], lastSeat)
				return
			}

			apply("match_add_ai", map[string]any{"seatIndex": 1})
			assertAI("ai_002", names[1], 1)

			apply("match_remove_player", map[string]any{"playerID": "ai_002"})
			apply("match_add_ai", map[string]any{"seatIndex": 2})
			assertAI("ai_002", names[1], 2)

			apply("match_remove_player", map[string]any{"playerID": "ai_001"})
			apply("match_add_ai", map[string]any{"seatIndex": lastSeat})
			assertAI("ai_001", names[0], lastSeat)

			finalRoster := aiRoster()
			if len(finalRoster) != 2 ||
				finalRoster["ai_001"].DisplayName == finalRoster["ai_002"].DisplayName {
				t.Fatalf("final AI roster does not have unique stable identities: %+v", finalRoster)
			}
		})
	}
}
