package rpc

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestBuildReplaySnapshotSupportsHex(t *testing.T) {
	payload, err := history.EncodeReplayFrame(1, map[string]any{
		"roomData": map[string]any{
			"boardSize":     11,
			"board":         make([]string, 121),
			"currentPlayer": "alice",
			"currentSide":   "red",
			"redPlayerID":   "alice",
			"tealPlayerID":  "bob",
			"firstPlayerID": "alice",
			"roundNo":       1,
			"gameStatus":    "playing",
			"pieRule":       true,
			"swapAvailable": false,
			"legalMoves":    []any{},
			"winningPath":   []any{},
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
			GameType:             contracts.GameHex,
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
	if snapshot.Hex == nil ||
		snapshot.Hex.Seq != -1 ||
		snapshot.Hex.RoomData.CurrentSide != "red" ||
		snapshot.Hex.RoomData.BoardSize != 11 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
