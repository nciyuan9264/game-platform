package main

import (
	"encoding/json"
	"testing"
)

func TestSelectFirstForSaleActionPassesOnHumanAuctionTurn(t *testing.T) {
	view, err := json.Marshal(map[string]any{
		"playerId": "7",
		"roomData": map[string]any{
			"currentPlayer":  "7",
			"gameStatus":     "auction",
			"propertyMarket": []int{4, 12, 19},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	action, ok := selectFirstAction(
		"test-run",
		"forsale",
		"room-1-r1",
		"7",
		realtimeEnvelope{
			Type: "ROOM_SYNC",
			View: map[string]json.RawMessage{"forSale": view},
		},
	)
	if !ok ||
		action.Command.Type != "game_pass" ||
		action.ConflictCommand.Type != "game_bid" ||
		action.Command.CommandID != action.ConflictCommand.CommandID {
		t.Fatalf("action = %+v/%v", action, ok)
	}
	group, ok := action.Command.Payload["forSale"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %+v", action.Command.Payload)
	}
	if _, ok := group["pass"]; !ok {
		t.Fatalf("pass payload = %+v", group)
	}
}
