package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestResultCardHistoryAuthorizationIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	store, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	suffix := strconv.FormatInt(now.UnixNano(), 10)
	matchID := "result-card-" + suffix
	gameType := "result-card-" + suffix
	participantID := now.UnixNano()
	outsiderID := participantID + 1
	rank := 1
	score := 9
	gameID, err := store.StartHistoryGame(ctx, history.Game{
		RoomID:     "result-card-room-" + suffix,
		MatchID:    matchID,
		GameType:   gameType,
		StartedAt:  now,
		MaxPlayers: 2,
	}, []history.Player{{
		UserID:      &participantID,
		PlayerID:    "participant",
		DisplayName: "Participant",
		SeatIndex:   0,
	}, {
		PlayerID:    "ai_001",
		DisplayName: "AI 1",
		SeatIndex:   1,
		IsAI:        true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM games WHERE id = $1`, gameID)
	})
	if err := store.FinishHistoryGame(ctx, gameID, history.FinalizeInput{
		EndedAt:         now.Add(time.Minute),
		DurationSeconds: 60,
		EndReason:       history.EndReasonCompleted,
		Players: []history.Player{{
			UserID:      &participantID,
			PlayerID:    "participant",
			DisplayName: "Participant",
			SeatIndex:   0,
			FinalScore:  &score,
			FinalRank:   &rank,
			IsWinner:    true,
		}, {
			PlayerID:    "ai_001",
			DisplayName: "AI 1",
			SeatIndex:   1,
			IsAI:        true,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	gameRecord, players, err := store.GetResultCardGameForUser(
		ctx, matchID, participantID, gameType,
	)
	if err != nil {
		t.Fatal(err)
	}
	if gameRecord.MatchID != matchID || len(players) != 2 {
		t.Fatalf("result card = %+v/%+v", gameRecord, players)
	}
	if _, _, err := store.GetResultCardGameForUser(
		ctx, matchID, outsiderID, gameType,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("outsider error = %v, want sql.ErrNoRows", err)
	}
}
