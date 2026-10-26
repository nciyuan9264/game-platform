package rpc

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestQuoridorRoomMetrics(t *testing.T) {
	metrics := roomMetrics(contracts.RoomRecord{
		GameType:        contracts.GameQuoridor,
		ProgressCurrent: 17,
		ProgressTotal:   0,
	})
	if metrics.Quoridor == nil ||
		metrics.Quoridor.TurnCount != 17 {
		t.Fatalf("metrics = %+v", metrics)
	}
}
