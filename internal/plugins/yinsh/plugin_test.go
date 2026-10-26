package yinsh

import (
	"context"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestPlayerViewIncludesProfilesBoardActionsAndTiming(t *testing.T) {
	state := startPreparedMatch(t, 1)
	view, err := (&Plugin{}).BuildPlayerView(context.Background(), state, game.ViewRequest{
		RoomID:   state.RoomID,
		MatchID:  "yinsh-room-r1",
		PlayerID: "owner",
		Seq:      4,
		PlayerProfiles: map[string]game.PlayerProfile{
			"owner": {
				UserID:      "100",
				DisplayName: "Alice",
				Avatar:      "https://example.com/alice.png",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := view.(map[string]any)
	if payload["playerId"] != "owner" || payload["seq"] != int64(4) {
		t.Fatalf("view envelope = %+v", payload)
	}
	roomData := payload["roomData"].(map[string]any)
	if roomData["gameStatus"] != StatusPlacement ||
		roomData["currentPlayer"] != "owner" ||
		roomData["markerPool"] != 51 ||
		roomData["roundNo"] != 1 {
		t.Fatalf("room data = %+v", roomData)
	}
	players := roomData["players"].(map[string]any)
	owner := players["owner"].(map[string]any)
	if owner["userID"] != "100" ||
		owner["displayName"] != "Alice" ||
		owner["avatar"] != "https://example.com/alice.png" {
		t.Fatalf("owner profile = %+v", owner)
	}
	if roomData["turnDeadline"] == nil || roomData["turnTimeoutMs"] != int64(30_000) {
		t.Fatalf("placement timing = %+v", roomData)
	}
}

func TestConnectionLifecyclePreservesTurnDeadline(t *testing.T) {
	plugin := &Plugin{}
	state := startPreparedMatch(t, 1)
	sentAt := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	updateTurnDeadline(state, sentAt)
	deadline := state.TurnDeadline

	for _, command := range []contracts.RoomCommand{
		{Type: "disconnect", PlayerID: state.CurrentPlayer, SentAt: sentAt.Add(10 * time.Second)},
		{Type: "connect", PlayerID: state.CurrentPlayer, SentAt: sentAt.Add(20 * time.Second)},
	} {
		if _, err := plugin.ApplyCommand(context.Background(), state, command); err != nil {
			t.Fatalf("%s: %v", command.Type, err)
		}
		if !state.TurnDeadline.Equal(deadline) {
			t.Fatalf("%s changed deadline from %s to %s", command.Type, deadline, state.TurnDeadline)
		}
	}
}

func TestAIRequestDelayAndFallback(t *testing.T) {
	state := startPreparedMatch(t, 1)
	state.Roster["owner"].AI = true
	request := nextAIRequest(state)
	if request == nil ||
		request.Delay < aiMinDelay ||
		request.Delay > aiMaxDelay ||
		request.FallbackCommand == nil ||
		request.FallbackCommand.Type != CommandTimeout {
		t.Fatalf("AI request = %+v", request)
	}
}

func TestMetadataProgressPresenceAndSpectator(t *testing.T) {
	state := startPreparedMatch(t, 1)
	plugin := &Plugin{}
	metadata, err := plugin.RoomMetadata(context.Background(), state)
	if err != nil || metadata.Visibility != contracts.VisibilityPublic {
		t.Fatalf("metadata = %+v, %v", metadata, err)
	}
	state.RingsRemoved[SideWhite] = 2
	progress, err := plugin.Progress(context.Background(), state)
	if err != nil || progress.Current != 2 || progress.Total != 3 {
		t.Fatalf("progress = %+v, %v", progress, err)
	}
	for index := 1; index <= maxNoHumanChecks; index++ {
		check, err := plugin.ObserveHumanPresence(context.Background(), state, false)
		if err != nil {
			t.Fatal(err)
		}
		if check.ConsecutiveEmpty != index ||
			check.ShouldClose != (index == maxNoHumanChecks) {
			t.Fatalf("presence check %d = %+v", index, check)
		}
	}
	spectator, err := plugin.BuildSpectatorView(context.Background(), state, 9)
	if err != nil {
		t.Fatal(err)
	}
	payload := spectator.(map[string]any)
	if payload["seq"] != int64(9) || payload["roomData"] == nil {
		t.Fatalf("spectator = %+v", payload)
	}
}

func TestPendingHumanTimeoutIsRecoverable(t *testing.T) {
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
		!pending[0].ExecuteAt.Equal(sentAt.Add(placementTimeout)) {
		t.Fatalf("pending = %+v", pending)
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
	if !decoded.TurnDeadline.Equal(state.TurnDeadline) ||
		decoded.TurnTimeout != placementTimeout {
		t.Fatalf("decoded timing = %s/%s", decoded.TurnDeadline, decoded.TurnTimeout)
	}
}
