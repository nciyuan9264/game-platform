package store

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestHistoryLeaderboardPrefersLatestDisplayNameIntegration(t *testing.T) {
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
	t.Cleanup(func() {
		_ = store.Close()
	})

	now := time.Now().UTC()
	suffix := strconv.FormatInt(now.UnixNano(), 10)
	gameType := "leaderboard-display-name-" + suffix
	userID := now.UnixNano()
	fallbackUserID := userID + 1
	gameIDs := make([]int64, 0, 3)
	t.Cleanup(func() {
		for _, gameID := range gameIDs {
			_, _ = store.db.ExecContext(context.Background(), `DELETE FROM games WHERE id = $1`, gameID)
		}
	})

	finishGame := func(roomID, playerID, displayName string, playerUserID int64, startedAt time.Time) {
		t.Helper()
		rank := 1
		gameID, startErr := store.StartHistoryGame(ctx, history.Game{
			RoomID:     roomID,
			GameType:   gameType,
			StartedAt:  startedAt,
			MaxPlayers: 2,
			CreatedAt:  startedAt,
			UpdatedAt:  startedAt,
		}, []history.Player{{
			UserID:      &playerUserID,
			PlayerID:    playerID,
			DisplayName: displayName,
			SeatIndex:   0,
		}})
		if startErr != nil {
			t.Fatal(startErr)
		}
		gameIDs = append(gameIDs, gameID)
		if finishErr := store.FinishHistoryGame(ctx, gameID, history.FinalizeInput{
			EndedAt:         startedAt.Add(time.Minute),
			DurationSeconds: 60,
			EndReason:       history.EndReasonCompleted,
			Players: []history.Player{{
				UserID:      &playerUserID,
				PlayerID:    playerID,
				DisplayName: displayName,
				SeatIndex:   0,
				FinalRank:   &rank,
				IsWinner:    true,
			}},
		}); finishErr != nil {
			t.Fatal(finishErr)
		}
	}

	oldStartedAt := now.Add(-2 * time.Hour)
	newStartedAt := now.Add(-time.Hour)
	userIDText := strconv.FormatInt(userID, 10)
	finishGame("leaderboard-old-room-"+suffix, "Legacy Name-"+userIDText, "Legacy Name", userID, oldStartedAt)
	finishGame("leaderboard-new-room-"+suffix, userIDText, "Current Name", userID, newStartedAt)
	finishGame(
		"leaderboard-fallback-room-"+suffix,
		strconv.FormatInt(fallbackUserID, 10),
		"",
		fallbackUserID,
		now,
	)

	entries, err := store.HistoryLeaderboardSorted(ctx, gameType, "metric", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	entriesByUserID := make(map[int64]history.LeaderboardEntry, len(entries))
	for _, entry := range entries {
		entriesByUserID[entry.UserID] = entry
	}
	entry, ok := entriesByUserID[userID]
	if !ok {
		t.Fatalf("leaderboard entry for user %d not found: %+v", userID, entries)
	}
	if entry.PlayerID != userIDText || entry.DisplayName != "Current Name" || entry.TotalGames != 2 {
		t.Fatalf("entry = %+v, want canonical ID, latest display name, and 2 games", entry)
	}
	fallback, ok := entriesByUserID[fallbackUserID]
	if !ok {
		t.Fatalf("fallback entry for user %d not found: %+v", fallbackUserID, entries)
	}
	wantFallback := strconv.FormatInt(fallbackUserID, 10)
	if fallback.PlayerID != wantFallback || fallback.DisplayName != "" {
		t.Fatalf("fallback entry = %+v, want canonical ID with empty local display name", fallback)
	}
}
