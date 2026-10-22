package roompkg

import (
	"encoding/json"
	"testing"

	"github.com/nciyuan9264/game-platform-server/internal/acquire/domain/domain"
)

func TestPayloadForActionMatchesExistingSchema(t *testing.T) {
	tests := []struct {
		name     string
		action   aiAction
		cmdType  string
		wantKeys []string
	}{
		{
			name:     "place tile",
			action:   aiAction{Kind: aiActionPlaceTile, TileKey: "3A"},
			cmdType:  "game_place_tile",
			wantKeys: []string{"tileKey"},
		},
		{
			name:     "create company",
			action:   aiAction{Kind: aiActionCreateCompany, Company: "Tower"},
			cmdType:  "game_create_company",
			wantKeys: []string{"company"},
		},
		{
			name:     "buy stock",
			action:   aiAction{Kind: aiActionBuyStock, Stocks: map[string]int{"Tower": 2}},
			cmdType:  "game_buy_stock",
			wantKeys: []string{"stocks"},
		},
		{
			name:     "merge selection",
			action:   aiAction{Kind: aiActionMergeSelection, MainCompany: "Tower"},
			cmdType:  "game_merging_selection",
			wantKeys: []string{"mainCompany"},
		},
		{
			name: "merge settle",
			action: aiAction{
				Kind: aiActionMergeSettle,
				SettleActions: []domain.MergingSettleItem{{
					Company:    "Sackson",
					SellAmount: 1,
				}},
			},
			cmdType:  "game_merging_settle",
			wantKeys: []string{"actions"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmdType, payload, ok := payloadForAction(tt.action)
			if !ok {
				t.Fatalf("payloadForAction returned ok=false")
			}
			if cmdType != tt.cmdType {
				t.Fatalf("cmdType = %q, want %q", cmdType, tt.cmdType)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatalf("payload is invalid json: %v", err)
			}
			for _, key := range tt.wantKeys {
				if _, ok := got[key]; !ok {
					t.Fatalf("payload missing key %q: %s", key, string(payload))
				}
			}
		})
	}
}

func TestEnumerateActionsFiltersIllegalSafeMergeTiles(t *testing.T) {
	r := newAITestRoom()
	r.State.Players["ai_001"].Tiles = []string{"2B", "9I"}
	r.State.BoardTiles["1B"] = &domain.Tile{ID: "1B", Belong: "Tower"}
	r.State.BoardTiles["3B"] = &domain.Tile{ID: "3B", Belong: "Sackson"}
	r.State.Companies["Tower"].Tiles = aiSafeCompanyTiles
	r.State.Companies["Sackson"].Tiles = aiSafeCompanyTiles

	actions := enumerateActions(r, "ai_001", domain.RoomStatusSetTile, nil, 10)
	for _, action := range actions {
		if action.Kind == aiActionPlaceTile && action.TileKey == "2B" {
			t.Fatalf("illegal safe merge tile was enumerated")
		}
	}
	if len(actions) != 1 || actions[0].TileKey != "9I" {
		t.Fatalf("actions = %+v, want only legal fallback tile 9I", actions)
	}
}

func TestEnumerateActionsIncludesAffordableStockCombinations(t *testing.T) {
	r := newAITestRoom()
	r.State.RoomStatus = domain.RoomStatusBuyStock
	r.State.CurrentPlayer = "ai_001"
	r.State.Players["ai_001"].Money = 600
	r.State.Companies["Tower"].Tiles = 2
	r.State.Companies["Tower"].StockPrice = 200
	r.State.Companies["Tower"].StockTotal = 25
	r.State.Companies["Sackson"].Tiles = 3
	r.State.Companies["Sackson"].StockPrice = 300
	r.State.Companies["Sackson"].StockTotal = 25

	actions := enumerateActions(r, "ai_001", domain.RoomStatusBuyStock, nil, 20)
	if len(actions) == 0 {
		t.Fatalf("expected at least one buy stock action")
	}
	for _, action := range actions {
		if action.Kind != aiActionBuyStock {
			t.Fatalf("unexpected action kind %q", action.Kind)
		}
		totalCount := 0
		totalCost := 0
		for company, count := range action.Stocks {
			totalCount += count
			totalCost += count * r.State.Companies[company].StockPrice
		}
		if totalCount > aiMaxStockPerTurn {
			t.Fatalf("stock count = %d, want <= %d", totalCount, aiMaxStockPerTurn)
		}
		if totalCost > r.State.Players["ai_001"].Money {
			t.Fatalf("stock cost = %d, money = %d", totalCost, r.State.Players["ai_001"].Money)
		}
	}
}

func TestMaybeRunAIIfNeededIgnoresEndedRoom(t *testing.T) {
	r := newAITestRoom()
	r.State.RoomStatus = domain.RoomStatusEnd
	r.State.CurrentPlayer = "ai_001"

	msg := map[string]interface{}{
		"type":     "ROOM_SYNC",
		"playerId": "ai_001",
		"roomData": map[string]interface{}{
			"currentPlayer": "ai_001",
			"gameStatus":    string(domain.RoomStatusEnd),
		},
		"tempData": map[string]interface{}{},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}

	if MaybeRunAIIfNeeded(r, data) {
		t.Fatalf("MaybeRunAIIfNeeded returned true for ended room")
	}
	if r.AIRunning {
		t.Fatalf("AIRunning = true, want false")
	}
}

func TestBuildTurnTimeoutCommandIgnoresEndedRoom(t *testing.T) {
	r := newAITestRoom()
	r.State.RoomStatus = domain.RoomStatusEnd

	if cmd, ok := BuildTurnTimeoutCommand(r, "ai_001"); ok {
		t.Fatalf("BuildTurnTimeoutCommand returned command for ended room: %+v", cmd)
	}
}
