package davinci

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestBuildSpectatorViewIncludesBothPlayersHiddenCards(t *testing.T) {
	plugin := NewPlugin()
	state := &State{
		RoomID:        "room-1",
		OwnerID:       "alice-1",
		Status:        StatusGuessCard,
		CurrentPlayer: "alice-1",
		PlayerSeq:     []string{"alice-1", "bob-2"},
		Players: map[string]*Player{
			"alice-1": {
				PlayerID: "alice-1",
				Cards:    []Card{{ID: "a", Num: 3, Color: 0, Index: 0}},
			},
			"bob-2": {
				PlayerID: "bob-2",
				Cards:    []Card{{ID: "b", Num: 9, Color: 1, Index: 0}},
			},
		},
	}
	raw, err := plugin.BuildSpectatorView(context.Background(), state, 7)
	if err != nil {
		t.Fatalf("BuildSpectatorView() error = %v", err)
	}
	view := raw.(map[string]any)
	players := view["playersData"].(map[string]*Player)
	if players["alice-1"].Cards[0].Num != 3 || players["bob-2"].Cards[0].Num != 9 {
		t.Fatalf("spectator view omitted hidden card values: %+v", players)
	}

	playerRaw, err := plugin.BuildPlayerView(context.Background(), state, game.ViewRequest{PlayerID: "alice-1", Seq: 7})
	if err != nil {
		t.Fatalf("BuildPlayerView() error = %v", err)
	}
	if playerRaw == nil {
		t.Fatal("player view is nil")
	}
}

func TestBuildMatchSyncIncludesRequiredRoomMetadata(t *testing.T) {
	plugin := NewPlugin()
	state := &State{
		RoomID:    "davinci-match-sync",
		OwnerID:   "alice-1",
		Status:    contracts.RoomStatusMatch,
		PlayerSeq: []string{"alice-1", "bob-2"},
		Players: map[string]*Player{
			"alice-1": {PlayerID: "alice-1"},
			"bob-2":   {PlayerID: "bob-2"},
		},
	}

	raw, err := plugin.BuildPlayerView(context.Background(), state, game.ViewRequest{
		MatchID:    "match-1",
		PlayerID:   "alice-1",
		Visibility: contracts.VisibilityPrivate,
		MaxPlayers: 4,
		PlayerProfiles: map[string]game.PlayerProfile{
			"alice-1": {
				UserID:      "user-1",
				DisplayName: "Alice",
				Avatar:      "https://example.com/alice.png",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	view := raw.(contracts.MatchSync)
	if view.Visibility != contracts.VisibilityPrivate || view.MaxPlayers != 4 {
		t.Fatalf("room metadata = (%q, %d), want (%q, 4)", view.Visibility, view.MaxPlayers, contracts.VisibilityPrivate)
	}
	if !reflect.DeepEqual(view.PlayerOrder, []string{"alice-1", "bob-2"}) {
		t.Fatalf("player order = %v", view.PlayerOrder)
	}
	player := view.Players["alice-1"]
	if player.UserID != "user-1" || player.DisplayName != "Alice" || player.Avatar != "https://example.com/alice.png" {
		t.Fatalf("player profile = %+v", player)
	}
	state.PlayerSeq[0] = "changed"
	if view.PlayerOrder[0] != "alice-1" {
		t.Fatalf("player order aliases mutable state: %v", view.PlayerOrder)
	}
}

func TestRoomVisibilitySettings(t *testing.T) {
	plugin := NewPlugin()
	raw, err := plugin.InitRoom(context.Background(), game.RoomConfig{
		RoomID:        "davinci-private-room",
		OwnerPlayerID: "alice-1",
		MaxPlayers:    2,
		RandomSeed:    1,
		Visibility:    contracts.VisibilityPrivate,
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := plugin.RoomMetadata(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Visibility != contracts.VisibilityPrivate {
		t.Fatalf("initial visibility = %q, want private", metadata.Visibility)
	}

	if _, err := plugin.ApplyCommand(context.Background(), raw, contracts.RoomCommand{
		Type:     CommandRoomSettings,
		PlayerID: "bob-2",
		Payload:  map[string]any{"visibility": contracts.VisibilityPublic},
	}); err == nil {
		t.Fatal("non-owner updated room visibility")
	}
	if _, err := plugin.ApplyCommand(context.Background(), raw, contracts.RoomCommand{
		Type:     CommandRoomSettings,
		PlayerID: "alice-1",
		Payload:  map[string]any{"visibility": contracts.VisibilityPublic},
	}); err != nil {
		t.Fatal(err)
	}
	metadata, err = plugin.RoomMetadata(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Visibility != contracts.VisibilityPublic {
		t.Fatalf("updated visibility = %q, want public", metadata.Visibility)
	}

	state := raw.(*State)
	state.Status = StatusGetCard
	if _, err := plugin.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type:     CommandRoomSettings,
		PlayerID: "alice-1",
		Payload:  map[string]any{"visibility": contracts.VisibilityPrivate},
	}); err == nil {
		t.Fatal("owner updated room visibility after preparation")
	}
}

func TestOwnerOnlyCommandsAreEnforcedByEngine(t *testing.T) {
	engine := NewEngine()
	state := engine.InitRoom("room-1", "alice-1", 3)
	for _, command := range []contracts.RoomCommand{
		{Type: CommandConnect, PlayerID: "alice-1"},
		{Type: CommandConnect, PlayerID: "bob-2"},
		{Type: CommandMatchReady, PlayerID: "bob-2", Payload: map[string]any{"ready": true}},
	} {
		if _, err := engine.ApplyCommand(context.Background(), state, command); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type: CommandAddAI, PlayerID: "bob-2",
	}); err == nil {
		t.Fatal("non-owner added an AI player")
	}
	if _, err := engine.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type: CommandMatchBegin, PlayerID: "bob-2",
	}); err == nil {
		t.Fatal("non-owner started the game")
	}
	state.Status = StatusEnd
	if _, err := engine.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type: CommandRestart, PlayerID: "bob-2",
	}); err == nil {
		t.Fatal("non-owner restarted the game")
	}
	if _, err := engine.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type: CommandRestart, PlayerID: "alice-1",
	}); err == nil {
		t.Fatal("terminal room was restarted in place")
	}
}

