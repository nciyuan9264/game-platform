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
	state.PlayerSeq[0] = "changed"
	if view.PlayerOrder[0] != "alice-1" {
		t.Fatalf("player order aliases mutable state: %v", view.PlayerOrder)
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

func slicesContain(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
