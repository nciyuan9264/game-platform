package hive

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestRoundAssignmentAlternatesWhiteSeat(t *testing.T) {
	first := startPreparedMatch(t, 40, 1)
	if first.WhitePlayerID != "owner" ||
		first.BlackPlayerID != "ai_2" ||
		first.FirstPlayerID != "owner" {
		t.Fatalf("round one = %+v", first)
	}
	second := startPreparedMatch(t, 40, 2)
	if second.WhitePlayerID != "ai_2" ||
		second.BlackPlayerID != "owner" ||
		second.FirstPlayerID != "ai_2" {
		t.Fatalf("round two = %+v", second)
	}
}

func TestViewsExposeLegalActionsOnlyToCurrentPlayer(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	plugin := &Plugin{}
	currentAny, err := plugin.BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{
			MatchID:  "hive-room-r1",
			PlayerID: "owner",
			Seq:      7,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	current := currentAny.(map[string]any)
	roomData := current["roomData"].(map[string]any)
	if current["playerId"] != "owner" ||
		current["seq"] != int64(7) ||
		len(roomData["legalActions"].([]map[string]any)) != 4 {
		t.Fatalf("current view = %+v", current)
	}
	nonCurrentAny, err := plugin.BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{PlayerID: "ai_2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	nonCurrent := nonCurrentAny.(map[string]any)["roomData"].(map[string]any)
	if len(nonCurrent["legalActions"].([]map[string]any)) != 0 {
		t.Fatal("non-current player received legal actions")
	}
	spectatorAny, err := plugin.BuildSpectatorView(
		context.Background(),
		state,
		9,
	)
	if err != nil {
		t.Fatal(err)
	}
	spectator := spectatorAny.(map[string]any)["roomData"].(map[string]any)
	if len(spectator["legalActions"].([]map[string]any)) != 0 {
		t.Fatal("spectator received legal actions")
	}
}

func TestRejectedCommandsDoNotMutateState(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	commands := []contracts.RoomCommand{
		{
			Type:     CommandPlacePiece,
			PlayerID: "ai_2",
			Payload: map[string]any{
				"pieceID": "bA1",
				"to":      Hex{},
			},
		},
		{
			Type:     CommandPlacePiece,
			PlayerID: "owner",
			Payload: map[string]any{
				"pieceID": "wQ1",
				"to":      Hex{},
			},
		},
		{
			Type:     CommandMovePiece,
			PlayerID: "owner",
			Payload: map[string]any{
				"pieceID": "wA1",
				"to":      Hex{Q: 100},
			},
		},
	}
	for _, command := range commands {
		if _, applyErr := (&Plugin{}).ApplyCommand(
			context.Background(),
			state,
			command,
		); applyErr == nil {
			t.Fatalf("command accepted: %+v", command)
		}
		after, marshalErr := json.Marshal(state)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("state changed:\n%s\n%s", before, after)
		}
	}
}

func TestHumanTimeoutAndAIRequest(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	sentAt := time.Date(2026, 10, 27, 13, 0, 0, 0, time.UTC)
	updateTurnDeadline(state, sentAt)
	plugin := &Plugin{}
	pending, err := plugin.PendingCommands(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 ||
		pending[0].Type != CommandTimeout ||
		!pending[0].ExecuteAt.Equal(sentAt.Add(turnTimeout)) {
		t.Fatalf("pending = %+v", pending)
	}
	if _, err := plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type:     CommandTimeout,
			PlayerID: "owner",
			ConnectionRef: contracts.ConnectionRef{
				SessionID:   "session",
				GatewayNode: "gateway",
			},
		},
	); err == nil {
		t.Fatal("client timeout was accepted")
	}
	result, err := plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type:     CommandTimeout,
			PlayerID: "owner",
			SentAt:   sentAt.Add(turnTimeout),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastAction == nil ||
		!state.LastAction.Automatic ||
		state.LastAction.PlayerID != "owner" {
		t.Fatalf("timeout action = %+v", state.LastAction)
	}
	if len(result.AIRequests) != 1 ||
		result.AIRequests[0].PlayerID != "ai_2" {
		t.Fatalf("AI request = %+v", result.AIRequests)
	}
}

func TestStateCodecAndForcedPassScheduler(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	plugin := &Plugin{}
	encoded, err := plugin.EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := plugin.DecodeState(context.Background(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, decoded) {
		t.Fatal("state codec changed the state")
	}
	pass := passState()
	pending, err := plugin.PendingCommands(context.Background(), pass)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 ||
		pending[0].Type != CommandForcedPass ||
		pending[0].PlayerID != "white" {
		t.Fatalf("forced pass pending = %+v", pending)
	}
}

func startPreparedMatch(t *testing.T, seed uint64, round int) *State {
	t.Helper()
	raw, err := (&Plugin{}).InitRoom(
		context.Background(),
		game.RoomConfig{
			RoomID:        "hive-room",
			OwnerPlayerID: "owner",
			MaxPlayers:    2,
			RandomSeed:    seed,
			RoundNo:       round,
			Visibility:    contracts.VisibilityPublic,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*State)
	commands := []contracts.RoomCommand{
		{Type: "connect", PlayerID: "owner"},
		{
			Type:     "match_add_ai",
			PlayerID: "owner",
			Payload:  map[string]any{"seatIndex": 1},
		},
		{Type: "match_begin", PlayerID: "owner"},
		{
			Type:     "game_ready",
			PlayerID: "owner",
			SentAt:   time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC),
		},
	}
	for _, command := range commands {
		if _, err := (&Plugin{}).ApplyCommand(
			context.Background(),
			state,
			command,
		); err != nil {
			t.Fatalf("%s: %v", command.Type, err)
		}
	}
	return state
}
