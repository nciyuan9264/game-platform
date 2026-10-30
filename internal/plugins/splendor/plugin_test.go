package splendor

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/plugins/splendor/domain"
	"github.com/nciyuan9264/game-platform/internal/plugins/splendor/entities"
	"github.com/nciyuan9264/game-platform/internal/plugins/splendor/rules"
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
	for playerID, player := range state.Players {
		if player.NormalCard == nil || player.NobleCard == nil ||
			player.Gem == nil || player.ReserveCard == nil {
			t.Fatalf("player %s has nil collections: %+v", playerID, player)
		}
	}
	apply("7", "game_ready", nil)
	apply("ai_001", "game_ready", nil)
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
	if projection.ReplaySchemaVersion != ReplaySchemaVersion || projection.ReplayFrame == nil {
		t.Fatalf("materialized replay projection = %+v", projection)
	}
	assertReplayFrameMatchesGolden(t, projection.ReplayFrame)
}

func TestPluginDecodeStateNormalizesPlayerCollections(t *testing.T) {
	plugin := &Plugin{}
	decoded, err := plugin.DecodeState(
		context.Background(),
		[]byte(`{"players":{"player-1":{}}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	player := decoded.(*domain.GameState).Players["player-1"]
	if player.NormalCard == nil || player.NobleCard == nil ||
		player.Gem == nil || player.ReserveCard == nil {
		t.Fatalf("decoded player has nil collections: %+v", player)
	}
}

func TestSplendorAIStaysReadyForSingleHumanStart(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	raw, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "splendor-ai-ready",
		OwnerPlayerID: "owner",
		MaxPlayers:    2,
		RandomSeed:    7,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*domain.GameState)
	apply := func(commandType string) {
		t.Helper()
		if _, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
			PlayerID: "owner",
			Type:     commandType,
			SentAt:   time.Now().UTC(),
		}); err != nil {
			t.Fatalf("%s: %v", commandType, err)
		}
	}
	apply("connect")
	apply("match_add_ai")
	apply("match_begin")
	if state.Roster["owner"].Ready || !state.Roster["ai_001"].Ready {
		t.Fatalf("waiting readiness = owner:%v ai:%v",
			state.Roster["owner"].Ready,
			state.Roster["ai_001"].Ready,
		)
	}
	apply("game_ready")
	if state.RoomStatus != domain.RoomStatusPlaying {
		t.Fatalf("room status = %q, want playing", state.RoomStatus)
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
		{PlayerID: "ai_001", Type: "game_ready"},
	} {
		command.SentAt = time.Now().UTC()
		if _, err := plugin.ApplyCommand(ctx, state, command); err != nil {
			t.Fatal(err)
		}
	}
	view, err := plugin.BuildPlayerView(ctx, state, game.ViewRequest{
		RoomID: state.RoomID, MatchID: "match-1", PlayerID: "7", Seq: 1,
		PlayerProfiles: map[string]game.PlayerProfile{
			"7": {
				UserID:      "user-7",
				DisplayName: "Alice",
				Avatar:      "https://example.com/alice.png",
			},
		},
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
	players := roomData["players"].(map[string]any)
	player := players["7"].(map[string]any)
	if player["userID"] != "user-7" || player["displayName"] != "Alice" || player["avatar"] != "https://example.com/alice.png" {
		t.Fatalf("player profile = %+v", player)
	}
}

func TestSplendorMetadataAndRecoverableTurnDeadline(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	raw, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "splendor-metadata",
		OwnerPlayerID: "owner",
		MaxPlayers:    2,
		RandomSeed:    2,
		Visibility:    contracts.VisibilityPrivate,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*domain.GameState)
	metadata, err := plugin.RoomMetadata(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Visibility != contracts.VisibilityPrivate {
		t.Fatalf("initial visibility = %q", metadata.Visibility)
	}

	sentAt := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	commands := []contracts.RoomCommand{
		{PlayerID: "owner", Type: "connect"},
		{PlayerID: "owner", Type: "match_add_ai", Payload: map[string]any{"seatIndex": 1}},
		{PlayerID: "owner", Type: "match_begin"},
		{PlayerID: "owner", Type: "game_ready"},
		{PlayerID: "ai_001", Type: "game_ready", SentAt: sentAt},
	}
	for _, command := range commands {
		if _, err := plugin.ApplyCommand(ctx, state, command); err != nil {
			t.Fatalf("%s: %v", command.Type, err)
		}
	}
	if !state.TurnDeadline.Equal(sentAt.Add(time.Minute)) || state.TurnTimeout != time.Minute {
		t.Fatalf("turn timing = %s/%s", state.TurnDeadline, state.TurnTimeout)
	}
	deadline := state.TurnDeadline
	for _, command := range []contracts.RoomCommand{
		{PlayerID: "owner", Type: "disconnect", SentAt: sentAt.Add(10 * time.Second)},
		{PlayerID: "owner", Type: "connect", SentAt: sentAt.Add(20 * time.Second)},
	} {
		if _, err := plugin.ApplyCommand(ctx, state, command); err != nil {
			t.Fatalf("%s: %v", command.Type, err)
		}
		if !state.TurnDeadline.Equal(deadline) {
			t.Fatalf("%s changed deadline from %s to %s", command.Type, deadline, state.TurnDeadline)
		}
	}
	pending, err := plugin.PendingCommands(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].PlayerID != "owner" ||
		!pending[0].ExecuteAt.Equal(state.TurnDeadline) {
		t.Fatalf("pending timeout command = %+v", pending)
	}
	view, err := plugin.BuildPlayerView(ctx, state, game.ViewRequest{PlayerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	roomData := view.(map[string]any)["roomData"].(map[string]any)
	if roomData["turnDeadline"] == nil || roomData["turnTimeoutMs"] != int64(60_000) {
		t.Fatalf("turn timing view = %+v", roomData)
	}
}

func TestSplendorAIAtTokenLimitPreservesGrantedGold(t *testing.T) {
	plugin := &Plugin{}
	state := &domain.GameState{
		RoomID:        "splendor-ai-gold",
		RoomStatus:    domain.RoomStatusPlaying,
		CurrentPlayer: "ai_1",
		Roster: map[string]*domain.Player{
			"ai_1": {PlayerID: "ai_1", AI: true},
		},
		Players: map[string]*domain.PlayerState{
			"ai_1": {Gem: map[string]int{"Red": 10}},
		},
		NormalCards: map[string]*entities.NormalCard{
			"1": {ID: 1, Cost: map[string]int{"Red": 11}, State: entities.CardStateRevealed},
		},
		NobleCards: map[string]*entities.NobleCard{},
		Gems:       map[string]int{"Gold": 5},
	}
	command, err := plugin.Decide(context.Background(), game.AIRequest{
		PlayerID: "ai_1",
		State:    state,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := command.Payload.(map[string]any)
	returns := payload["returns"].(map[string]int)
	if command.Type != "game_preserve_card" || returns["Red"] != 1 || returns["Gold"] != 0 {
		t.Fatalf("AI reserve command = %+v", command)
	}
	rawPayload, err := json.Marshal(command.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if actionErr := rules.ValidateAction(domain.NewRoom(state), domain.Command{
		Type: command.Type, PlayerID: command.PlayerID, Payload: rawPayload,
	}); actionErr != nil {
		t.Fatalf("AI reserve command is illegal: %v", actionErr)
	}
}

func TestRevealedNoblesUseNaturalIDOrder(t *testing.T) {
	state := &domain.GameState{NobleCards: map[string]*entities.NobleCard{
		"N10": {ID: "N10", State: entities.CardStateRevealed},
		"N2":  {ID: "N2", State: entities.CardStateRevealed},
		"N1":  {ID: "N1", State: entities.CardStateRevealed},
	}}
	nobles := revealedNobles(state)
	if len(nobles) != 3 || nobles[0].ID != "N1" || nobles[1].ID != "N2" || nobles[2].ID != "N10" {
		t.Fatalf("noble order = %+v", nobles)
	}
}

func TestNextSplendorAIRequestUsesDelayAndDeepSnapshot(t *testing.T) {
	state := &domain.GameState{
		RoomID:        "snapshot-test",
		RoomStatus:    domain.RoomStatusPlaying,
		CurrentPlayer: "ai",
		PlayerOrder:   []string{"ai"},
		Roster: map[string]*domain.Player{
			"ai": {PlayerID: "ai", DisplayName: "before", AI: true},
		},
		NormalCards: map[string]*entities.NormalCard{
			"1": {
				ID: 1, State: entities.CardStateRevealed,
				Cost: map[string]int{"Red": 1},
			},
		},
		NobleCards: map[string]*entities.NobleCard{
			"N1": {
				ID: "N1", State: entities.CardStateRevealed,
				Cost: map[string]int{"Blue": 3},
			},
		},
		Gems: map[string]int{"Gold": 5},
		Players: map[string]*domain.PlayerState{
			"ai": {
				Gem:        map[string]int{"Red": 1},
				NormalCard: []entities.NormalCard{{ID: 2, Cost: map[string]int{"White": 1}}},
				NobleCard:  []entities.NobleCard{{ID: "N2", Cost: map[string]int{"Black": 3}}},
				ReserveCard: []entities.NormalCard{{
					ID: 3, Cost: map[string]int{"Green": 2},
				}},
			},
		},
		LastData: &domain.LastAction{Payload: json.RawMessage(`{"before":true}`)},
	}

	var request *game.AIRequest
	for iteration := 0; iteration < 200; iteration++ {
		current := nextAIRequest(state)
		if current == nil {
			t.Fatal("AI request is nil")
		}
		if current.Delay < splendorAIMinDelay || current.Delay > splendorAIMaxDelay {
			t.Fatalf("delay = %s", current.Delay)
		}
		if iteration == 0 {
			request = current
		}
	}

	snapshot := request.State.(*domain.GameState)
	state.PlayerOrder[0] = "changed"
	state.Roster["ai"].DisplayName = "changed"
	state.NormalCards["1"].Cost["Red"] = 9
	state.NobleCards["N1"].Cost["Blue"] = 9
	state.Gems["Gold"] = 0
	state.Players["ai"].Gem["Red"] = 9
	state.Players["ai"].NormalCard[0].Cost["White"] = 9
	state.Players["ai"].NobleCard[0].Cost["Black"] = 9
	state.Players["ai"].ReserveCard[0].Cost["Green"] = 9
	state.LastData.Payload[2] = 'X'

	if snapshot.PlayerOrder[0] != "ai" ||
		snapshot.Roster["ai"].DisplayName != "before" ||
		snapshot.NormalCards["1"].Cost["Red"] != 1 ||
		snapshot.NobleCards["N1"].Cost["Blue"] != 3 ||
		snapshot.Gems["Gold"] != 5 ||
		snapshot.Players["ai"].Gem["Red"] != 1 ||
		snapshot.Players["ai"].NormalCard[0].Cost["White"] != 1 ||
		snapshot.Players["ai"].NobleCard[0].Cost["Black"] != 3 ||
		snapshot.Players["ai"].ReserveCard[0].Cost["Green"] != 2 ||
		string(snapshot.LastData.Payload) != `{"before":true}` {
		t.Fatalf("AI state was not deeply cloned: %+v", snapshot)
	}
}

func TestSplendorAllAIGamesFinishWithLegalCommands(t *testing.T) {
	for _, playerCount := range []int{2, 3, 4} {
		t.Run(fmt.Sprintf("%d players", playerCount), func(t *testing.T) {
			state := &domain.GameState{
				RoomID:      fmt.Sprintf("all-ai-%d", playerCount),
				RoomStatus:  domain.RoomStatusPlaying,
				MaxPlayers:  playerCount,
				RandomSeed:  uint64(100 + playerCount),
				Roster:      map[string]*domain.Player{},
				PlayerOrder: make([]string, 0, playerCount),
			}
			for index := 1; index <= playerCount; index++ {
				playerID := fmt.Sprintf("ai_%d", index)
				state.PlayerOrder = append(state.PlayerOrder, playerID)
				state.Roster[playerID] = &domain.Player{
					PlayerID:  playerID,
					AI:        true,
					SeatIndex: index - 1,
				}
			}
			initBoard(state)
			state.CurrentPlayer = state.PlayerOrder[0]
			state.FirstPlayer = state.CurrentPlayer
			state.RoomStatus = domain.RoomStatusPlaying

			plugin := &Plugin{}
			for turn := 0; turn < 400 && state.RoomStatus != domain.RoomStatusEnd; turn++ {
				playerID := state.CurrentPlayer
				command, err := plugin.Decide(context.Background(), game.AIRequest{
					PlayerID: playerID,
					State:    state,
				})
				if err != nil {
					t.Fatalf("turn %d player %s: %v", turn, playerID, err)
				}
				rawPayload, err := json.Marshal(command.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if actionErr := rules.ValidateAction(domain.NewRoom(state), domain.Command{
					Type: command.Type, PlayerID: playerID, Payload: rawPayload,
				}); actionErr != nil {
					t.Fatalf("turn %d illegal command %+v: %v", turn, command, actionErr)
				}
				if _, err := plugin.ApplyCommand(context.Background(), state, command); err != nil {
					t.Fatalf("turn %d rejected command %+v: %v", turn, command, err)
				}
			}
			if state.RoomStatus != domain.RoomStatusEnd {
				t.Fatalf("game did not finish: current=%s scores=%+v", state.CurrentPlayer, state.Players)
			}
		})
	}
}
