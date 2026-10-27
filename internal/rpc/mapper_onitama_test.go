package rpc

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestOnitamaRoomMetrics(t *testing.T) {
	metrics := roomMetrics(contracts.RoomRecord{
		GameType:        contracts.GameOnitama,
		ProgressCurrent: 23,
	})
	if metrics.Onitama == nil ||
		metrics.Onitama.TurnCount != 23 {
		t.Fatalf("metrics = %+v", metrics)
	}
}