func TestDeckUsesPrivateSeedAndOpaqueCardIDs(t *testing.T) {
	if _, err := NewPlugin().InitRoom(context.Background(), game.RoomConfig{RoomID: "missing-seed"}); err == nil {
		t.Fatal("room initialized without a random seed")
	}
	first := buildDeck(12345, 0)
	replayed := buildDeck(12345, 0)
	different := buildDeck(67890, 0)
	if !reflect.DeepEqual(first, replayed) {
		t.Fatal("same seed did not reproduce the deck")
	}
	if reflect.DeepEqual(first, different) {
		t.Fatal("different seeds produced the same deck")
	}
	for _, card := range first {
		if card.ID == fmt.Sprintf("%d_%d", card.Color, card.Num) {
			t.Fatalf("card ID exposes hidden value: %+v", card)
		}
	}
}

func TestDavinciRejectsInvalidGuessAndSetCard(t *testing.T) {
	engine := NewEngine()
	state := &State{
		Status:        StatusGuessCard,
		CurrentPlayer: "alice",
		PlayerSeq:     []string{"alice", "bob"},
		Players: map[string]*Player{
			"alice": {
				PlayerID: "alice",
				Cards:    []Card{{ID: "a1", Num: 3, Index: 0}},
			},
			"bob": {
				PlayerID: "bob",
				Cards:    []Card{{ID: "b1", Num: 5, Index: 0, IsRevealed: true}},
			},
		},
	}
	for _, payload := range []map[string]any{
		{"id": "b1", "num": 5},
		{"id": "b1", "num": 12},
	} {
		if _, err := engine.ApplyCommand(context.Background(), state, contracts.RoomCommand{
			Type: CommandGuessCard, PlayerID: "alice", Payload: payload,
		}); err == nil {
			t.Fatalf("invalid guess accepted: %+v", payload)
		}
	}

	state.Status = StatusSetCard
	state.Players["alice"].Cards = []Card{
		{ID: "existing", Num: 3, Index: 0},
		{ID: "new", Num: 7, Index: -1},
	}
	for _, payload := range []map[string]any{
		{"id": "existing", "index": 0},
		{"id": "new", "index": 0},
		{"id": "new", "index": 2},
	} {
		if _, err := engine.ApplyCommand(context.Background(), state, contracts.RoomCommand{
			Type: CommandSetCard, PlayerID: "alice", Payload: payload,
		}); err == nil {
			t.Fatalf("invalid set-card accepted: %+v", payload)
		}
	}
	if state.Players["alice"].Cards[1].Index != -1 {
		t.Fatal("rejected set-card mutated pending card")
	}
}

