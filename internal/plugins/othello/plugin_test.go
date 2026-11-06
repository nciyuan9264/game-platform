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
		first.WhitePlayerID != "ai_001" ||
		first.CurrentPlayer != "owner" {
		t.Fatalf("round one colors = %+v", first)
	}
	second := startPreparedMatch(t, 2)
	if second.BlackPlayerID != "ai_001" ||
		second.WhitePlayerID != "owner" ||
		second.CurrentPlayer != "ai_001" {
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
		result.AIRequests[0].PlayerID != "ai_001" ||
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
		state.LastAction.PlayerID != "ai_001" {
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

func TestPendingHintCompletesPrivatelyWithoutResettingDeadline(t *testing.T) {
	state := startPreparedMatch(t, 1)
	plugin := &Plugin{}
	deadline := state.TurnDeadline
	result, err := plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type:     CommandRequestHint,
			PlayerID: state.CurrentPlayer,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	player := state.Roster[state.CurrentPlayer]
	if !player.HintPending ||
		player.HintsUsed != 0 ||
		len(result.BackgroundRequests) != 0 {
		t.Fatalf("pending hint state = %+v, result = %+v", player, result)
	}
	if !state.TurnDeadline.Equal(deadline) {
		t.Fatalf("hint request changed deadline: %s", state.TurnDeadline)
	}

	bestMove := legalMoves(state.Board, currentSide(state))[0].Index
	result, err = plugin.ApplyCommand(
		context.Background(),
		state,
		internalAnalysisResultCommand(state, bestMove),
	)
	if err != nil {
		t.Fatal(err)
	}
	if player.HintPending ||
		player.HintsUsed != 1 ||
		player.Hint == nil ||
		player.Hint.Index != bestMove {
		t.Fatalf("completed hint state = %+v", player)
	}
	if len(result.BackgroundRequests) != 0 || len(result.AIRequests) != 0 {
		t.Fatalf("hint completion scheduled work: %+v", result)
	}
	if !state.TurnDeadline.Equal(deadline) {
		t.Fatalf("analysis completion changed deadline: %s", state.TurnDeadline)
	}

	ownerViewRaw, err := plugin.BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{PlayerID: "owner"},
	)
	if err != nil {
		t.Fatal(err)
	}
	ownerView := ownerViewRaw.(map[string]any)
	hint := ownerView["hint"].(map[string]any)
	if hint["index"] != bestMove ||
		hint["remaining"] != 2 ||
		hint["pending"] != false {
		t.Fatalf("owner hint view = %+v", hint)
	}
	opponentViewRaw, err := plugin.BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{PlayerID: "ai_001"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opponentViewRaw.(map[string]any)["hint"]; ok {
		t.Fatalf("opponent received private hint: %+v", opponentViewRaw)
	}
	spectatorViewRaw, err := plugin.BuildSpectatorView(
		context.Background(),
		state,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := spectatorViewRaw.(map[string]any)["hint"]; ok {
		t.Fatalf("spectator received private hint: %+v", spectatorViewRaw)
	}

	if _, err := plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type:     CommandPlaceDisc,
			PlayerID: "owner",
			Payload:  map[string]any{"index": bestMove},
		},
	); err != nil {
		t.Fatal(err)
	}
	if player.Hint != nil || player.HintPending {
		t.Fatalf("hint survived position change: %+v", player)
	}
}

func TestHintUnavailableRefundsAttemptAndReadyAnalysisCompletesImmediately(
	t *testing.T,
) {
	t.Run("unavailable refunds", func(t *testing.T) {
		state := startPreparedMatch(t, 1)
		plugin := &Plugin{}
		if _, err := plugin.ApplyCommand(
			context.Background(),
			state,
			contracts.RoomCommand{
				Type:     CommandRequestHint,
				PlayerID: "owner",
			},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := plugin.ApplyCommand(
			context.Background(),
			state,
			contracts.RoomCommand{
				Type: CommandAnalysisUnavailable,
				Payload: analysisUnavailablePayload{
					RequestID:   state.Analysis.RequestID,
					PositionKey: state.Analysis.PositionKey,
				},
			},
		); err != nil {
			t.Fatal(err)
		}
		player := state.Roster["owner"]
		if player.HintsUsed != 0 || player.HintPending || player.Hint != nil {
			t.Fatalf("failed analysis consumed hint: %+v", player)
		}
	})

	t.Run("ready completes immediately", func(t *testing.T) {
		state := startPreparedMatch(t, 1)
		plugin := &Plugin{}
		bestMove := legalMoves(state.Board, currentSide(state))[0].Index
		if _, err := plugin.ApplyCommand(
			context.Background(),
			state,
			internalAnalysisResultCommand(state, bestMove),
		); err != nil {
			t.Fatal(err)
		}
		if _, err := plugin.ApplyCommand(
			context.Background(),
			state,
			contracts.RoomCommand{
				Type:     CommandRequestHint,
				PlayerID: "owner",
			},
		); err != nil {
			t.Fatal(err)
		}
		player := state.Roster["owner"]
		if player.HintsUsed != 1 ||
			player.Hint == nil ||
			player.Hint.Index != bestMove {
			t.Fatalf("immediate hint = %+v", player)
		}
		if _, err := plugin.ApplyCommand(
			context.Background(),
			state,
			contracts.RoomCommand{
				Type:     CommandRequestHint,
				PlayerID: "owner",
			},
		); err == nil {
			t.Fatal("duplicate hint request for the same position was accepted")
		}
		if player.HintsUsed != 1 {
			t.Fatalf("duplicate request consumed a hint: %+v", player)
		}
		player.Hint = nil
		player.HintsUsed = hintLimit
		if _, err := plugin.ApplyCommand(
			context.Background(),
			state,
			contracts.RoomCommand{
				Type:     CommandRequestHint,
				PlayerID: "owner",
			},
		); err == nil {
			t.Fatal("hint request beyond the per-match limit was accepted")
		}
	})
}

func TestAIDifficultyDefaultsValidatesAndProjects(t *testing.T) {
	plugin := &Plugin{}
	newState := func() *State {
		raw, err := plugin.InitRoom(context.Background(), game.RoomConfig{
			RoomID:        "difficulty-room",
			OwnerPlayerID: "owner",
			RandomSeed:    42,
		})
		if err != nil {
			t.Fatal(err)
		}
		state := raw.(*State)
		if _, err := plugin.ApplyCommand(
			context.Background(),
			state,
			contracts.RoomCommand{Type: "connect", PlayerID: "owner"},
		); err != nil {
			t.Fatal(err)
		}
		return state
	}

	defaultState := newState()
	if _, err := plugin.ApplyCommand(
		context.Background(),
		defaultState,
		contracts.RoomCommand{
			Type:     "match_add_ai",
			PlayerID: "owner",
		},
	); err != nil {
		t.Fatal(err)
	}
	if got := defaultState.Roster["ai_001"].AIDifficulty; got != game.AIDifficultyMedium {
		t.Fatalf("default difficulty = %q", got)
	}

	hardState := newState()
	if _, err := plugin.ApplyCommand(
		context.Background(),
		hardState,
		contracts.RoomCommand{
			Type:     "match_add_ai",
			PlayerID: "owner",
			Payload:  map[string]any{"aiDifficulty": "hard"},
		},
	); err != nil {
		t.Fatal(err)
	}
	roster, err := plugin.Roster(context.Background(), hardState)
	if err != nil {
		t.Fatal(err)
	}
	if roster[1].AIDifficulty != "hard" {
		t.Fatalf("hard roster = %+v", roster)
	}

	invalidState := newState()
	if _, err := plugin.ApplyCommand(
		context.Background(),
		invalidState,
		contracts.RoomCommand{
			Type:     "match_add_ai",
			PlayerID: "owner",
			Payload:  map[string]any{"aiDifficulty": "expert"},
		},
	); err == nil {
		t.Fatal("invalid AI difficulty was accepted")
	}
}

func TestConfiguredHintLimit(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int
	}{
		{name: "default", want: 3},
		{name: "disabled", value: "0", want: 0},
		{name: "custom", value: "12", want: 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(hintLimitEnv, tt.value)
			if got := configuredHintLimit(); got != tt.want {
				t.Fatalf("configuredHintLimit() = %d, want %d", got, tt.want)
			}
		})
	}

	for _, value := range []string{"-1", "101", "invalid"} {
		t.Run("rejects "+value, func(t *testing.T) {
			t.Setenv(hintLimitEnv, value)
			defer func() {
				if recover() == nil {
					t.Fatalf("hint limit %q was accepted", value)
				}
			}()
			_ = configuredHintLimit()
		})
	}

	t.Run("admin custom", func(t *testing.T) {
		t.Setenv(adminHintLimitEnv, "9")
		if got := configuredAdminHintLimit(); got != 9 {
			t.Fatalf("configuredAdminHintLimit() = %d, want 9", got)
		}
	})

	t.Run("admin default", func(t *testing.T) {
		t.Setenv(adminHintLimitEnv, "")
		if got := configuredAdminHintLimit(); got != 50 {
			t.Fatalf("configuredAdminHintLimit() = %d, want 50", got)
		}
	})
}

