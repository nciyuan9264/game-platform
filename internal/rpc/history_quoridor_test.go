package rpc

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestBuildReplaySnapshotSupportsQuoridor(t *testing.T) {
	payload, err := history.EncodeReplayFrame(1, map[string]any{
		"roomData": map[string]any{
			"pawns": map[string]any{
				"south": map[string]any{"row": 8, "column": 4},
				"north": map[string]any{"row": 0, "column": 4},
			},
			"walls":          []any{},
			"wallStock":      map[string]int{"south": 10, "north": 10},
			"currentPlayer":  "alice",
			"currentSide":    "south",
			"southPlayerID":  "alice",
			"northPlayerID":  "bob",
			"firstPlayerID":  "alice",
			"roundNo":        1,
			"gameStatus":     "playing",
			"legalMoves":     []any{},
			"wallOptions":    []any{},
			"referencePaths": map[string]any{"south": []any{}, "north": []any{}},
			"moveLog":        []any{},
			"players":        map[string]any{},
			"playerOrder":    []string{"alice", "bob"},
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
			GameType:             contracts.GameQuoridor,
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
	if snapshot.Quoridor == nil ||
		snapshot.Quoridor.Seq != -1 ||
		snapshot.Quoridor.RoomData.CurrentSide != "south" ||
		snapshot.Quoridor.RoomData.WallStock["south"] != 10 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
