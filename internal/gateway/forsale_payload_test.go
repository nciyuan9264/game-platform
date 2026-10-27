package gateway

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestForSaleClientCommandPayloads(t *testing.T) {
	got, err := clientCommandPayload(
		contracts.GameForSale,
		"game_bid",
		map[string]any{
			"forSale": map[string]any{
				"bid": map[string]any{"amount": float64(7)},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := got.(map[string]any)
	if !ok || payload["amount"] != float64(7) {
		t.Fatalf("bid payload = %#v", got)
	}

	got, err = clientCommandPayload(
		contracts.GameForSale,
		"game_lock_sale",
		map[string]any{
			"forSale": map[string]any{
				"lockSale": map[string]any{"property": float64(24)},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok = got.(map[string]any)
	if !ok || payload["property"] != float64(24) {
		t.Fatalf("lock payload = %#v", got)
	}

	for command, variant := range map[string]string{
		"game_pass":          "pass",
		"game_reveal_sale":   "revealSale",
		"game_next_round":    "nextRound",
		"game_start_selling": "startSelling",
	} {
		got, err = clientCommandPayload(
			contracts.GameForSale,
			command,
			map[string]any{
				"forSale": map[string]any{
					variant: map[string]any{},
				},
			},
		)
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		if _, ok := got.(map[string]any); !ok {
			t.Fatalf("%s payload = %#v", command, got)
		}
	}

	if _, err := clientCommandPayload(
		contracts.GameForSale,
		"game_bid",
		map[string]any{"forSale": map[string]any{}},
	); err == nil {
		t.Fatal("missing for sale union variant was accepted")
	}
}
