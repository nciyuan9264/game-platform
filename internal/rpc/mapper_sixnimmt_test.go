package rpc

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestSixNimmtRoomMetrics(t *testing.T) {
	metrics := roomMetrics(contracts.RoomRecord{
		GameType:        contracts.GameSixNimmt,
		ProgressCurrent: 34,
		ProgressTotal:   66,
	})
	if metrics.Sixnimmt == nil ||
		metrics.Sixnimmt.MaxPenalty != 34 ||
		metrics.Sixnimmt.TargetPenalty != 66 ||
		metrics.Sixnimmt.ProgressCurrent != 34 ||
		metrics.Sixnimmt.ProgressTotal != 66 {
		t.Fatalf("metrics = %+v", metrics)
	}
}
