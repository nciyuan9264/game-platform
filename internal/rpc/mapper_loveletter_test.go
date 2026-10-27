package rpc

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestLoveLetterRoomMetrics(t *testing.T) {
	metrics := roomMetrics(contracts.RoomRecord{
		GameType:        contracts.GameLoveLetter,
		ProgressCurrent: 3,
		ProgressTotal:   5,
	})
	if metrics.Loveletter == nil ||
		metrics.Loveletter.CurrentRound != 0 ||
		metrics.Loveletter.MaxFavor != 3 ||
		metrics.Loveletter.TargetFavor != 5 ||
		metrics.Loveletter.ProgressCurrent != 3 ||
		metrics.Loveletter.ProgressTotal != 5 {
		t.Fatalf("metrics = %+v", metrics)
	}
}
