package rpc

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestBuildReplaySnapshotSupportsSixNimmt(t *testing.T) {
	payload, err := history.EncodeReplayFrame(1, map[string]any{
		"roomData": map[string]any{
			"gameStatus": "selecting",
			"roundNo":    2,
			"turnNo":     4,
			"rows": []any{
				map[string]any{"index": 0, "cards": []int{12}, "bullheads": 1},
				map[string]any{"index": 1, "cards": []int{30}, "bullheads": 3},
				map[string]any{"index": 2, "cards": []int{61}, "bullheads": 1},
				map[string]any{"index": 3, "cards": []int{79}, "bullheads": 1},
			},
			"lockedPlayerIDs": []string{},
			"activityLog":     []any{},
			"players":         map[string]any{},
			"playerOrder":     []string{"alice", "bob"},
			"turnTimeoutMs":   60000,
		},
		"playersData": map[string]any{
			"alice": map[string]any{
				"roundPenalty": 2,
				"totalPenalty": 7,
				"handCount":    6,
				"takenCount":   2,
				"locked":       false,
			},
		},
		"privateData": map[string]any{
			"alice": map[string]any{
				"hand":       []int{24, 55},
				"legalCards": []int{24, 55},
			},
		},
		"unusedCards": []int{1, 2, 3},
		"result":      nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := (&Handler{}).buildReplaySnapshot(
		context.Background(),
		history.Game{
			GameType:             contracts.GameSixNimmt,
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
	if snapshot.Sixnimmt == nil ||
		snapshot.Sixnimmt.Seq != -1 ||
		snapshot.Sixnimmt.RoomData.RoundNo != 2 ||
		len(snapshot.Sixnimmt.RoomData.Rows) != 4 ||
		len(snapshot.Sixnimmt.UnusedCards) != 3 ||
		snapshot.Sixnimmt.PrivateData["alice"].Hand[1] != 55 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
