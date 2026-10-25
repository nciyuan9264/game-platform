package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/lib/pq"
	"github.com/nciyuan9264/game-platform/internal/contracts"
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
			room_id, room_display_name, game_type, engine_version, replay_schema_version,
			started_at, max_players, initial_state, initial_replay_payload,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
		RETURNING id`,
		gameRecord.RoomID, gameRecord.RoomDisplayName,
		gameRecord.GameType, gameRecord.EngineVersion,
		gameRecord.ReplaySchemaVersion, gameRecord.StartedAt,
		gameRecord.MaxPlayers, nullableJSON(gameRecord.InitialState),
		nullableBytes(gameRecord.InitialReplayPayload), now,
	).Scan(&gameID)
	if err != nil {
		return 0, err
	}
	for _, player := range players {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO game_players (
				game_id, user_id, player_id, display_name, game_role, seat_index, is_ai,
				final_stocks, is_winner, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,0,false,$8)`,
			gameID, nullableInt64(player.UserID), player.PlayerID, player.DisplayName,
			player.GameRole, player.SeatIndex, player.IsAI, now)
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

func (s *PostgresStore) FindHistoryGameByStart(
	ctx context.Context,
	roomID string,
	gameType string,
	startedAt time.Time,
) (int64, int, bool, error) {
	var gameID int64
	var nextSeq int
	var finished bool
	err := s.db.QueryRowContext(ctx, `
		SELECT
			g.id,
			COALESCE(MAX(ge.seq) + 1, 0),
			g.ended_at IS NOT NULL
		FROM games g
		LEFT JOIN game_events ge ON ge.game_id = g.id
		WHERE g.room_id = $1
			AND g.game_type = $2
			AND g.started_at = $3
		GROUP BY g.id, g.ended_at
		ORDER BY COUNT(ge.id) DESC, g.id ASC
		LIMIT 1`,
		roomID,
		gameType,
		startedAt,
	).Scan(&gameID, &nextSeq, &finished)
	if err != nil {
		return 0, 0, false, err
	}
	return gameID, nextSeq, finished, nil
}

