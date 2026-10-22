package ai

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/davinci"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestDecideGuessDoesNotUseHiddenCardNumber(t *testing.T) {
	state := &davinci.State{
		Status:        davinci.StatusGuessCard,
		CurrentPlayer: "ai",
		Players: map[string]*davinci.Player{
			"ai": {
				PlayerID: "ai",
				AI:       true,
				Cards: []davinci.Card{
					{ID: "ai-0", Color: 0, Num: davinci.NumMinus1, IsRevealed: false, Index: 0},
					{ID: "ai-1", Color: 0, Num: 0, IsRevealed: false, Index: 1},
					{ID: "ai-2", Color: 0, Num: 1, IsRevealed: false, Index: 2},
				},
			},
			"human": {
				PlayerID: "human",
				Cards: []davinci.Card{
					{ID: "human-hidden", Color: 0, Num: 7, IsRevealed: false, Index: 0},
				},
			},
		},
	}

	cmd, err := NewService().Decide(context.Background(), game.AIRequest{PlayerID: "ai", State: state})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if cmd.Type != davinci.CommandGuessCard {
		t.Fatalf("type = %q, want %q", cmd.Type, davinci.CommandGuessCard)
	}
	payload := cmd.Payload.(map[string]any)
	if payload["id"] != "human-hidden" {
		t.Fatalf("target id = %v, want human-hidden", payload["id"])
	}
	if payload["num"] == 7 {
		t.Fatalf("AI guessed hidden real number %v; want public-info guess", payload["num"])
	}
}

func TestDecideSetCardPlacesPendingCard(t *testing.T) {
	state := &davinci.State{
		Status:        davinci.StatusSetCard,
		CurrentPlayer: "ai",
		Players: map[string]*davinci.Player{
			"ai": {
				PlayerID: "ai",
				AI:       true,
				Cards: []davinci.Card{
					{ID: "low", Color: 0, Num: 2, Index: 0},
					{ID: "pending", Color: 0, Num: 4, Index: -1},
					{ID: "high", Color: 0, Num: 8, Index: 1},
				},
			},
		},
	}

	cmd, err := NewService().Decide(context.Background(), game.AIRequest{PlayerID: "ai", State: state})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if cmd.Type != davinci.CommandSetCard {
		t.Fatalf("type = %q, want %q", cmd.Type, davinci.CommandSetCard)
	}
	payload := cmd.Payload.(map[string]any)
	if payload["id"] != "pending" || payload["index"] != 1 {
		t.Fatalf("payload = %+v, want pending at index 1", payload)
	}
}
