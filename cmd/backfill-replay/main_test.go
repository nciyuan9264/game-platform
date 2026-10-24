package main

import (
	"encoding/json"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestLegacyFrameMaterializationPreservesReplayEnvelope(t *testing.T) {
	legacy := map[string]any{
		"seq":         3,
		"totalEvents": 7,
		"currentEvent": map[string]any{
			"seq":         3,
			"playerID":    "alice",
			"cmdType":     "game_move",
			"payloadJSON": `{"value":1}`,
		},
		"roomData":    map[string]any{"status": "playing"},
		"playersData": map[string]any{"alice": map[string]any{"score": 2}},
		"result":      map[string]any{},
	}
	payload, err := encodeLegacyFrame(1, legacy)
	if err != nil {
		t.Fatal(err)
	}
	event := &history.Event{
		Seq:         3,
		PlayerID:    "alice",
		CommandType: "game_move",
		Payload:     json.RawMessage(`{"value":1}`),
	}
	if err := compareMaterializedReplay(1, payload, 3, 7, event, legacy); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalJSONIgnoresObjectKeyOrder(t *testing.T) {
	left, err := canonicalJSON([]byte(`{"a":1,"b":{"x":2,"y":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := canonicalJSON([]byte(`{"b":{"y":3,"x":2},"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(left) != string(right) {
		t.Fatalf("canonical JSON differs: %s != %s", left, right)
	}
}
