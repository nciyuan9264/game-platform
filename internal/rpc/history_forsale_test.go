package rpc

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestBuildReplaySnapshotSupportsForSale(t *testing.T) {
	payload, err := history.EncodeReplayFrame(1, map[string]any{
		"roomData": map[string]any{
			"gameStatus":     "auction",
			"round":          2,
			"totalRounds":    7,
			"propertyMarket": []int{4, 12, 19, 27},
			"chequeMarket":   []int{},
			"highestBid":     3,
			"currentPlayer":  "alice",
			"starterPlayer":  "bob",
			"lockedCount":    0,
			"purchases":      []any{},
			"sales":          []any{},
			"actionLog":      []any{},
			"players":        map[string]any{},
			"playerOrder":    []string{"alice", "bob", "carol", "dave"},
		},
		"playersData": map[string]any{
			"alice": map[string]any{
				"seatIndex": 0, "bid": 3, "passed": false,
				"propertyCount": 1, "chequeCount": 0, "locked": false,
				"coins": 18, "properties": []int{10}, "cheques": []int{},
			},
		},
		"result": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := (&Handler{}).buildReplaySnapshot(
		context.Background(),
		history.Game{
			GameType:             contracts.GameForSale,
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
	if snapshot.ForSale == nil ||
		snapshot.ForSale.Seq != -1 ||
		snapshot.ForSale.RoomData.Round != 2 ||
		snapshot.ForSale.RoomData.TotalRounds != 7 ||
		snapshot.ForSale.PlayersData["alice"].GetCoins() != 18 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
