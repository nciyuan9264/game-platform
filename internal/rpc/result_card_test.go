package rpc

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestPrepareResultCardPlayersSortsRanksAndMarksOneSelf(t *testing.T) {
	userID := int64(7)
	firstRank := 1
	secondRank := 2
	score := 12
	players := []history.Player{
		{
			PlayerID:    "late-self",
			UserID:      &userID,
			DisplayName: "Later Self",
			SeatIndex:   3,
			FinalRank:   &secondRank,
		},
		{
			PlayerID:    "winner",
			DisplayName: "Winner",
			SeatIndex:   2,
			FinalScore:  &score,
			FinalRank:   &firstRank,
			IsWinner:    true,
		},
		{
			PlayerID:    "self",
			UserID:      &userID,
			DisplayName: "Self",
			SeatIndex:   1,
			FinalRank:   &secondRank,
		},
		{
			PlayerID:    "unranked",
			DisplayName: "Unranked",
			SeatIndex:   0,
		},
	}

	got := prepareResultCardPlayers(players, userID)
	if len(got) != 4 {
		t.Fatalf("players = %d, want 4", len(got))
	}
	if got[0].DisplayName != "Winner" || !got[0].IsWinner ||
		got[1].DisplayName != "Self" || !got[1].IsSelf ||
		got[2].DisplayName != "Later Self" || got[2].IsSelf ||
		got[3].DisplayName != "Unranked" {
		t.Fatalf("mapped players = %+v", got)
	}
	if got[0].FinalScore == nil || *got[0].FinalScore != 12 {
		t.Fatalf("winner score = %v, want 12", got[0].FinalScore)
	}
}
