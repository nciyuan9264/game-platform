package davinci

import (
	"context"
	"fmt"
	"reflect"
	"testing"

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
