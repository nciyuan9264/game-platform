package main

import (
	"encoding/json"
	"testing"
)

func TestDecideActionSkipsWhenNotCurrentPlayer(t *testing.T) {
	sync := roomSync{
		PlayerID: "lt-room-p1",
		RoomData: roomData{
			CurrentPlayer: "lt-room-p2",
			GameStatus:    "setTile",
		},
	}

	_, ok := decideAction(sync)

	if ok {
		t.Fatalf("decideAction returned an action for non-current player")
	}
}

func TestDecideActionPlacesFirstTile(t *testing.T) {
	sync := roomSync{
		PlayerID: "lt-room-p1",
		PlayerData: playerData{
			Tiles: []string{"1A", "2B"},
		},
		RoomData: roomData{
			CurrentPlayer: "lt-room-p1",
			GameStatus:    "setTile",
		},
	}

	msg, ok := decideAction(sync)

	if !ok {
		t.Fatalf("decideAction returned no action")
	}
	assertMessage(t, msg, "game_place_tile", map[string]any{"tileKey": "1A"})
}

func TestDecideActionCreatesFirstEmptyCompany(t *testing.T) {
	sync := roomSync{
		PlayerID: "lt-room-p1",
		RoomData: roomData{
			CurrentPlayer: "lt-room-p1",
			GameStatus:    "createCompany",
			CompanyInfo: map[string]companyInfo{
				"Tower":   {Tiles: 2, StockTotal: 20, StockPrice: 200},
				"Sackson": {Tiles: 0, StockTotal: 25, StockPrice: 200},
			},
		},
	}

	msg, ok := decideAction(sync)

	if !ok {
		t.Fatalf("decideAction returned no action")
	}
	assertMessage(t, msg, "game_create_company", map[string]any{"company": "Sackson"})
}

func TestDecideActionBuysAffordableStockWithMaxThreeShares(t *testing.T) {
	sync := roomSync{
		PlayerID: "lt-room-p1",
		PlayerData: playerData{
			Money: 500,
		},
		RoomData: roomData{
			CurrentPlayer: "lt-room-p1",
			GameStatus:    "buyStock",
			CompanyInfo: map[string]companyInfo{
				"Sackson": {Tiles: 2, StockTotal: 25, StockPrice: 200},
				"Tower":   {Tiles: 3, StockTotal: 25, StockPrice: 200},
			},
		},
	}

	msg, ok := decideAction(sync)

	if !ok {
		t.Fatalf("decideAction returned no action")
	}
	assertMessage(t, msg, "game_buy_stock", map[string]any{
		"stocks": map[string]any{
			"Sackson": float64(2),
			"Tower":   float64(0),
		},
	})
}

func TestActionKeyChangesWhenTurnStateChanges(t *testing.T) {
	first := roomSync{
		PlayerID: "lt-room-p1",
		PlayerData: playerData{
			Tiles: []string{"1A"},
		},
		RoomData: roomData{
			CurrentPlayer: "lt-room-p1",
			GameStatus:    "setTile",
		},
	}
	second := first
	second.PlayerData.Tiles = []string{"2B"}

	if actionKey(first) == "" {
		t.Fatalf("action key should not be empty")
	}
	if actionKey(first) == actionKey(second) {
		t.Fatalf("action key should change when first tile changes")
	}
}

func assertMessage(t *testing.T, raw []byte, wantType string, wantPayload map[string]any) {
	t.Helper()
	var got struct {
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if got.Type != wantType {
		t.Fatalf("type = %q, want %q", got.Type, wantType)
	}
	for key, want := range wantPayload {
		gotValue, ok := got.Payload[key]
		if !ok {
			t.Fatalf("payload missing key %q in %#v", key, got.Payload)
		}
		if jsonString(gotValue) != jsonString(want) {
			t.Fatalf("payload[%s] = %s, want %s", key, jsonString(gotValue), jsonString(want))
		}
	}
}

func jsonString(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
