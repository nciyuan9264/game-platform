package splendor

import (
	"context"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform-server/internal/contracts"
	"github.com/nciyuan9264/game-platform-server/internal/game"
	"github.com/nciyuan9264/game-platform-server/internal/splendor/domain"
	"github.com/nciyuan9264/game-platform-server/internal/splendor/entities"
)

func TestPluginLifecycleAndHistory(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	raw, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID: "splendor-test", OwnerUserID: "7", OwnerPlayerID: "7",
		MaxPlayers: 4, RandomSeed: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*domain.GameState)
	apply := func(playerID, command string, payload any) {
		t.Helper()
		_, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
			RoomID: "splendor-test", PlayerID: playerID, Type: command,
			Payload: payload, SentAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
	}
	apply("7", "connect", nil)
	apply("7", "match_add_ai", map[string]any{"seatIndex": 1})
	apply("7", "match_begin", nil)
	apply("7", "game_ready", nil)
	apply("ai_2", "game_ready", nil)
	if state.RoomStatus != domain.RoomStatusPlaying || state.CurrentPlayer == "" {
		t.Fatalf("state = %+v", state)
	}
	projection, err := plugin.ProjectHistory(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Started || projection.MaxPlayers != 4 || len(projection.Players) != 2 {
		t.Fatalf("projection = %+v", projection)
	}
	if len(projection.InitialState) == 0 || len(projection.StateBlob) == 0 {
		t.Fatal("history snapshots were not generated")
	}
}

func TestPluginSnapshotDoesNotExposeHiddenCards(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	raw, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID: "splendor-view", OwnerPlayerID: "7", MaxPlayers: 4, RandomSeed: 84,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*domain.GameState)
	for _, command := range []contracts.RoomCommand{
		{PlayerID: "7", Type: "connect"},
		{PlayerID: "7", Type: "match_add_ai", Payload: map[string]any{"seatIndex": 1}},
		{PlayerID: "7", Type: "match_begin"},
		{PlayerID: "7", Type: "game_ready"},
		{PlayerID: "ai_2", Type: "game_ready"},
	} {
		command.SentAt = time.Now().UTC()
		if _, err := plugin.ApplyCommand(ctx, state, command); err != nil {
			t.Fatal(err)
		}
	}
	view, err := plugin.BuildPlayerView(ctx, state, game.ViewRequest{
		RoomID: state.RoomID, MatchID: "match-1", PlayerID: "7", Seq: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	roomData := view.(map[string]any)["roomData"].(map[string]any)
	cards := roomData["card"].(map[string][]entities.NormalCard)
	visible := 0
	for _, level := range cards {
		visible += len(level)
	}
	if visible != 12 {
		t.Fatalf("visible cards = %d, want 12", visible)
	}
}
