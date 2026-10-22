package roompkg

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
)

func TestSimulateActionDoesNotMutateOriginalRoom(t *testing.T) {
	original := newAITestRoom()
	original.State.CurrentPlayer = "ai_001"
	original.State.RoomStatus = domain.RoomStatusSetTile
	original.State.Players["ai_001"].Tiles = []string{"3A", "3B"}

	sim, ok := simulateAction(original, "ai_001", aiAction{Kind: aiActionPlaceTile, TileKey: "3A"})
	if !ok {
		t.Fatalf("simulateAction returned ok=false")
	}
	if sim == original {
		t.Fatalf("simulation reused original room")
	}
	if sim.State.BoardTiles["3A"].Belong == "" {
		t.Fatalf("simulation did not place tile")
	}
	if original.State.BoardTiles["3A"].Belong != "" {
		t.Fatalf("original board was mutated")
	}
	if len(original.State.Players["ai_001"].Tiles) != 2 {
		t.Fatalf("original hand was mutated: %+v", original.State.Players["ai_001"].Tiles)
	}
}

func TestSimulateBuyStockAdvancesOnlySimulation(t *testing.T) {
	original := newAITestRoom()
	original.State.CurrentPlayer = "ai_001"
	original.State.RoomStatus = domain.RoomStatusBuyStock
	original.State.Players["ai_001"].Money = 1000
	original.State.Players["ai_001"].Stocks["Tower"] = 1
	original.State.Companies["Tower"].Tiles = 2
	original.State.Companies["Tower"].StockPrice = 200
	original.State.Companies["Tower"].StockTotal = 24

	sim, ok := simulateAction(original, "ai_001", aiAction{
		Kind:   aiActionBuyStock,
		Stocks: map[string]int{"Tower": 1},
	})
	if !ok {
		t.Fatalf("simulateAction returned ok=false")
	}
	if sim.State.Players["ai_001"].Money != 800 {
		t.Fatalf("simulated money = %d, want 800", sim.State.Players["ai_001"].Money)
	}
	if sim.State.Players["ai_001"].Stocks["Tower"] != 2 {
		t.Fatalf("simulated Tower stocks = %d, want 2", sim.State.Players["ai_001"].Stocks["Tower"])
	}
	if original.State.Players["ai_001"].Money != 1000 {
		t.Fatalf("original money was mutated")
	}
	if original.State.Players["ai_001"].Stocks["Tower"] != 1 {
		t.Fatalf("original stock was mutated")
	}
	if original.State.CurrentPlayer != "ai_001" {
		t.Fatalf("original current player was mutated")
	}
}
