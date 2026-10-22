package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/nciyuan9264/game-platform/internal/history"
)

func (s *PostgresStore) StartHistoryGame(ctx context.Context, gameRecord history.Game, players []history.Player) (int64, error) {
	if gameRecord.StartedAt.IsZero() {
		gameRecord.StartedAt = time.Now().UTC()
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var gameID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO games (
			room_id, game_type, started_at, max_players, initial_state, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$6)
		RETURNING id`,
		gameRecord.RoomID, gameRecord.GameType, gameRecord.StartedAt, gameRecord.MaxPlayers,
		nullableJSON(gameRecord.InitialState), now,
	).Scan(&gameID)
	if err != nil {
		return 0, err
	}
	for _, player := range players {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO game_players (
				game_id, user_id, player_id, seat_index, is_ai, final_stocks, is_winner, created_at
			) VALUES ($1,$2,$3,$4,$5,0,false,$6)`,
			gameID, nullableInt64(player.UserID), player.PlayerID, player.SeatIndex, player.IsAI, now)
		if err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return gameID, nil
}

func (s *PostgresStore) OpenHistoryGame(ctx context.Context, roomID, gameType string) (int64, int, error) {
	var gameID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id
		FROM games
		WHERE room_id = $1 AND game_type = $2 AND ended_at IS NULL
		ORDER BY id DESC
		LIMIT 1`, roomID, gameType).Scan(&gameID)
	if err != nil {
		return 0, 0, err
	}
	var nextSeq int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq) + 1, 0) FROM game_events WHERE game_id = $1`, gameID).Scan(&nextSeq); err != nil {
		return 0, 0, err
	}
	return gameID, nextSeq, nil
}

func (s *PostgresStore) ResetOpenHistoryGame(ctx context.Context, roomID, gameType string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM games
		WHERE room_id = $1
			AND game_type = $2
			AND ended_at IS NULL`, roomID, gameType)
	return err
}

func (s *PostgresStore) AppendHistoryEvent(ctx context.Context, event history.Event) error {
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage("null")
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO game_events (
			game_id, seq, occurred_at, player_id, cmd_type, payload, state_snapshot, state_blob
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (game_id, seq) DO NOTHING`,
		event.GameID, event.Seq, event.OccurredAt, event.PlayerID, event.CommandType,
		nullableJSON(event.Payload), nullableJSON(event.StateSnapshot), nullableBytes(event.StateBlob))
	return err
}

func (s *PostgresStore) FinishHistoryGame(ctx context.Context, gameID int64, input history.FinalizeInput) error {
	if input.EndedAt.IsZero() {
		input.EndedAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		UPDATE games SET
			ended_at = $2,
			duration_seconds = $3,
			winner_user_id = $4,
			winner_player_id = $5,
			end_reason = $6,
			final_result = $7,
			updated_at = $2
		WHERE id = $1`,
		gameID, input.EndedAt, input.DurationSeconds, input.WinnerUserID,
		input.WinnerPlayerID, input.EndReason, nullableJSON(input.FinalResult))
	if err != nil {
		return err
	}
	for _, player := range input.Players {
		_, err = tx.ExecContext(ctx, `
			UPDATE game_players SET
				user_id = COALESCE($3, user_id),
				final_score = $4,
				final_money = $5,
				final_stocks = $6,
				final_rank = $7,
				is_winner = $8
			WHERE game_id = $1 AND player_id = $2`,
			gameID, player.PlayerID, nullableInt64(player.UserID), player.FinalScore,
			player.FinalMoney, player.FinalStocks, player.FinalRank, player.IsWinner)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) DiscardHistoryGame(ctx context.Context, gameID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM games WHERE id = $1 AND ended_at IS NULL`, gameID)
	return err
}

