package rpc

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestOthelloRoomMetrics(t *testing.T) {
	metrics := roomMetrics(contracts.RoomRecord{
		GameType:        contracts.GameOthello,
		ProgressCurrent: 12,
		ProgressTotal:   60,
	})
	if metrics.Othello == nil ||
		metrics.Othello.OccupiedCount != 16 ||
		metrics.Othello.ProgressCurrent != 12 ||
		metrics.Othello.ProgressTotal != 60 {
		t.Fatalf("metrics = %+v", metrics)
	}
}
