package acquire

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestUserIDFromPlayerIDSupportsPlatformAndLegacyFormats(t *testing.T) {
	tests := []struct {
		playerID string
		want     string
		ok       bool
	}{
		{playerID: "137", want: "137", ok: true},
		{playerID: "player@example.com-137", want: "137", ok: true},
		{playerID: "ai_1"},
		{playerID: "player"},
		{playerID: "player-0"},
	}
	for _, tt := range tests {
		t.Run(tt.playerID, func(t *testing.T) {
			got, ok := userIDFromPlayerID(tt.playerID)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("userIDFromPlayerID(%q) = (%q, %v), want (%q, %v)",
					tt.playerID, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestProjectHistoryLinksNumericPlatformPlayerIDs(t *testing.T) {
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
	userIDs := map[string]string{}
	for _, player := range projection.Players {
		userIDs[player.PlayerID] = player.UserID
	}
	if userIDs["137"] != "137" || userIDs["204"] != "204" {
		t.Fatalf("history user IDs = %+v", userIDs)
	}
}