func (s *PostgresStore) ListHistoryGames(ctx context.Context, userID int64, gameType string, limit, offset int) ([]history.Game, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT g.id, g.room_id, g.game_type, g.started_at, g.ended_at,
			g.duration_seconds, g.winner_user_id, g.winner_player_id, g.end_reason,
			g.max_players, g.initial_state, g.final_result, g.created_at, g.updated_at
		FROM games g
		JOIN game_players gp ON gp.game_id = g.id
		WHERE gp.user_id = $1 AND g.game_type = $2 AND g.ended_at IS NOT NULL
		ORDER BY g.started_at DESC
		LIMIT $3 OFFSET $4`, userID, gameType, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	games := []history.Game{}
	for rows.Next() {
		gameRecord, err := scanHistoryGame(rows)
		if err != nil {
			return nil, err
		}
		players, err := s.HistoryPlayers(ctx, gameRecord.ID)
		if err != nil {
			return nil, err
		}
		gameRecord.Players = players
		games = append(games, gameRecord)
	}
	return games, rows.Err()
}

func (s *PostgresStore) CountHistoryGames(ctx context.Context, userID int64, gameType string) (int, error) {
	var total int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT g.id)
		FROM games g
		JOIN game_players gp ON gp.game_id = g.id
		WHERE gp.user_id = $1 AND g.game_type = $2 AND g.ended_at IS NOT NULL`,
		userID, gameType).Scan(&total)
	return total, err
}

func (s *PostgresStore) GetHistoryGame(ctx context.Context, gameID int64, gameType string, includeEvents bool) (history.Game, []history.Player, []history.Event, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, room_id, game_type, started_at, ended_at, duration_seconds,
			winner_user_id, winner_player_id, end_reason, max_players,
			initial_state, final_result, created_at, updated_at
		FROM games WHERE id = $1 AND game_type = $2`, gameID, gameType)
	gameRecord, err := scanHistoryGame(row)
	if err != nil {
		return history.Game{}, nil, nil, err
	}
	players, err := s.HistoryPlayers(ctx, gameID)
	if err != nil {
		return history.Game{}, nil, nil, err
	}
	events, err := s.HistoryEvents(ctx, gameID, includeEvents)
	return gameRecord, players, events, err
}

func (s *PostgresStore) GetCompletedHistoryGameForUser(ctx context.Context, gameID, userID int64, gameType string, includeEvents bool) (history.Game, []history.Player, []history.Event, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT g.id, g.room_id, g.game_type, g.started_at, g.ended_at, g.duration_seconds,
			g.winner_user_id, g.winner_player_id, g.end_reason, g.max_players,
			g.initial_state, g.final_result, g.created_at, g.updated_at
		FROM games g
		WHERE g.id = $1
			AND g.game_type = $2
			AND g.ended_at IS NOT NULL
			AND EXISTS (
				SELECT 1
				FROM game_players gp
				WHERE gp.game_id = g.id AND gp.user_id = $3
			)`, gameID, gameType, userID)
	gameRecord, err := scanHistoryGame(row)
	if err != nil {
		return history.Game{}, nil, nil, err
	}
	players, err := s.HistoryPlayers(ctx, gameID)
	if err != nil {
		return history.Game{}, nil, nil, err
	}
	events, err := s.HistoryEvents(ctx, gameID, includeEvents)
	return gameRecord, players, events, err
}

