package rpc

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestHexRoomMetrics(t *testing.T) {
	metrics := roomMetrics(contracts.RoomRecord{
		GameType:        contracts.GameHex,
		ProgressCurrent: 17,
		ProgressTotal:   0,
	})
	if metrics.Hex == nil ||
		metrics.Hex.TurnCount != 17 {
		t.Fatalf("metrics = %+v", metrics)
	}
}
