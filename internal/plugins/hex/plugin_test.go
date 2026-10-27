package hex

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestRoundAssignmentAndViews(t *testing.T) {
	first := startPreparedMatch(t, 40, 1)
	if first.RedPlayerID != "owner" || first.CurrentPlayer != "owner" {
		t.Fatalf("round one assignment = %+v", first)
	}
	second := startPreparedMatch(t, 40, 2)
	if second.RedPlayerID != "ai_2" || second.CurrentPlayer != "ai_2" {
		t.Fatalf("round two assignment = %+v", second)
	}

	viewAny, err := (&Plugin{}).BuildPlayerView(
		context.Background(),
		first,
		game.ViewRequest{PlayerID: "owner", MatchID: "match", Seq: 7},
	)
	if err != nil {
		t.Fatal(err)
	}
	room := viewAny.(map[string]any)["roomData"].(map[string]any)
	if len(room["legalMoves"].([]Cell)) != boardSize*boardSize {
		t.Fatalf("legal moves = %d", len(room["legalMoves"].([]Cell)))
	}
	spectatorAny, err := (&Plugin{}).BuildSpectatorView(
		context.Background(),
		first,
		8,
	)
	if err != nil {
		t.Fatal(err)
	}
	spectator := spectatorAny.(map[string]any)["roomData"].(map[string]any)
	if len(spectator["legalMoves"].([]Cell)) != 0 {
		t.Fatal("spectator received interactive moves")
	}
}

func TestRejectedCommandsAreAtomic(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []contracts.RoomCommand{
		{
			Type: CommandPlaceStone, PlayerID: "ai_2",
			Payload: map[string]any{"cell": Cell{Row: 5, Column: 5}},
		},
		{
			Type: CommandPlaceStone, PlayerID: "owner",
			Payload: map[string]any{"cell": Cell{Row: -1, Column: 5}},
		},
		{Type: CommandSwap, PlayerID: "owner"},
	} {
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
			t.Fatalf("state changed after rejection:\n%s\n%s", before, after)
		}
	}
}

func TestTimeoutAndAIProduceLegalActions(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	updateTurnDeadline(state, now)
	pending, err := (&Plugin{}).PendingCommands(context.Background(), state)
	if err != nil || len(pending) != 1 ||
		pending[0].ExecuteAt != now.Add(turnTimeout) {
		t.Fatalf("pending = %+v, err = %v", pending, err)
	}
	result, err := (&Plugin{}).ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type: CommandTimeout, PlayerID: "owner",
			SentAt: now.Add(turnTimeout),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastAction == nil || !state.LastAction.Automatic ||
		state.LastAction.Kind != "place" ||
		len(result.AIRequests) != 1 {
		t.Fatalf("timeout result = %+v / %+v", state.LastAction, result)
	}
	command, err := (&Plugin{}).Decide(
		context.Background(),
		result.AIRequests[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	if command.Type != CommandPlaceStone && command.Type != CommandSwap {
		t.Fatalf("AI command = %+v", command)
	}
}

func startPreparedMatch(t *testing.T, seed uint64, roundNo int) *State {
	t.Helper()
	raw, err := (&Plugin{}).InitRoom(context.Background(), game.RoomConfig{
		RoomID: "hex-room", OwnerPlayerID: "owner",
		MaxPlayers: 2, RandomSeed: seed, RoundNo: roundNo,
		Visibility: contracts.VisibilityPublic,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*State)
	commands := []contracts.RoomCommand{
		{Type: "connect", PlayerID: "owner"},
		{
			Type: "match_add_ai", PlayerID: "owner",
			Payload: map[string]any{"seatIndex": 1},
		},
		{Type: "match_begin", PlayerID: "owner"},
		{
			Type: "game_ready", PlayerID: "owner",
			SentAt: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC),
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