func (s *PostgresStore) HistoryPlayers(ctx context.Context, gameID int64) ([]history.Player, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, game_id, user_id, player_id, seat_index, is_ai, final_score,
			final_money, final_stocks, final_rank, is_winner, created_at
		FROM game_players WHERE game_id = $1 ORDER BY seat_index ASC`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	players := []history.Player{}
	for rows.Next() {
		var player history.Player
		var userID, finalScore, finalMoney, finalRank sql.NullInt64
		if err := rows.Scan(&player.ID, &player.GameID, &userID, &player.PlayerID, &player.SeatIndex,
			&player.IsAI, &finalScore, &finalMoney, &player.FinalStocks, &finalRank, &player.IsWinner, &player.CreatedAt); err != nil {
			return nil, err
		}
		player.UserID = int64Pointer(userID)
		player.FinalScore = intPointer(finalScore)
		player.FinalMoney = intPointer(finalMoney)
		player.FinalRank = intPointer(finalRank)
		players = append(players, player)
	}
	return players, rows.Err()
}

func (s *PostgresStore) HistoryEvents(ctx context.Context, gameID int64, includeState bool) ([]history.Event, error) {
	stateColumns := "NULL::jsonb, NULL::bytea"
	if includeState {
		stateColumns = "state_snapshot, state_blob"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, game_id, seq, occurred_at, player_id, cmd_type, payload, %s
		FROM game_events WHERE game_id = $1 ORDER BY seq ASC`, stateColumns), gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []history.Event{}
	for rows.Next() {
		var event history.Event
		var payload, snapshot []byte
		if err := rows.Scan(&event.ID, &event.GameID, &event.Seq, &event.OccurredAt,
			&event.PlayerID, &event.CommandType, &payload, &snapshot, &event.StateBlob); err != nil {
			return nil, err
		}
		event.Payload = payload
		event.StateSnapshot = snapshot
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *PostgresStore) HistoryReplayPageForUser(
	ctx context.Context,
	gameID,
	userID int64,
	gameType string,
	eventOffset,
	eventLimit int,
) (history.Game, []history.Player, int, []history.Event, error) {
	gameRecord, players, _, err := s.GetCompletedHistoryGameForUser(ctx, gameID, userID, gameType, false)
	if err != nil {
		return history.Game{}, nil, 0, nil, err
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_events WHERE game_id = $1`, gameID).Scan(&total); err != nil {
		return history.Game{}, nil, 0, nil, err
	}
	if eventLimit <= 0 || eventOffset >= total {
		return gameRecord, players, total, []history.Event{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, game_id, seq, occurred_at, player_id, cmd_type, payload,
			state_snapshot, state_blob
		FROM game_events
		WHERE game_id = $1
		ORDER BY seq ASC
		OFFSET $2 LIMIT $3`, gameID, eventOffset, eventLimit)
	if err != nil {
		return history.Game{}, nil, 0, nil, err
	}
	defer rows.Close()
	events := make([]history.Event, 0, eventLimit)
	for rows.Next() {
		var event history.Event
		var payload, snapshot []byte
		if err := rows.Scan(
			&event.ID,
			&event.GameID,
			&event.Seq,
			&event.OccurredAt,
			&event.PlayerID,
			&event.CommandType,
			&payload,
			&snapshot,
			&event.StateBlob,
		); err != nil {
			return history.Game{}, nil, 0, nil, err
		}
		event.Payload = payload
		event.StateSnapshot = snapshot
		events = append(events, event)
	}
	return gameRecord, players, total, events, rows.Err()
}

