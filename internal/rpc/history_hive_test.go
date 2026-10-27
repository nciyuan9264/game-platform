package rpc

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestBuildReplaySnapshotSupportsHive(t *testing.T) {
	payload, err := history.EncodeReplayFrame(1, map[string]any{
		"roomData": map[string]any{
			"stacks":        []any{},
			"currentPlayer": "alice",
			"currentSide":   "white",
			"whitePlayerID": "alice",
			"blackPlayerID": "bob",
			"firstPlayerID": "alice",
			"roundNo":       1,
			"gameStatus":    "playing",
			"legalActions":  []any{},
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
			GameType:             contracts.GameHive,
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
	if snapshot.Hive == nil ||
		snapshot.Hive.Seq != -1 ||
		snapshot.Hive.RoomData.CurrentSide != "white" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
