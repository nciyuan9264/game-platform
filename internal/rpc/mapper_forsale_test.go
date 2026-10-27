package rpc

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestRoomMetricsMapsForSaleProgress(t *testing.T) {
	metrics := roomMetrics(contracts.RoomRecord{
		GameType:        contracts.GameForSale,
		ProgressCurrent: 5,
		ProgressTotal:   12,
	})
	if metrics.ForSale == nil ||
		metrics.ForSale.ProgressCurrent != 5 ||
		metrics.ForSale.ProgressTotal != 12 {
		t.Fatalf("metrics = %+v", metrics)
	}
}
