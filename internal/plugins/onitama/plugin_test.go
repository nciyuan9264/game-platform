package onitama

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestRoundAlternatesSidesAndTransitMarkStarts(t *testing.T) {
	first := startPreparedMatch(t, 9264, 1)
	if first.BluePlayerID != "owner" ||
		first.RedPlayerID != "ai_2" ||
		len(first.DrawnCardIDs) != 5 {
		t.Fatalf("round one = %+v", first)
	}
	transit, _ := cardByID(first.TransitCardID)
	if sideForPlayer(first, first.FirstPlayerID) != transit.Mark ||
		first.CurrentPlayer != first.FirstPlayerID {
		t.Fatalf("first player = %+v, transit = %+v", first, transit)
	}
	second := startPreparedMatch(t, 9264, 2)
	if second.BluePlayerID != "ai_2" ||
		second.RedPlayerID != "owner" {
		t.Fatalf("round two = %+v", second)
	}
	if reflect.DeepEqual(first.DrawnCardIDs, second.DrawnCardIDs) {
		t.Fatal("next round did not redeal")
	}
}

func TestViewsExposeMovesOnlyToCurrentPlayer(t *testing.T) {
	state := startPreparedMatch(t, 9264, 1)
	plugin := &Plugin{}
	currentAny, err := plugin.BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{
			MatchID:  "onitama-r1",
			PlayerID: state.CurrentPlayer,
			Seq:      7,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	current := currentAny.(map[string]any)
	roomData := current["roomData"].(map[string]any)
	if len(roomData["legalMoves"].([]LegalMove)) == 0 ||
		current["seq"] != int64(7) {
		t.Fatalf("current view = %+v", current)
	}
	otherPlayer := playerForSide(
		state,
		other(sideForPlayer(state, state.CurrentPlayer)),
	)
	otherAny, err := plugin.BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{PlayerID: otherPlayer},
	)
	if err != nil {
		t.Fatal(err)
	}
	otherData := otherAny.(map[string]any)["roomData"].(map[string]any)
	if len(otherData["legalMoves"].([]LegalMove)) != 0 ||
		otherData["canPass"].(bool) {
		t.Fatalf("opponent view is interactive: %+v", otherData)
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
	if len(spectator["legalMoves"].([]LegalMove)) != 0 ||
		spectator["canPass"].(bool) {
		t.Fatalf("spectator view is interactive: %+v", spectator)
	}
}

func TestRejectedCommandDoesNotMutateState(t *testing.T) {
	state := startPreparedMatch(t, 9264, 1)
	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []contracts.RoomCommand{
		{
			Type:     CommandMovePiece,
			PlayerID: playerForSide(state, other(currentSide(state))),
			Payload: map[string]any{
				"cardID":  state.Hands[currentSide(state)][0],
				"pieceID": string(currentSide(state)) + "-master",
				"to":      Cell{Row: 2, Column: 2},
			},
		},
		{
			Type:     CommandMovePiece,
			PlayerID: state.CurrentPlayer,
			Payload: map[string]any{
				"cardID":  "missing",
				"pieceID": string(currentSide(state)) + "-master",
				"to":      Cell{Row: 2, Column: 2},
			},
		},
		{
			Type:     CommandTimeout,
			PlayerID: state.CurrentPlayer,
			ConnectionRef: contracts.ConnectionRef{
				SessionID:   "session",
				GatewayNode: "gateway",
			},
		},
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
			t.Fatalf("state changed:\n%s\n%s", before, after)
		}
	}
}

func TestTimeoutAIAndCodecAreRecoverable(t *testing.T) {
	state := startPreparedMatchWithHumanTurn(t)
	sentAt := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	updateTurnDeadline(state, sentAt)
	plugin := &Plugin{}
	pending, err := plugin.PendingCommands(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 ||
		!pending[0].ExecuteAt.Equal(sentAt.Add(turnTimeout)) {
		t.Fatalf("pending = %+v", pending)
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
	if state.Status == StatusPlaying &&
		state.CurrentPlayer == "ai_2" {
		if len(result.AIRequests) != 1 ||
			result.AIRequests[0].Timeout != aiTimeout {
			t.Fatalf("AI request = %+v", result.AIRequests)
		}
		command, decideErr := plugin.Decide(
			context.Background(),
			result.AIRequests[0],
		)
		if decideErr != nil {
			t.Fatal(decideErr)
		}
		if _, applyErr := plugin.ApplyCommand(
			context.Background(),
			state,
			command,
		); applyErr != nil {
			t.Fatal(applyErr)
		}
	}
	encoded, err := plugin.EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := plugin.DecodeState(context.Background(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, decoded) {
		t.Fatalf("codec changed state:\n%+v\n%+v", state, decoded)
	}
}

func TestMetadataProgressAndCorruption(t *testing.T) {
	state := startPreparedMatch(t, 9264, 1)
	plugin := &Plugin{}
	metadata, err := plugin.RoomMetadata(context.Background(), state)
	if err != nil ||
		metadata.TurnTimeoutSeconds != 60 ||
		metadata.Visibility != contracts.VisibilityPublic {
		t.Fatalf("metadata = %+v/%v", metadata, err)
	}
	progress, err := plugin.Progress(context.Background(), state)
	if err != nil || progress.Current != 0 || progress.Total != 0 {
		t.Fatalf("progress = %+v/%v", progress, err)
	}
	encoded, _ := json.Marshal(state)
	var corrupt State
	if err := json.Unmarshal(encoded, &corrupt); err != nil {
		t.Fatal(err)
	}
	corrupt.Hands[SideBlue][0] = corrupt.TransitCardID
	data, _ := json.Marshal(corrupt)
	if _, err := plugin.DecodeState(
		context.Background(),
		data,
	); err == nil {
		t.Fatal("corrupt duplicate card was accepted")
	}
}

func startPreparedMatch(
	t *testing.T,
	seed uint64,
	roundNo int,
) *State {
	t.Helper()
	plugin := &Plugin{}
	raw, err := plugin.InitRoom(context.Background(), game.RoomConfig{
		RoomID:        "onitama-room",
		OwnerPlayerID: "owner",
		MaxPlayers:    2,
		RandomSeed:    seed,
		RoundNo:       roundNo,
		Visibility:    contracts.VisibilityPublic,
	})
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
			SentAt: time.Date(
				2026,
				10,
				27,
				9,
				0,
				0,
				0,
				time.UTC,
			),
		},
	}
	for _, command := range commands {
		if _, err := plugin.ApplyCommand(
			context.Background(),
			state,
			command,
		); err != nil {
			t.Fatalf("%s: %v", command.Type, err)
		}
	}
	return state
}

func startPreparedMatchWithHumanTurn(t *testing.T) *State {
	t.Helper()
	for seed := uint64(1); seed < 1000; seed++ {
		state := startPreparedMatch(t, seed, 1)
		if state.CurrentPlayer == "owner" {
			return state
		}
	}
	t.Fatal("could not find deterministic human-first seed")
	return nil
}
