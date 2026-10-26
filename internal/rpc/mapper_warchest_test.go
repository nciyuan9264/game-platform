package rpc

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestWarChestRoomMetrics(t *testing.T) {
	metrics := roomMetrics(contracts.RoomRecord{
		GameType:        contracts.GameWarChest,
		ProgressCurrent: 5,
		ProgressTotal:   6,
	})
	if metrics.Warchest == nil ||
		metrics.Warchest.MaxControlledLocations != 5 ||
		metrics.Warchest.ProgressCurrent != 5 ||
		metrics.Warchest.ProgressTotal != 6 {
		t.Fatalf("metrics = %+v", metrics)
	}
}