func TestAdminRoleUsesIndependentHintLimit(t *testing.T) {
	originalHintLimit := hintLimit
	originalAdminHintLimit := adminHintLimit
	hintLimit = 3
	adminHintLimit = 7
	t.Cleanup(func() {
		hintLimit = originalHintLimit
		adminHintLimit = originalAdminHintLimit
	})

	state := startPreparedMatch(t, 1)
	plugin := &Plugin{}
	if _, err := plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type:     "connect",
			PlayerID: "owner",
			Roles:    []string{"member", "ADMIN"},
		},
	); err != nil {
		t.Fatal(err)
	}
	player := state.Roster["owner"]
	if !player.Admin {
		t.Fatal("admin role was not persisted on the Othello player")
	}
	view := hintView(state, "owner")
	if view["remaining"] != 7 {
		t.Fatalf("admin hint view = %+v, want 7 remaining", view)
	}

	bestMove := legalMoves(state.Board, currentSide(state))[0].Index
	if _, err := plugin.ApplyCommand(
		context.Background(),
		state,
		internalAnalysisResultCommand(state, bestMove),
	); err != nil {
		t.Fatal(err)
	}
	player.HintsUsed = 6
	if _, err := plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type:     CommandRequestHint,
			PlayerID: "owner",
		},
	); err != nil {
		t.Fatal(err)
	}
	if player.HintsUsed != 7 {
		t.Fatalf("admin hint usage = %d, want 7", player.HintsUsed)
	}

	player.HintsUsed = 0
	player.Hint = nil
	if _, err := plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type:     "connect",
			PlayerID: "owner",
			Roles:    []string{"member"},
		},
	); err != nil {
		t.Fatal(err)
	}
	if player.Admin {
		t.Fatal("removed admin role remained on the Othello player")
	}
	view = hintView(state, "owner")
	if view["remaining"] != 3 {
		t.Fatalf("member hint view = %+v, want 3 remaining", view)
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
