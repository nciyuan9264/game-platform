package store

import (
	"database/sql"
	"testing"
	"time"
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
		*dest[2].(*string) = "acquire"
		*dest[3].(*time.Time) = now
		*dest[4].(*sql.NullTime) = sql.NullTime{Time: now, Valid: true}
		*dest[5].(*int) = 60
		*dest[6].(*sql.NullInt64) = sql.NullInt64{}
		*dest[7].(*sql.NullString) = sql.NullString{}
		*dest[8].(*sql.NullString) = sql.NullString{}
		*dest[9].(*int) = 6
		*dest[10].(*[]byte) = nil
		*dest[11].(*[]byte) = nil
		*dest[12].(*time.Time) = now
		*dest[13].(*time.Time) = now
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
		*dest[4].(*int) = 1
		*dest[5].(*bool) = false
		*dest[6].(*sql.NullInt64) = sql.NullInt64{Int64: 42, Valid: true}
		*dest[7].(*sql.NullInt64) = sql.NullInt64{}
		*dest[8].(*int) = 4
		*dest[9].(*sql.NullInt64) = sql.NullInt64{Int64: 2, Valid: true}
		*dest[10].(*bool) = true
		*dest[11].(*time.Time) = now
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
}