func TestDavinciRequestedSeatRemovePlayerAndRoster(t *testing.T) {
	engine := NewEngine()
	state := engine.InitRoom("room", "owner", 4)
	if _, err := engine.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type: CommandConnect, PlayerID: "owner",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type: CommandConnect, PlayerID: "guest",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type: CommandAddAI, PlayerID: "owner", Payload: map[string]any{"seatIndex": 3},
	}); err != nil {
		t.Fatal(err)
	}
	if state.Players["AI_3"] == nil || state.Players["AI_3"].SeatIndex != 3 {
		t.Fatalf("requested AI seat not retained: %+v", state.Players)
	}
	if _, err := engine.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type: CommandRemove, PlayerID: "owner", Payload: map[string]any{"playerID": "guest"},
	}); err != nil {
		t.Fatal(err)
	}
	if state.Players["guest"] != nil || slicesContain(state.PlayerSeq, "guest") {
		t.Fatalf("removed player remains in state: %+v", state)
	}
}

func TestDavinciSchedulerPersistsDeadlineAndBuildsTimeoutAction(t *testing.T) {
	plugin := NewPlugin()
	state := &State{
		RoomID:        "deadline-room",
		Status:        StatusGetCard,
		CurrentPlayer: "alice",
		PlayerSeq:     []string{"alice", "bob"},
		Players: map[string]*Player{
			"alice": {PlayerID: "alice", Online: true},
			"bob":   {PlayerID: "bob", Online: true},
		},
		Deck: []Card{{ID: "deck-1", Num: 1, Index: -1}},
	}
	sentAt := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	if _, err := plugin.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type: CommandTurnTimeout, PlayerID: "bob", SentAt: sentAt,
	}); err == nil {
		t.Fatal("stale timeout was accepted")
	}
	updateTurnDeadline(state, sentAt)
	if !state.TurnDeadline.Equal(sentAt.Add(20 * time.Second)) {
		t.Fatalf("deadline = %s", state.TurnDeadline)
	}
	pending, err := plugin.PendingCommands(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Type != CommandGetCard ||
		pending[0].PlayerID != "alice" || !pending[0].ExecuteAt.Equal(state.TurnDeadline) {
		t.Fatalf("pending timeout action = %+v", pending)
	}

	raw, err := plugin.BuildPlayerView(context.Background(), state, game.ViewRequest{PlayerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	roomData := raw.(contracts.RoomSync).RoomData.(map[string]any)
	if roomData["turnDeadline"] == nil || roomData["turnTimeoutMs"] != int64(20_000) {
		t.Fatalf("deadline view = %+v", roomData)
	}
}

func TestDavinciInitialHandSupportsZeroOneAndTwoJokers(t *testing.T) {
	engine := NewEngine()
	state := &State{
		Status:    StatusInitialize,
		OwnerID:   "alice",
		PlayerSeq: []string{"alice", "bob"},
		Players: map[string]*Player{
			"alice": {
				PlayerID: "alice",
				Cards: []Card{
					{ID: "a-2", Num: 2, Color: 0},
					{ID: "a-black-joker", Num: NumMinus1, Color: 1},
					{ID: "a-7", Num: 7, Color: 1},
					{ID: "a-white-joker", Num: NumMinus1, Color: 0},
					{ID: "a-4", Num: 4, Color: 0},
				},
			},
			"bob": {
				PlayerID: "bob",
				Cards: []Card{
					{ID: "b-1", Num: 1, Color: 0},
					{ID: "b-3", Num: 3, Color: 1},
					{ID: "b-5", Num: 5, Color: 0},
					{ID: "b-8", Num: 8, Color: 1},
					{ID: "b-10", Num: 10, Color: 0},
				},
			},
		},
	}
	prepareInitialHand(state.Players["alice"])
	prepareInitialHand(state.Players["bob"])
	stableOrder := cardIDs(state.Players["alice"].Cards)

	if _, err := engine.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type:     CommandInitialize,
		PlayerID: "alice",
		Payload: map[string]any{"jokerPlacements": []map[string]any{
			{"id": "a-black-joker", "index": 1},
			{"id": "a-white-joker", "index": 4},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusInitialize || !state.Players["alice"].InitialHandReady {
		t.Fatalf("first confirmation state = %+v", state)
	}
	if got := cardIndexes(state.Players["alice"].Cards); !reflect.DeepEqual(got, map[string]int{
		"a-2": 0, "a-black-joker": 1, "a-4": 2, "a-7": 3, "a-white-joker": 4,
	}) {
		t.Fatalf("alice indexes = %+v", got)
	}
	if got := cardIDs(state.Players["alice"].Cards); !reflect.DeepEqual(got, stableOrder) {
		t.Fatalf("initial placement reordered backing cards: got %v want %v", got, stableOrder)
	}

	if _, err := engine.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type:     CommandInitialize,
		PlayerID: "bob",
		Payload:  map[string]any{"jokerPlacements": []any{}},
	}); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusWaiting || !state.Players["bob"].InitialHandReady {
		t.Fatalf("completed initialization state = %+v", state)
	}
}

func TestDavinciRejectsInvalidInitialJokerPlacementsWithoutMutation(t *testing.T) {
	base := &Player{
		PlayerID: "alice",
		Cards: []Card{
			{ID: "two", Num: 2, Color: 0},
			{ID: "black-joker", Num: NumMinus1, Color: 1},
			{ID: "seven", Num: 7, Color: 1},
			{ID: "white-joker", Num: NumMinus1, Color: 0},
			{ID: "four", Num: 4, Color: 0},
		},
	}
	prepareInitialHand(base)
	tests := []struct {
		name    string
		payload any
	}{
		{name: "missing payload field", payload: map[string]any{}},
		{name: "missing joker", payload: map[string]any{"jokerPlacements": []map[string]any{{"id": "black-joker", "index": 0}}}},
		{name: "number card", payload: map[string]any{"jokerPlacements": []map[string]any{{"id": "two", "index": 0}, {"id": "white-joker", "index": 1}}}},
		{name: "duplicate joker", payload: map[string]any{"jokerPlacements": []map[string]any{{"id": "black-joker", "index": 0}, {"id": "black-joker", "index": 1}}}},
		{name: "duplicate index", payload: map[string]any{"jokerPlacements": []map[string]any{{"id": "black-joker", "index": 2}, {"id": "white-joker", "index": 2}}}},
		{name: "negative index", payload: map[string]any{"jokerPlacements": []map[string]any{{"id": "black-joker", "index": -1}, {"id": "white-joker", "index": 2}}}},
		{name: "large index", payload: map[string]any{"jokerPlacements": []map[string]any{{"id": "black-joker", "index": 0}, {"id": "white-joker", "index": 5}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := *base
			player.Cards = append([]Card(nil), base.Cards...)
			state := &State{
				Status:    StatusInitialize,
				PlayerSeq: []string{"alice"},
				Players:   map[string]*Player{"alice": &player},
			}
			before := append([]Card(nil), player.Cards...)
			if _, err := NewEngine().ApplyCommand(context.Background(), state, contracts.RoomCommand{
				Type: CommandInitialize, PlayerID: "alice", Payload: tt.payload,
			}); err == nil {
				t.Fatal("invalid placement was accepted")
			}
			if player.InitialHandReady || !reflect.DeepEqual(player.Cards, before) {
				t.Fatalf("rejected placement mutated player: before=%+v after=%+v", before, player)
			}
		})
	}
}

func TestDavinciInitializationDeadlineAndTimeout(t *testing.T) {
	plugin := NewPlugin()
	state := &State{
		RoomID:    "initialization-deadline",
		OwnerID:   "alice",
		Status:    StatusInitialize,
		PlayerSeq: []string{"alice", "bob"},
		Players: map[string]*Player{
			"alice": {PlayerID: "alice", Cards: []Card{{ID: "a", Num: 2, Index: 0}}},
			"bob": {
				PlayerID: "bob",
				Cards: []Card{
					{ID: "b-3", Num: 3, Index: 0},
					{ID: "b-joker", Num: NumMinus1, Color: 1, Index: -1},
				},
			},
		},
	}
	startedAt := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	updateTurnDeadline(state, startedAt)
	deadline := state.TurnDeadline
	if !deadline.Equal(startedAt.Add(initializationTimeout)) || state.TurnTimeout != initializationTimeout {
		t.Fatalf("initial deadline = %s/%s", deadline, state.TurnTimeout)
	}
	if _, err := plugin.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type:     CommandInitialize,
		PlayerID: "alice",
		SentAt:   startedAt.Add(5 * time.Second),
		Payload:  map[string]any{"jokerPlacements": []any{}},
	}); err != nil {
		t.Fatal(err)
	}
	if !state.TurnDeadline.Equal(deadline) {
		t.Fatalf("confirmation extended deadline from %s to %s", deadline, state.TurnDeadline)
	}
	pending, err := plugin.PendingCommands(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Type != CommandInitTimeout ||
		pending[0].PlayerID != "alice" || !pending[0].ExecuteAt.Equal(deadline) {
		t.Fatalf("initialization pending command = %+v", pending)
	}
	encoded, err := plugin.EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	restoredRaw, err := plugin.DecodeState(context.Background(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	state = restoredRaw.(*State)
	restoredPending, err := plugin.PendingCommands(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(restoredPending) != 1 || !restoredPending[0].ExecuteAt.Equal(deadline) {
		t.Fatalf("restored initialization pending command = %+v", restoredPending)
	}
	if _, err := plugin.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type:          CommandInitTimeout,
		PlayerID:      "alice",
		ConnectionRef: contracts.ConnectionRef{GatewayNode: "gateway", SessionID: "session"},
	}); err == nil {
		t.Fatal("client initialization timeout was accepted")
	}
	if _, err := plugin.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type: CommandInitTimeout, PlayerID: "alice", SentAt: deadline,
	}); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusWaiting || !state.TurnDeadline.IsZero() || state.TurnTimeout != 0 {
		t.Fatalf("timeout state = %+v", state)
	}
	if got := cardIndexes(state.Players["bob"].Cards); !reflect.DeepEqual(got, map[string]int{"b-3": 0, "b-joker": 1}) {
		t.Fatalf("timeout fallback indexes = %+v", got)
	}
}

func TestDavinciConnectionLifecyclePreservesTurnDeadline(t *testing.T) {
	plugin := NewPlugin()
	sentAt := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	state := &State{
		RoomID:        "reconnect-deadline",
		OwnerID:       "alice",
		Status:        StatusGetCard,
		CurrentPlayer: "alice",
		PlayerSeq:     []string{"alice", "bob"},
		Players: map[string]*Player{
			"alice": {PlayerID: "alice"},
			"bob":   {PlayerID: "bob"},
		},
		TurnDeadline: sentAt.Add(20 * time.Second),
		TurnTimeout:  20 * time.Second,
	}
	deadline := state.TurnDeadline

	for _, command := range []contracts.RoomCommand{
		{Type: CommandDisconnect, PlayerID: "alice", SentAt: sentAt.Add(5 * time.Second)},
		{Type: CommandConnect, PlayerID: "alice", SentAt: sentAt.Add(10 * time.Second)},
	} {
		if _, err := plugin.ApplyCommand(context.Background(), state, command); err != nil {
			t.Fatalf("%s: %v", command.Type, err)
		}
		if !state.TurnDeadline.Equal(deadline) {
			t.Fatalf("%s changed deadline from %s to %s", command.Type, deadline, state.TurnDeadline)
		}
	}
}

func TestDavinciInitializationViewDoesNotLeakOpponentJoker(t *testing.T) {
	state := &State{
		RoomID:    "private-initialization",
		OwnerID:   "alice",
		Status:    StatusInitialize,
		PlayerSeq: []string{"alice", "bob"},
		Players: map[string]*Player{
			"alice": {
				PlayerID:         "alice",
				InitialHandReady: false,
				Cards:            []Card{{ID: "a-2", Num: 2, Index: 0}},
			},
			"bob": {
				PlayerID:         "bob",
				InitialHandReady: true,
				Cards: []Card{
					{ID: "b-3", Num: 3, Color: 0, Index: 1},
					{ID: "b-joker", Num: NumMinus1, Color: 1, Index: 0},
				},
			},
		},
	}
	raw := NewEngine().BuildRoomSync(state, game.ViewRequest{PlayerID: "alice"})
	cardData := raw.CardData.(map[string]any)
	if cardData["initialHandReady"] != false {
		t.Fatalf("self initialization flag = %#v", cardData["initialHandReady"])
	}
	opponents := cardData["opponents"].([]map[string]any)
	for index, card := range opponents {
		if card["num"] != nil {
			t.Fatalf("opponent number leaked: %+v", card)
		}
		if card["index"] != index {
			t.Fatalf("opponent temporary index = %#v, want %d", card["index"], index)
		}
	}
}

func TestDavinciAIConfirmsInitialJokers(t *testing.T) {
	state := &State{
		RoomID:    "ai-initialization",
		Status:    StatusInitialize,
		PlayerSeq: []string{"AI_2"},
		Players: map[string]*Player{
			"AI_2": {
				PlayerID: "AI_2",
				AI:       true,
				Cards: []Card{
					{ID: "three", Num: 3, Index: 0},
					{ID: "black-joker", Num: NumMinus1, Color: 1, Index: -1},
					{ID: "white-joker", Num: NumMinus1, Color: 0, Index: -1},
				},
			},
		},
	}
	command, err := NewAIPlanner().Decide(context.Background(), game.AIRequest{
		RoomID: state.RoomID, GameType: contracts.GameDaVinci, PlayerID: "AI_2", State: state,
	})
	if err != nil {
		t.Fatal(err)
	}
	if command.Type != CommandInitialize {
		t.Fatalf("AI initialization command = %+v", command)
	}
	if _, err := NewEngine().ApplyCommand(context.Background(), state, command); err != nil {
		t.Fatal(err)
	}
	if !state.Players["AI_2"].InitialHandReady || state.Status != StatusWaiting {
		t.Fatalf("AI initialization state = %+v", state)
	}
	if got := cardIndexes(state.Players["AI_2"].Cards); !reflect.DeepEqual(got, map[string]int{
		"three": 0, "black-joker": 1, "white-joker": 2,
	}) {
		t.Fatalf("AI indexes = %+v", got)
	}
}

func TestDavinciMatchBeginSchedulesAIInitializationFallback(t *testing.T) {
	plugin := NewPlugin()
	raw, err := plugin.InitRoom(context.Background(), game.RoomConfig{
		RoomID:        "begin-initialization",
		OwnerPlayerID: "owner",
		MaxPlayers:    2,
		RandomSeed:    42,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*State)
	for _, command := range []contracts.RoomCommand{
		{Type: CommandConnect, PlayerID: "owner"},
		{Type: CommandAddAI, PlayerID: "owner", Payload: map[string]any{"seatIndex": 1}},
	} {
		if _, err := plugin.ApplyCommand(context.Background(), state, command); err != nil {
			t.Fatal(err)
		}
	}
	startedAt := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	result, err := plugin.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type: CommandMatchBegin, PlayerID: "owner", SentAt: startedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusInitialize || !state.TurnDeadline.Equal(startedAt.Add(initializationTimeout)) {
		t.Fatalf("begin state = %+v", state)
	}
	if len(result.AIRequests) != 1 || result.AIRequests[0].FallbackCommand == nil ||
		result.AIRequests[0].FallbackCommand.Type != CommandInitialize {
		t.Fatalf("AI initialization request = %+v", result.AIRequests)
	}
}

func cardIndexes(cards []Card) map[string]int {
	indexes := make(map[string]int, len(cards))
	for _, card := range cards {
		indexes[card.ID] = card.Index
	}
	return indexes
}

func cardIDs(cards []Card) []string {
	ids := make([]string, 0, len(cards))
	for _, card := range cards {
		ids = append(ids, card.ID)
	}
	return ids
}

func slicesContain(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