func (s *PostgresStore) HistoryStats(ctx context.Context, userID int64, gameType string) (history.Stats, error) {
	var stats history.Stats
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COUNT(*) FILTER (WHERE gp.is_winner),
			COALESCE(AVG(gp.final_score), 0)
		FROM game_players gp
		JOIN games g ON g.id = gp.game_id
		WHERE gp.user_id = $1 AND g.game_type = $2 AND g.ended_at IS NOT NULL`,
		userID, gameType).Scan(&stats.TotalGames, &stats.Wins, &stats.AvgScore)
	if err == nil && stats.TotalGames > 0 {
		stats.WinRate = float64(stats.Wins) / float64(stats.TotalGames)
	}
	return stats, err
}

func (s *PostgresStore) HistoryLeaderboard(ctx context.Context, gameType string, limit, offset int) ([]history.LeaderboardEntry, error) {
	return s.HistoryLeaderboardSorted(ctx, gameType, "metric", limit, offset)
}

func (s *PostgresStore) HistoryLeaderboardSorted(ctx context.Context, gameType, sortBy string, limit, offset int) ([]history.LeaderboardEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if gameType == "davinci" {
		order := "win_rate DESC, total_games DESC, gp.user_id ASC"
		if sortBy == "totalGames" {
			order = "total_games DESC, win_rate DESC, gp.user_id ASC"
		}
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
			SELECT gp.user_id,
				COUNT(*) AS total_games,
				COUNT(*) FILTER (WHERE gp.is_winner) AS wins,
				1.0 * COUNT(*) FILTER (WHERE gp.is_winner) / COUNT(*) AS win_rate,
				(ARRAY_AGG(gp.player_id ORDER BY g.started_at DESC))[1]
			FROM game_players gp
			JOIN games g ON g.id = gp.game_id
			WHERE g.game_type = $1 AND g.ended_at IS NOT NULL
				AND gp.user_id IS NOT NULL
			GROUP BY gp.user_id
			ORDER BY %s
			LIMIT $2 OFFSET $3`, order), gameType, limit, offset)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		entries := []history.LeaderboardEntry{}
		for rows.Next() {
			var entry history.LeaderboardEntry
			var wins int
			var winRate float64
			if err := rows.Scan(&entry.UserID, &entry.TotalGames, &wins, &winRate, &entry.PlayerID); err != nil {
				return nil, err
			}
			entry.Wins = &wins
			entry.WinRate = &winRate
			entries = append(entries, entry)
		}
		return entries, rows.Err()
	}
	order := "avg_rank ASC, total_games DESC, gp.user_id ASC"
	if sortBy == "totalGames" {
		order = "total_games DESC, avg_rank ASC, gp.user_id ASC"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT gp.user_id, COUNT(*) AS total_games, AVG(gp.final_rank)::float8 AS avg_rank,
			(ARRAY_AGG(gp.player_id ORDER BY g.started_at DESC))[1]
		FROM game_players gp
		JOIN games g ON g.id = gp.game_id
		WHERE g.game_type = $1 AND g.ended_at IS NOT NULL
			AND gp.user_id IS NOT NULL AND gp.final_rank IS NOT NULL
		GROUP BY gp.user_id
		ORDER BY %s
		LIMIT $2 OFFSET $3`, order), gameType, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []history.LeaderboardEntry{}
	for rows.Next() {
		var entry history.LeaderboardEntry
		var avgRank float64
		if err := rows.Scan(&entry.UserID, &entry.TotalGames, &avgRank, &entry.PlayerID); err != nil {
			return nil, err
		}
		entry.AvgRank = &avgRank
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *PostgresStore) CountLeaderboardEntries(ctx context.Context, gameType string) (int, error) {
	var total int
	extra := ""
	if gameType != "davinci" {
		extra = " AND gp.final_rank IS NOT NULL"
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT gp.user_id)
		FROM game_players gp
		JOIN games g ON g.id = gp.game_id
		WHERE g.game_type = $1 AND g.ended_at IS NOT NULL
			AND gp.user_id IS NOT NULL`+extra, gameType).Scan(&total)
	return total, err
}

type historyGameScanner interface {
	Scan(dest ...any) error
}

func scanHistoryGame(scanner historyGameScanner) (history.Game, error) {
	var gameRecord history.Game
	var endedAt sql.NullTime
	var winnerUserID sql.NullInt64
	var winnerPlayerID, endReason sql.NullString
	var initialState, finalResult []byte
	err := scanner.Scan(
		&gameRecord.ID, &gameRecord.RoomID, &gameRecord.GameType, &gameRecord.StartedAt,
		&endedAt, &gameRecord.DurationSeconds, &winnerUserID, &winnerPlayerID,
		&endReason, &gameRecord.MaxPlayers, &initialState, &finalResult,
		&gameRecord.CreatedAt, &gameRecord.UpdatedAt,
	)
	if err != nil {
		return history.Game{}, err
	}
	if endedAt.Valid {
		gameRecord.EndedAt = &endedAt.Time
	}
	if winnerUserID.Valid {
		gameRecord.WinnerUserID = &winnerUserID.Int64
	}
	if winnerPlayerID.Valid {
		gameRecord.WinnerPlayerID = winnerPlayerID.String
	}
	if endReason.Valid {
		gameRecord.EndReason = endReason.String
	}
	gameRecord.InitialState = initialState
	gameRecord.FinalResult = finalResult
	return gameRecord, nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func int64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func intPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}

func ParseNumericUserID(value string) *int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

var ErrHistoryNotFound = errors.New("history game not found")
