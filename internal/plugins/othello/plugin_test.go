package othello

import (
	"context"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestRoundAlternatesColorsAndBuildsAuthoritativeView(t *testing.T) {
	first := startPreparedMatch(t, 1)
	if first.BlackPlayerID != "owner" ||
		first.WhitePlayerID != "ai_2" ||
		first.CurrentPlayer != "owner" {
		t.Fatalf("round one colors = %+v", first)
	}
	second := startPreparedMatch(t, 2)
	if second.BlackPlayerID != "ai_2" ||
		second.WhitePlayerID != "owner" ||
		second.CurrentPlayer != "ai_2" {
		t.Fatalf("round two colors = %+v", second)
	}

	view, err := (&Plugin{}).BuildPlayerView(
		context.Background(),
		first,
		game.ViewRequest{
			MatchID:  "othello-room-r1",
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
	payload := view.(map[string]any)
	if payload["playerId"] != "owner" || payload["seq"] != int64(7) {
		t.Fatalf("view envelope = %+v", payload)
	}
	roomData := payload["roomData"].(map[string]any)
	if roomData["gameStatus"] != StatusPlaying ||
		roomData["currentSide"] != SideBlack ||
		roomData["roundNo"] != 1 ||
		len(roomData["legalMoves"].([]Move)) != 4 {
		t.Fatalf("room data = %+v", roomData)
	}
	players := roomData["players"].(map[string]any)
	owner := players["owner"].(map[string]any)
	if owner["displayName"] != "Alice" ||
		owner["avatar"] != "https://example.com/alice.png" {
		t.Fatalf("owner profile = %+v", owner)
	}
}

func TestHumanTimeoutAndAIRequestAreRecoverable(t *testing.T) {
	state := startPreparedMatch(t, 1)
	sentAt := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	updateTurnDeadline(state, sentAt)
	pending, err := (&Plugin{}).PendingCommands(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 ||
		pending[0].Type != CommandTimeout ||
		pending[0].PlayerID != "owner" ||
		!pending[0].ExecuteAt.Equal(sentAt.Add(turnTimeout)) {
		t.Fatalf("pending = %+v", pending)
	}
	if _, err := (&Plugin{}).ApplyCommand(
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
	result, err := (&Plugin{}).ApplyCommand(
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
		state.LastAction.Kind != "move" {
		t.Fatalf("timeout action = %+v", state.LastAction)
	}
	if len(result.AIRequests) != 1 ||
		result.AIRequests[0].PlayerID != "ai_2" ||
		result.AIRequests[0].Delay < aiMinDelay ||
		result.AIRequests[0].Delay > aiMaxDelay {
		t.Fatalf("AI request = %+v", result.AIRequests)
	}
	aiCommand, err := (&Plugin{}).Decide(
		context.Background(),
		result.AIRequests[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	aiCommand.SentAt = sentAt.Add(turnTimeout + result.AIRequests[0].Delay)
	if _, err := (&Plugin{}).ApplyCommand(
		context.Background(),
		state,
		aiCommand,
	); err != nil {
		t.Fatal(err)
	}
	if state.LastAction == nil ||
		!state.LastAction.Automatic ||
		state.LastAction.PlayerID != "ai_2" {
		t.Fatalf("AI action = %+v", state.LastAction)
	}

	encoded, err := (&Plugin{}).EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	decodedRaw, err := (&Plugin{}).DecodeState(context.Background(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodedRaw.(*State)
	if decoded.LastAction == nil ||
		!decoded.LastAction.Automatic ||
		len(decoded.Board) != 64 {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestMetadataProgressPresenceAndSpectator(t *testing.T) {
	state := startPreparedMatch(t, 1)
	plugin := &Plugin{}
	metadata, err := plugin.RoomMetadata(context.Background(), state)
	if err != nil || metadata.Visibility != contracts.VisibilityPublic {
		t.Fatalf("metadata = %+v, %v", metadata, err)
	}
	progress, err := plugin.Progress(context.Background(), state)
	if err != nil || progress.Current != 0 || progress.Total != 60 {
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
	spectator, err := plugin.BuildSpectatorView(
		context.Background(),
		state,
		9,
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := spectator.(map[string]any)
	if payload["seq"] != int64(9) || payload["roomData"] == nil {
		t.Fatalf("spectator = %+v", payload)
	}
}

func startPreparedMatch(t *testing.T, roundNo int) *State {
	t.Helper()
	plugin := &Plugin{}
	raw, err := plugin.InitRoom(context.Background(), game.RoomConfig{
		RoomID:        "othello-room",
		OwnerPlayerID: "owner",
		MaxPlayers:    2,
		RandomSeed:    42,
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
				25,
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
