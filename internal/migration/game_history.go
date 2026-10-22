package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	_ "github.com/lib/pq"
)

const legacySource = "game-backend"

var supportedGameTypes = []string{"acquire", "davinci", "splendor"}

type GameHistoryOptions struct {
	GameTypes []string
	DryRun    bool
	Verify    bool
}

type GameHistoryCount struct {
	GameType string
	Games    int
	Players  int
	Events   int
}

type GameHistoryResult struct {
	Counts []GameHistoryCount
}

func CopyGameHistory(
	ctx context.Context,
	sourceDSN,
	targetDSN string,
	options GameHistoryOptions,
) (GameHistoryResult, error) {
	gameTypes, err := normalizeGameTypes(options.GameTypes)
	if err != nil {
		return GameHistoryResult{}, err
	}
	source, err := sql.Open("postgres", sourceDSN)
	if err != nil {
		return GameHistoryResult{}, err
	}
	defer source.Close()
	target, err := sql.Open("postgres", targetDSN)
	if err != nil {
		return GameHistoryResult{}, err
	}
	defer target.Close()
	if err := source.PingContext(ctx); err != nil {
		return GameHistoryResult{}, fmt.Errorf("source database: %w", err)
	}
	if err := target.PingContext(ctx); err != nil {
		return GameHistoryResult{}, fmt.Errorf("target database: %w", err)
	}

	result := GameHistoryResult{}
	for _, gameType := range gameTypes {
		count, err := sourceHistoryCount(ctx, source, gameType)
		if err != nil {
			return result, err
		}
		if !options.DryRun {
			if err := copyGameType(ctx, source, target, gameType); err != nil {
				return result, err
			}
		}
		if options.Verify || !options.DryRun {
			targetCount, err := targetHistoryCount(ctx, target, gameType)
			if err != nil {
				return result, err
			}
			if targetCount != count {
				return result, fmt.Errorf(
					"%s history verification failed: source=%+v target=%+v",
					gameType,
					count,
					targetCount,
				)
			}
		}
		result.Counts = append(result.Counts, count)
	}
	return result, nil
}

func normalizeGameTypes(values []string) ([]string, error) {
	if len(values) == 0 {
		return append([]string(nil), supportedGameTypes...), nil
	}
	allowed := map[string]bool{}
	for _, value := range supportedGameTypes {
		allowed[value] = true
	}
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if !allowed[value] {
			return nil, fmt.Errorf("unsupported game type %q", value)
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result, nil
}

func sourceHistoryCount(ctx context.Context, db *sql.DB, gameType string) (GameHistoryCount, error) {
	count := GameHistoryCount{GameType: gameType}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM games
		WHERE game_type = $1`, gameType).Scan(&count.Games); err != nil {
		return count, fmt.Errorf("count source %s games: %w", gameType, err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM game_players player
		JOIN games game ON game.id = player.game_id
		WHERE game.game_type = $1`, gameType).Scan(&count.Players); err != nil {
		return count, fmt.Errorf("count source %s players: %w", gameType, err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM game_events event
		JOIN games game ON game.id = event.game_id
		WHERE game.game_type = $1`, gameType).Scan(&count.Events); err != nil {
		return count, fmt.Errorf("count source %s events: %w", gameType, err)
	}
	return count, nil
}

func targetHistoryCount(ctx context.Context, db *sql.DB, gameType string) (GameHistoryCount, error) {
	count := GameHistoryCount{GameType: gameType}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM games
		WHERE game_type = $1 AND legacy_source = $2`,
		gameType, legacySource).Scan(&count.Games); err != nil {
		return count, err
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM game_players player
		JOIN games game ON game.id = player.game_id
		WHERE game.game_type = $1 AND game.legacy_source = $2`,
		gameType, legacySource).Scan(&count.Players); err != nil {
		return count, err
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM game_events event
		JOIN games game ON game.id = event.game_id
		WHERE game.game_type = $1 AND game.legacy_source = $2`,
		gameType, legacySource).Scan(&count.Events); err != nil {
		return count, err
	}
	return count, nil
}

func copyGameType(ctx context.Context, source, target *sql.DB, gameType string) error {
	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := source.QueryContext(ctx, `
		SELECT id, room_id, started_at, ended_at, duration_seconds,
			winner_user_id, winner_player_id, end_reason, max_players,
			initial_state, final_result, created_at, updated_at
		FROM games
		WHERE game_type = $1
		ORDER BY id`, gameType)
	if err != nil {
		return fmt.Errorf("read legacy %s games: %w", gameType, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			sourceID int64
			roomID   string
			values   = make([]any, 11)
		)
		destinations := []any{&sourceID, &roomID}
		for index := range values {
			destinations = append(destinations, &values[index])
		}
		if err := rows.Scan(destinations...); err != nil {
			return err
		}
		var targetID int64
		err := tx.QueryRowContext(ctx, `
			INSERT INTO games (
				room_id, game_type, started_at, ended_at, duration_seconds,
				winner_user_id, winner_player_id, end_reason, max_players,
				initial_state, final_result, created_at, updated_at,
				legacy_source, legacy_game_id
			) VALUES (
				$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15
			)
			ON CONFLICT (legacy_source, legacy_game_id)
				WHERE legacy_source IS NOT NULL
			DO UPDATE SET legacy_source = EXCLUDED.legacy_source
			RETURNING id`,
			roomID, gameType,
			values[0], values[1], values[2], values[3], values[4], values[5],
			values[6], values[7], values[8], values[9], values[10],
			legacySource, sourceID,
		).Scan(&targetID)
		if err != nil {
			return fmt.Errorf("copy %s game %d: %w", gameType, sourceID, err)
		}
		if err := copyPlayers(ctx, source, tx, sourceID, targetID); err != nil {
			return err
		}
		if err := copyEvents(ctx, source, tx, sourceID, targetID); err != nil {
			return err
		}
		if err := projectLegacyMatch(ctx, tx, gameType, sourceID, targetID); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func copyPlayers(ctx context.Context, source *sql.DB, target *sql.Tx, sourceGameID, targetGameID int64) error {
	rows, err := source.QueryContext(ctx, `
		SELECT user_id, player_id, seat_index, is_ai, final_score,
			final_money, final_stocks, final_rank, is_winner, created_at
		FROM game_players
		WHERE game_id = $1
		ORDER BY seat_index, id`, sourceGameID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]any, 10)
		destinations := make([]any, len(values))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return err
		}
		if _, err := target.ExecContext(ctx, `
			INSERT INTO game_players (
				game_id, user_id, player_id, seat_index, is_ai, final_score,
				final_money, final_stocks, final_rank, is_winner, created_at
			)
			SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11
			WHERE NOT EXISTS (
				SELECT 1 FROM game_players
				WHERE game_id = $1 AND player_id = $3 AND seat_index = $4
			)`,
			targetGameID,
			values[0], values[1], values[2], values[3], values[4],
			values[5], values[6], values[7], values[8], values[9],
		); err != nil {
			return err
		}
	}
	return rows.Err()
}

