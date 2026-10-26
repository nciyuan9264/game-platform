package acquire

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestProjectHistoryDoesNotInferUserIDsFromPlayerIDs(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "numeric-history",
		OwnerPlayerID: "137",
		MaxPlayers:    2,
		RandomSeed:    testRandomSeed,
	})
	if err != nil {
		t.Fatal(err)
	}
	apply := func(playerID, commandType string, payload any) {
		t.Helper()
		if _, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
			PlayerID: playerID,
			Type:     commandType,
			Payload:  payload,
		}); err != nil {
			t.Fatalf("ApplyCommand(%s) error = %v", commandType, err)
		}
	}
	apply("137", "connect", nil)
	apply("204", "connect", nil)
	apply("204", "match_ready", map[string]any{"ready": true})
	apply("137", "match_begin", nil)
	apply("137", "game_ready", nil)

	projection, err := plugin.ProjectHistory(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Players) != 2 {
		t.Fatalf("history players = %d, want 2", len(projection.Players))
	}
	for _, player := range projection.Players {
		if player.UserID != "" {
			t.Fatalf("history player %+v inferred a user ID", player)
		}
	}
}
