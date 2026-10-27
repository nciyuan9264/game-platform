package rpc

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestHiveRoomMetrics(t *testing.T) {
	metrics := roomMetrics(contracts.RoomRecord{
		GameType:        contracts.GameHive,
		ProgressCurrent: 17,
		ProgressTotal:   9,
	})
	if metrics.Hive == nil ||
		metrics.Hive.TurnCount != 17 ||
		metrics.Hive.PlacedPieceCount != 9 {
		t.Fatalf("metrics = %+v", metrics)
	}
}