func copyEvents(ctx context.Context, source *sql.DB, target *sql.Tx, sourceGameID, targetGameID int64) error {
	rows, err := source.QueryContext(ctx, `
		SELECT seq, occurred_at, player_id, cmd_type, payload,
			state_snapshot, state_blob
		FROM game_events
		WHERE game_id = $1
		ORDER BY seq`, sourceGameID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]any, 7)
		destinations := make([]any, len(values))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return err
		}
		if _, err := target.ExecContext(ctx, `
			INSERT INTO game_events (
				game_id, seq, occurred_at, player_id, cmd_type, payload,
				state_snapshot, state_blob
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (game_id, seq) DO NOTHING`,
			targetGameID,
			values[0], values[1], values[2], values[3],
			values[4], values[5], values[6],
		); err != nil {
			return err
		}
	}
	return rows.Err()
}

func projectLegacyMatch(
	ctx context.Context,
	target *sql.Tx,
	gameType string,
	sourceGameID,
	targetGameID int64,
) error {
	matchID := fmt.Sprintf("legacy-%s-%d", gameType, sourceGameID)
	if _, err := target.ExecContext(ctx, `
		INSERT INTO matches (
			match_id, runtime_room_id, game_type, status, owner_user_id,
			owner_player_id, runtime_owner_node, epoch, max_players,
			player_count, engine_version, started_at, updated_at, ended_at,
			duration_seconds, winner_user_id, winner_player_id, end_reason
		)
		SELECT
			$2, $2, game.game_type,
			CASE WHEN game.end_reason = 'abandoned' THEN 'aborted' ELSE 'ended' END,
			COALESCE((
				SELECT player.user_id::text FROM game_players player
				WHERE player.game_id = game.id AND player.user_id IS NOT NULL
				ORDER BY player.seat_index LIMIT 1
			), ''),
			COALESCE((
				SELECT player.player_id FROM game_players player
				WHERE player.game_id = game.id ORDER BY player.seat_index LIMIT 1
			), ''),
			'legacy', 1, game.max_players,
			(SELECT COUNT(*) FROM game_players player WHERE player.game_id = game.id),
			game.game_type || '-legacy', game.started_at,
			COALESCE(game.updated_at, game.started_at), game.ended_at,
			COALESCE(game.duration_seconds, 0), COALESCE(game.winner_user_id::text, ''),
			COALESCE(game.winner_player_id, ''), COALESCE(game.end_reason, '')
		FROM games game
		WHERE game.id = $1
		ON CONFLICT (match_id) DO NOTHING`, targetGameID, matchID); err != nil {
		return err
	}
	_, err := target.ExecContext(ctx, `
		INSERT INTO match_players (
			match_id, user_id, player_id, display_name, name, email, avatar,
			gateway_node, online, is_ai, seat_index, ready, joined_at, last_seen
		)
		SELECT $2, COALESCE(user_id::text, ''), player_id, player_id, '', '', '',
			'legacy', false, is_ai, seat_index, false, created_at, created_at
		FROM game_players
		WHERE game_id = $1
		ON CONFLICT (match_id, player_id) DO NOTHING`, targetGameID, matchID)
	return err
}

func VerifyGameHistory(
	ctx context.Context,
	sourceDSN,
	targetDSN string,
	gameTypes []string,
) (GameHistoryResult, error) {
	return CopyGameHistory(ctx, sourceDSN, targetDSN, GameHistoryOptions{
		GameTypes: gameTypes,
		DryRun:    true,
		Verify:    true,
	})
}

func IsVerificationError(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled) &&
		strings.Contains(err.Error(), "verification failed")
}
