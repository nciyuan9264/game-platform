package rpc

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestRoomMetricsMapsYinshProgress(t *testing.T) {
	metrics := roomMetrics(contracts.RoomRecord{
		GameType:        contracts.GameYinsh,
		ProgressCurrent: 2,
		ProgressTotal:   3,
	})
	if metrics.Yinsh == nil ||
		metrics.Yinsh.RingsRemoved != 2 ||
		metrics.Yinsh.ProgressCurrent != 2 ||
		metrics.Yinsh.ProgressTotal != 3 {
		t.Fatalf("metrics = %+v", metrics)
	}
}
