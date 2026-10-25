package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

type historyScannerFunc func(dest ...any) error

func (scan historyScannerFunc) Scan(dest ...any) error {
	return scan(dest...)
}

func TestScanHistoryGameHandlesNullableLegacyStrings(t *testing.T) {
	now := time.Now().UTC()
	scanner := historyScannerFunc(func(dest ...any) error {
		*dest[0].(*int64) = 1
		*dest[1].(*string) = "room-1"
		*dest[2].(*string) = "Board room"
		*dest[3].(*string) = "acquire"
		*dest[4].(*string) = "acquire-v2"
		*dest[5].(*int) = 1
		*dest[6].(*time.Time) = now
		*dest[7].(*sql.NullTime) = sql.NullTime{Time: now, Valid: true}
		*dest[8].(*int) = 60
		*dest[9].(*sql.NullInt64) = sql.NullInt64{}
		*dest[10].(*sql.NullString) = sql.NullString{}
		*dest[11].(*sql.NullString) = sql.NullString{}
		*dest[12].(*int) = 6
		*dest[13].(*[]byte) = nil
		*dest[14].(*[]byte) = nil
		*dest[15].(*[]byte) = nil
		*dest[16].(*time.Time) = now
		*dest[17].(*time.Time) = now
		return nil
	})

	gameRecord, err := scanHistoryGame(scanner)
	if err != nil {
		t.Fatal(err)
	}
	if gameRecord.WinnerPlayerID != "" || gameRecord.EndReason != "" {
		t.Fatalf("legacy strings = %q/%q", gameRecord.WinnerPlayerID, gameRecord.EndReason)
	}
}

func TestScanHistoryPlayerMapsNullableValues(t *testing.T) {
	now := time.Now().UTC()
	scanner := historyScannerFunc(func(dest ...any) error {
		*dest[0].(*int64) = 7
		*dest[1].(*int64) = 3
		*dest[2].(*sql.NullInt64) = sql.NullInt64{Int64: 11, Valid: true}
		*dest[3].(*string) = "player-11"
		*dest[4].(*string) = "Alice-Cooper-11"
		*dest[5].(*string) = "fugitive"
		*dest[6].(*int) = 1
		*dest[7].(*bool) = false
		*dest[8].(*sql.NullInt64) = sql.NullInt64{Int64: 42, Valid: true}
		*dest[9].(*sql.NullInt64) = sql.NullInt64{}
		*dest[10].(*int) = 4
		*dest[11].(*sql.NullInt64) = sql.NullInt64{Int64: 2, Valid: true}
		*dest[12].(*bool) = true
		*dest[13].(*time.Time) = now
		return nil
	})

	player, err := scanHistoryPlayer(scanner)
	if err != nil {
		t.Fatal(err)
	}
	if player.UserID == nil || *player.UserID != 11 || player.FinalScore == nil || *player.FinalScore != 42 {
		t.Fatalf("nullable player values = %+v", player)
	}
	if player.FinalMoney != nil || player.FinalRank == nil || *player.FinalRank != 2 {
		t.Fatalf("nullable result values = %+v", player)
	}
	if player.DisplayName != "Alice-Cooper-11" {
		t.Fatalf("display name = %q", player.DisplayName)
	}
}

func TestWinRateLeaderboardGames(t *testing.T) {
	if !usesWinRateLeaderboard(contracts.GameDaVinci) ||
		!usesWinRateLeaderboard(contracts.GameYinsh) ||
		!usesWinRateLeaderboard(contracts.GameOthello) ||
		!usesWinRateLeaderboard(contracts.GameFugitive) ||
		usesWinRateLeaderboard(contracts.GameAcquire) ||
		usesWinRateLeaderboard(contracts.GameSplendor) {
		t.Fatal("win-rate leaderboard routing does not match game semantics")
	}
}