func (s *PostgresStore) SetHistoryReplayMetadata(
	ctx context.Context,
	gameID int64,
	engineVersion string,
	replaySchemaVersion int,
	initialReplayPayload []byte,
) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE games
		SET engine_version = CASE
				WHEN engine_version = '' THEN $2
				ELSE engine_version
			END,
			replay_schema_version = CASE
				WHEN replay_schema_version = 0 THEN $3
				ELSE replay_schema_version
			END,
			initial_replay_payload = COALESCE(initial_replay_payload, $4)
		WHERE id = $1
			AND (engine_version = '' OR engine_version = $2)
			AND (replay_schema_version = 0 OR replay_schema_version = $3)`,
		gameID,
		engineVersion,
		replaySchemaVersion,
		nullableBytes(initialReplayPayload),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf(
			"history replay metadata conflict for game %d",
			gameID,
		)
	}
	return nil
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
			game_id, seq, occurred_at, player_id, cmd_type, payload,
			state_snapshot, state_blob, replay_payload
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (game_id, seq) DO UPDATE SET
			replay_payload = COALESCE(game_events.replay_payload, EXCLUDED.replay_payload)`,
		event.GameID, event.Seq, event.OccurredAt, event.PlayerID, event.CommandType,
		nullableJSON(event.Payload), nullableJSON(event.StateSnapshot),
		nullableBytes(event.StateBlob), nullableBytes(event.ReplayPayload))
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
	result, err := tx.ExecContext(ctx, `
		UPDATE games SET
			ended_at = $2,
			duration_seconds = $3,
			winner_user_id = $4,
			winner_player_id = $5,
			end_reason = $6,
			final_result = $7,
			updated_at = $2
		WHERE id = $1 AND ended_at IS NULL`,
		gameID, input.EndedAt, input.DurationSeconds, input.WinnerUserID,
		input.WinnerPlayerID, input.EndReason, nullableJSON(input.FinalResult))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return tx.Commit()
	}
	for _, player := range input.Players {
		_, err = tx.ExecContext(ctx, `
			UPDATE game_players SET
				user_id = COALESCE($3, user_id),
				display_name = COALESCE(NULLIF($4, ''), display_name),
				game_role = COALESCE(NULLIF($5, ''), game_role),
				final_score = $6,
				final_money = $7,
				final_stocks = $8,
				final_rank = $9,
				is_winner = $10
			WHERE game_id = $1 AND player_id = $2`,
			gameID, player.PlayerID, nullableInt64(player.UserID),
			player.DisplayName, player.GameRole, player.FinalScore,
			player.FinalMoney, player.FinalStocks, player.FinalRank,
			player.IsWinner)
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
		SELECT DISTINCT g.id, g.room_id, g.room_display_name, g.game_type, g.engine_version,
			g.replay_schema_version, g.started_at, g.ended_at,
			g.duration_seconds, g.winner_user_id, g.winner_player_id, g.end_reason,
			g.max_players, NULL::jsonb, NULL::bytea, NULL::jsonb,
			g.created_at, g.updated_at
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
	gameIDs := make([]int64, 0, limit)
	for rows.Next() {
		gameRecord, scanErr := scanHistoryGame(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		games = append(games, gameRecord)
		gameIDs = append(gameIDs, gameRecord.ID)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
	}
	if closeErr := rows.Close(); closeErr != nil {
		return nil, closeErr
	}
	playersByGameID, err := s.historyPlayersByGameIDs(ctx, gameIDs)
	if err != nil {
		return nil, err
	}
	for index := range games {
		games[index].Players = playersByGameID[games[index].ID]
	}
	return games, nil
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
		SELECT id, room_id, room_display_name, game_type, engine_version, replay_schema_version,
			started_at, ended_at, duration_seconds,
			winner_user_id, winner_player_id, end_reason, max_players,
			initial_state, initial_replay_payload, final_result, created_at, updated_at
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
		SELECT g.id, g.room_id, g.room_display_name, g.game_type, g.engine_version,
			g.replay_schema_version, g.started_at, g.ended_at, g.duration_seconds,
			g.winner_user_id, g.winner_player_id, g.end_reason, g.max_players,
			g.initial_state, g.initial_replay_payload, g.final_result,
			g.created_at, g.updated_at
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
		SELECT id, game_id, user_id, player_id, display_name, game_role, seat_index, is_ai, final_score,
			final_money, final_stocks, final_rank, is_winner, created_at
		FROM game_players WHERE game_id = $1 ORDER BY seat_index ASC`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	players := []history.Player{}
	for rows.Next() {
		player, scanErr := scanHistoryPlayer(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		players = append(players, player)
	}
	return players, rows.Err()
}

func (s *PostgresStore) historyPlayersByGameIDs(ctx context.Context, gameIDs []int64) (map[int64][]history.Player, error) {
	playersByGameID := make(map[int64][]history.Player, len(gameIDs))
	if len(gameIDs) == 0 {
		return playersByGameID, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, game_id, user_id, player_id, display_name, game_role, seat_index, is_ai, final_score,
			final_money, final_stocks, final_rank, is_winner, created_at
		FROM game_players
		WHERE game_id = ANY($1)
		ORDER BY game_id ASC, seat_index ASC, id ASC`, pq.Array(gameIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		player, scanErr := scanHistoryPlayer(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		playersByGameID[player.GameID] = append(playersByGameID[player.GameID], player)
	}
	return playersByGameID, rows.Err()
}

func (s *PostgresStore) HistoryEvents(ctx context.Context, gameID int64, includeState bool) ([]history.Event, error) {
	stateColumns := "NULL::jsonb, NULL::bytea, replay_payload"
	if includeState {
		stateColumns = "state_snapshot, state_blob, replay_payload"
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
			&event.PlayerID, &event.CommandType, &payload, &snapshot,
			&event.StateBlob, &event.ReplayPayload); err != nil {
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
			state_snapshot, state_blob, replay_payload
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
			&event.ReplayPayload,
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
	stats := history.Stats{
		GamesByRole: map[string]int{},
		WinsByRole:  map[string]int{},
	}
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
	if err != nil {
		return stats, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT gp.game_role,
			COUNT(*),
			COUNT(*) FILTER (WHERE gp.is_winner)
		FROM game_players gp
		JOIN games g ON g.id = gp.game_id
		WHERE gp.user_id = $1
			AND g.game_type = $2
			AND g.ended_at IS NOT NULL
			AND gp.game_role <> ''
		GROUP BY gp.game_role`,
		userID,
		gameType,
	)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var role string
		var games, wins int
		if err := rows.Scan(&role, &games, &wins); err != nil {
			return stats, err
		}
		stats.GamesByRole[role] = games
		stats.WinsByRole[role] = wins
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}
	return stats, err
}

func (s *PostgresStore) HistoryLeaderboard(ctx context.Context, gameType string, limit, offset int) ([]history.LeaderboardEntry, error) {
	return s.HistoryLeaderboardSorted(ctx, gameType, "metric", limit, offset)
}

func (s *PostgresStore) HistoryLeaderboardSorted(ctx context.Context, gameType, sortBy string, limit, offset int) ([]history.LeaderboardEntry, error) {
	return s.HistoryLeaderboardSortedByRole(
		ctx,
		gameType,
		sortBy,
		"",
		limit,
		offset,
	)
}

func (s *PostgresStore) HistoryLeaderboardSortedByRole(
	ctx context.Context,
	gameType,
	sortBy,
	gameRole string,
	limit,
	offset int,
) ([]history.LeaderboardEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if usesWinRateLeaderboard(gameType) {
		order := "win_rate DESC, total_games DESC, gp.user_id ASC"
		if sortBy == "totalGames" {
			order = "total_games DESC, win_rate DESC, gp.user_id ASC"
		}
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
			SELECT gp.user_id,
				COUNT(*) AS total_games,
				COUNT(*) FILTER (WHERE gp.is_winner) AS wins,
				1.0 * COUNT(*) FILTER (WHERE gp.is_winner) / COUNT(*) AS win_rate,
				gp.user_id::text,
				COALESCE(
					(ARRAY_AGG(gp.display_name ORDER BY g.started_at DESC)
						FILTER (WHERE gp.display_name <> ''))[1],
					''
				)
			FROM game_players gp
			JOIN games g ON g.id = gp.game_id
			WHERE g.game_type = $1 AND g.ended_at IS NOT NULL
				AND gp.user_id IS NOT NULL
				AND ($4 = '' OR gp.game_role = $4)
			GROUP BY gp.user_id
			ORDER BY %s
			LIMIT $2 OFFSET $3`, order),
			gameType, limit, offset, gameRole)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		entries := []history.LeaderboardEntry{}
		for rows.Next() {
			var entry history.LeaderboardEntry
			var wins int
			var winRate float64
			if err := rows.Scan(
				&entry.UserID,
				&entry.TotalGames,
				&wins,
				&winRate,
				&entry.PlayerID,
				&entry.DisplayName,
			); err != nil {
				return nil, err
			}
			entry.Wins = &wins
			entry.WinRate = &winRate
			entries = append(entries, entry)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		return entries, nil
	}
	order := "avg_rank ASC, total_games DESC, gp.user_id ASC"
	if sortBy == "totalGames" {
		order = "total_games DESC, avg_rank ASC, gp.user_id ASC"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT gp.user_id, COUNT(*) AS total_games, AVG(gp.final_rank)::float8 AS avg_rank,
			gp.user_id::text,
			COALESCE(
				(ARRAY_AGG(gp.display_name ORDER BY g.started_at DESC)
					FILTER (WHERE gp.display_name <> ''))[1],
				''
			)
		FROM game_players gp
		JOIN games g ON g.id = gp.game_id
		WHERE g.game_type = $1 AND g.ended_at IS NOT NULL
			AND gp.user_id IS NOT NULL AND gp.final_rank IS NOT NULL
			AND ($4 = '' OR gp.game_role = $4)
		GROUP BY gp.user_id
		ORDER BY %s
		LIMIT $2 OFFSET $3`, order),
		gameType, limit, offset, gameRole)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []history.LeaderboardEntry{}
	for rows.Next() {
		var entry history.LeaderboardEntry
		var avgRank float64
		if err := rows.Scan(
			&entry.UserID,
			&entry.TotalGames,
			&avgRank,
			&entry.PlayerID,
			&entry.DisplayName,
		); err != nil {
			return nil, err
		}
		entry.AvgRank = &avgRank
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *PostgresStore) CountLeaderboardEntries(ctx context.Context, gameType string) (int, error) {
	return s.CountLeaderboardEntriesByRole(ctx, gameType, "")
}

func (s *PostgresStore) CountLeaderboardEntriesByRole(
	ctx context.Context,
	gameType,
	gameRole string,
) (int, error) {
	var total int
	extra := ""
	if !usesWinRateLeaderboard(gameType) {
		extra = " AND gp.final_rank IS NOT NULL"
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT gp.user_id)
		FROM game_players gp
		JOIN games g ON g.id = gp.game_id
		WHERE g.game_type = $1 AND g.ended_at IS NOT NULL
			AND gp.user_id IS NOT NULL
			AND ($2 = '' OR gp.game_role = $2)`+extra,
		gameType, gameRole).Scan(&total)
	return total, err
}

func usesWinRateLeaderboard(gameType string) bool {
	return gameType == contracts.GameDaVinci ||
		gameType == contracts.GameYinsh ||
		gameType == contracts.GameOthello ||
		gameType == contracts.GameFugitive
}

func (s *PostgresStore) ListReplayBackfillGameIDs(
	ctx context.Context,
	gameType string,
	afterID int64,
	limit int,
) ([]int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id
		FROM games g
		WHERE g.game_type = $1
			AND g.id > $2
			AND (
				g.initial_replay_payload IS NULL
				OR EXISTS (
					SELECT 1
					FROM game_events ge
					WHERE ge.game_id = g.id AND ge.replay_payload IS NULL
				)
			)
		ORDER BY g.id ASC
		LIMIT $3`,
		gameType,
		afterID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *PostgresStore) BackfillReplayPayloads(
	ctx context.Context,
	gameID int64,
	engineVersion string,
	replaySchemaVersion int,
	initialReplayPayload []byte,
	eventPayloads map[int][]byte,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE games
		SET engine_version = CASE
				WHEN engine_version = '' THEN $2
				ELSE engine_version
			END,
			replay_schema_version = CASE
				WHEN replay_schema_version = 0 THEN $3
				ELSE replay_schema_version
			END,
			initial_replay_payload = COALESCE(initial_replay_payload, $4)
		WHERE id = $1
			AND (engine_version = '' OR engine_version = $2)
			AND (replay_schema_version = 0 OR replay_schema_version = $3)`,
		gameID,
		engineVersion,
		replaySchemaVersion,
		nullableBytes(initialReplayPayload),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("replay backfill metadata conflict for game %d", gameID)
	}
	for seq, payload := range eventPayloads {
		if _, err := tx.ExecContext(ctx, `
			UPDATE game_events
			SET replay_payload = COALESCE(replay_payload, $3)
			WHERE game_id = $1 AND seq = $2`,
			gameID,
			seq,
			nullableBytes(payload),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) CountMissingReplayPayloads(
	ctx context.Context,
	gameType string,
) (history.ReplayMissingCounts, error) {
	var counts history.ReplayMissingCounts
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(
				SELECT COUNT(*)
				FROM games g
				WHERE g.game_type = $1
					AND g.initial_replay_payload IS NULL
			),
			(
				SELECT COUNT(*)
				FROM game_events ge
				JOIN games g ON g.id = ge.game_id
				WHERE g.game_type = $1
					AND ge.replay_payload IS NULL
			)`,
		gameType,
	).Scan(&counts.Games, &counts.Events)
	return counts, err
}

type historyGameScanner interface {
	Scan(dest ...any) error
}

type historyPlayerScanner interface {
	Scan(dest ...any) error
}

func scanHistoryPlayer(scanner historyPlayerScanner) (history.Player, error) {
	var player history.Player
	var userID, finalScore, finalMoney, finalRank sql.NullInt64
	err := scanner.Scan(
		&player.ID,
		&player.GameID,
		&userID,
		&player.PlayerID,
		&player.DisplayName,
		&player.GameRole,
		&player.SeatIndex,
		&player.IsAI,
		&finalScore,
		&finalMoney,
		&player.FinalStocks,
		&finalRank,
		&player.IsWinner,
		&player.CreatedAt,
	)
	if err != nil {
		return history.Player{}, err
	}
	player.UserID = int64Pointer(userID)
	player.FinalScore = intPointer(finalScore)
	player.FinalMoney = intPointer(finalMoney)
	player.FinalRank = intPointer(finalRank)
	return player, nil
}

func scanHistoryGame(scanner historyGameScanner) (history.Game, error) {
	var gameRecord history.Game
	var endedAt sql.NullTime
	var winnerUserID sql.NullInt64
	var winnerPlayerID, endReason sql.NullString
	var initialState, initialReplayPayload, finalResult []byte
	err := scanner.Scan(
		&gameRecord.ID, &gameRecord.RoomID,
		&gameRecord.RoomDisplayName, &gameRecord.GameType,
		&gameRecord.EngineVersion, &gameRecord.ReplaySchemaVersion, &gameRecord.StartedAt,
		&endedAt, &gameRecord.DurationSeconds, &winnerUserID, &winnerPlayerID,
		&endReason, &gameRecord.MaxPlayers, &initialState, &initialReplayPayload,
		&finalResult, &gameRecord.CreatedAt, &gameRecord.UpdatedAt,
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
	gameRecord.InitialReplayPayload = initialReplayPayload
	gameRecord.FinalResult = finalResult
	return gameRecord, nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
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
