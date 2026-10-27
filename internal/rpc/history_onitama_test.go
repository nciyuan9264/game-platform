package rpc

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestBuildReplaySnapshotSupportsOnitama(t *testing.T) {
	payload, err := history.EncodeReplayFrame(1, map[string]any{
		"roomData": map[string]any{
			"pieces":        []any{},
			"hands":         map[string]any{"blue": []string{"tiger", "crab"}, "red": []string{"dragon", "elephant"}},
			"transitCardID": "crane",
			"currentPlayer": "alice",
			"currentSide":   "blue",
			"bluePlayerID":  "alice",
			"redPlayerID":   "bob",
			"firstPlayerID": "alice",
			"roundNo":       1,
			"gameStatus":    "playing",
			"legalMoves":    []any{},
			"canPass":       false,
			"moveLog":       []any{},
			"players":       map[string]any{},
			"playerOrder":   []string{"alice", "bob"},
		},
		"playersData": map[string]any{},
		"result":      nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := (&Handler{}).buildReplaySnapshot(
		context.Background(),
		history.Game{
			GameType:             contracts.GameOnitama,
			ReplaySchemaVersion:  1,
			InitialReplayPayload: payload,
		},
		nil,
		nil,
		-1,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Onitama == nil ||
		snapshot.Onitama.Seq != -1 ||
		snapshot.Onitama.RoomData.CurrentSide != "blue" ||
		snapshot.Onitama.RoomData.TransitCardID != "crane" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
