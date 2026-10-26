package quoridor

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestRoundAssignmentUsesSeedAndAlternatesBothPolicies(t *testing.T) {
	first := startPreparedMatch(t, 40, 1)
	if first.SouthPlayerID != "owner" ||
		first.NorthPlayerID != "ai_2" ||
		first.FirstPlayerID != "owner" ||
		first.CurrentPlayer != "owner" {
		t.Fatalf("round one assignment = %+v", first)
	}
	second := startPreparedMatch(t, 40, 2)
	if second.SouthPlayerID != "ai_2" ||
		second.NorthPlayerID != "owner" ||
		second.FirstPlayerID != "ai_2" ||
		second.CurrentPlayer != "ai_2" {
		t.Fatalf("round two assignment = %+v", second)
	}
	independent := startPreparedMatch(t, 42, 1)
	if independent.SouthPlayerID != "owner" ||
		independent.FirstPlayerID != "ai_2" {
		t.Fatalf("independent bits assignment = %+v", independent)
	}
}

func TestViewsExposeActionsOnlyToCurrentPlayer(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	plugin := &Plugin{}
	currentAny, err := plugin.BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{
			MatchID:  "quoridor-room-r1",
			PlayerID: "owner",
			Seq:      7,
			PlayerProfiles: map[string]game.PlayerProfile{
				"owner": {
					UserID:      "100",
					DisplayName: "Alice",
					Avatar:      "https://example.com/alice.png",
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	current := currentAny.(map[string]any)
	roomData := current["roomData"].(map[string]any)
	if current["playerId"] != "owner" ||
		current["seq"] != int64(7) ||
		len(roomData["legalMoves"].([]Cell)) != 3 ||
		len(roomData["wallOptions"].([]WallOption)) != 128 {
		t.Fatalf("current player view = %+v", current)
	}
	players := roomData["players"].(map[string]any)
	owner := players["owner"].(map[string]any)
	if owner["displayName"] != "Alice" ||
		owner["avatar"] != "https://example.com/alice.png" {
		t.Fatalf("owner profile = %+v", owner)
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
	if len(nonCurrent["legalMoves"].([]Cell)) != 0 ||
		len(nonCurrent["wallOptions"].([]WallOption)) != 0 {
		t.Fatalf("non-current actions = %+v/%+v",
			nonCurrent["legalMoves"], nonCurrent["wallOptions"])
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
	if len(spectator["legalMoves"].([]Cell)) != 0 ||
		len(spectator["wallOptions"].([]WallOption)) != 0 {
		t.Fatal("spectator received interactive actions")
	}
}

func TestRejectedCommandDoesNotMutateState(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []contracts.RoomCommand{
		{
			Type:     CommandMovePawn,
			PlayerID: "ai_2",
			Payload: map[string]any{
				"to": map[string]any{"row": 7, "column": 4},
			},
		},
		{
			Type:     CommandMovePawn,
			PlayerID: "owner",
			Payload: map[string]any{
				"to": map[string]any{"row": -1, "column": 4},
			},
		},
		{
			Type:     CommandPlaceWall,
			PlayerID: "owner",
			Payload: map[string]any{
				"wall": map[string]any{
					"row": 8, "column": 0, "orientation": "h",
				},
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
			t.Fatalf("state changed after rejection:\n%s\n%s", before, after)
		}
	}
}

func TestHumanTimeoutAndAIRequestAreRecoverable(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	sentAt := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	updateTurnDeadline(state, sentAt)
	plugin := &Plugin{}
	pending, err := plugin.PendingCommands(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 ||
		pending[0].Type != CommandTimeout ||
		pending[0].PlayerID != "owner" ||
		!pending[0].ExecuteAt.Equal(sentAt.Add(turnTimeout)) {
		t.Fatalf("pending = %+v", pending)
	}
	if _, err := plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type:     CommandTimeout,
			PlayerID: "owner",
			SentAt:   sentAt.Add(turnTimeout),
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
		result.AIRequests[0].PlayerID != "ai_2" ||
		result.AIRequests[0].Delay < aiMinDelay ||
		result.AIRequests[0].Delay > aiMaxDelay ||
		result.AIRequests[0].Timeout != aiTimeout {
		t.Fatalf("AI request = %+v", result.AIRequests)
	}
	command, err := plugin.Decide(
		context.Background(),
		result.AIRequests[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	command.SentAt = sentAt.Add(turnTimeout + result.AIRequests[0].Delay)
	if _, err := plugin.ApplyCommand(
		context.Background(),
		state,
		command,
	); err != nil {
		t.Fatal(err)
	}
	if state.LastAction == nil ||
		!state.LastAction.Automatic ||
		state.LastAction.PlayerID != "ai_2" {
		t.Fatalf("AI action = %+v", state.LastAction)
	}
}

func TestStateCodecRejectsCorruption(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	plugin := &Plugin{}
	encoded, err := plugin.EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	decodedAny, err := plugin.DecodeState(context.Background(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodedAny.(*State)
	if !reflect.DeepEqual(state, decoded) {
		t.Fatalf("round trip changed state:\n%+v\n%+v", state, decoded)
	}
	decoded.WallStock[SideSouth] = 11
	corrupt, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.DecodeState(context.Background(), corrupt); err == nil {
		t.Fatal("corrupt state was accepted")
	}
}

func TestMetadataProgressAndPresence(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	plugin := &Plugin{}
	metadata, err := plugin.RoomMetadata(context.Background(), state)
	if err != nil ||
		metadata.Visibility != contracts.VisibilityPublic ||
		metadata.TurnTimeoutSeconds != 60 {
		t.Fatalf("metadata = %+v, %v", metadata, err)
	}
	progress, err := plugin.Progress(context.Background(), state)
	if err != nil || progress.Current != 0 || progress.Total != 0 {
		t.Fatalf("progress = %+v, %v", progress, err)
	}
	for index := 1; index <= maxNoHumanChecks; index++ {
		check, checkErr := plugin.ObserveHumanPresence(
			context.Background(),
			state,
			false,
		)
		if checkErr != nil {
			t.Fatal(checkErr)
		}
		if check.ConsecutiveEmpty != index ||
			check.ShouldClose != (index == maxNoHumanChecks) {
			t.Fatalf("presence check %d = %+v", index, check)
		}
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
		RoomID:        "quoridor-room",
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
				26,
				11,
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
