package adminanalytics

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lib/pq"
	"github.com/nciyuan9264/game-platform/internal/history"
)

func (s *Service) Matches(ctx context.Context, input MatchQuery) (MatchesPage, error) {
	query := normalizeMatchQuery(input)
	key := fmt.Sprintf(
		"matches:%d:%d:%s:%s:%s",
		query.Page,
		query.PageSize,
		strings.ToLower(query.Query),
		query.GameType,
		query.EndReason,
	)
	return cachedValue(ctx, s, key, func(ctx context.Context) (MatchesPage, error) {
		var total int64
		if err := s.gameDB.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM games g
			WHERE g.ended_at IS NOT NULL
				AND ($1 = '' OR g.game_type = $1)
				AND ($2 = '' OR g.end_reason = $2)
				AND (
					$3 = ''
					OR g.id::text = $3
					OR g.room_id ILIKE '%' || $3 || '%'
					OR g.match_id ILIKE '%' || $3 || '%'
					OR EXISTS (
						SELECT 1
						FROM game_players gp
						WHERE gp.game_id = g.id
							AND (
								COALESCE(gp.user_id::text, '') = $3
								OR gp.player_id ILIKE '%' || $3 || '%'
								OR gp.display_name ILIKE '%' || $3 || '%'
							)
					)
				)`,
			query.GameType,
			query.EndReason,
			query.Query,
		).Scan(&total); err != nil {
			return MatchesPage{}, fmt.Errorf("count historical matches: %w", err)
		}

		rows, err := s.gameDB.QueryContext(ctx, `
			SELECT
				g.id,
				g.room_id,
				g.match_id,
				g.game_type,
				g.started_at,
				g.ended_at,
				g.duration_seconds,
				g.end_reason,
				g.max_players
			FROM games g
			WHERE g.ended_at IS NOT NULL
				AND ($1 = '' OR g.game_type = $1)
				AND ($2 = '' OR g.end_reason = $2)
				AND (
					$3 = ''
					OR g.id::text = $3
					OR g.room_id ILIKE '%' || $3 || '%'
					OR g.match_id ILIKE '%' || $3 || '%'
					OR EXISTS (
						SELECT 1
						FROM game_players gp
						WHERE gp.game_id = g.id
							AND (
								COALESCE(gp.user_id::text, '') = $3
								OR gp.player_id ILIKE '%' || $3 || '%'
								OR gp.display_name ILIKE '%' || $3 || '%'
							)
					)
				)
			ORDER BY g.started_at DESC, g.id DESC
			LIMIT $4 OFFSET $5`,
			query.GameType,
			query.EndReason,
			query.Query,
			query.PageSize,
			(query.Page-1)*query.PageSize,
		)
		if err != nil {
			return MatchesPage{}, fmt.Errorf("query historical matches: %w", err)
		}
		defer rows.Close()

		matches := make([]Match, 0, query.PageSize)
		ids := make([]int64, 0, query.PageSize)
		for rows.Next() {
			var item Match
			if err := rows.Scan(
				&item.ID,
				&item.RoomID,
				&item.MatchID,
				&item.GameType,
				&item.StartedAt,
				&item.EndedAt,
				&item.DurationSeconds,
				&item.EndReason,
				&item.MaxPlayers,
			); err != nil {
				return MatchesPage{}, err
			}
			matches = append(matches, item)
			ids = append(ids, item.ID)
		}
		if err := rows.Err(); err != nil {
			return MatchesPage{}, err
		}

		players, err := s.matchPlayers(ctx, ids)
		if err != nil {
			return MatchesPage{}, err
		}
		for index := range matches {
			matches[index].Players = players[matches[index].ID]
		}
		return MatchesPage{
			Matches: matches, Page: query.Page, PageSize: query.PageSize, Total: total,
		}, nil
	})
}

func (s *Service) matchPlayers(ctx context.Context, matchIDs []int64) (map[int64][]MatchPlayer, error) {
	result := make(map[int64][]MatchPlayer, len(matchIDs))
	if len(matchIDs) == 0 {
		return result, nil
	}
	rows, err := s.gameDB.QueryContext(ctx, `
		SELECT
			game_id,
			user_id,
			player_id,
			display_name,
			seat_index,
			is_ai,
			final_rank,
			is_winner
		FROM game_players
		WHERE game_id = ANY($1)
		ORDER BY game_id, seat_index, id`,
		pq.Array(matchIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("query historical match players: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var matchID int64
		var userID sql.NullInt64
		var finalRank sql.NullInt64
		var player MatchPlayer
		if err := rows.Scan(
			&matchID,
			&userID,
			&player.PlayerID,
			&player.DisplayName,
			&player.SeatIndex,
			&player.IsAI,
			&finalRank,
			&player.IsWinner,
		); err != nil {
			return nil, err
		}
		if userID.Valid {
			value := userID.Int64
			player.UserID = &value
		}
		if finalRank.Valid {
			value := int(finalRank.Int64)
			player.FinalRank = &value
		}
		player.DisplayName = strings.TrimSpace(player.DisplayName)
		if player.DisplayName == "" {
			player.DisplayName = strings.TrimSpace(player.PlayerID)
		}
		if player.DisplayName == "" && player.IsAI {
			player.DisplayName = "AI"
		}
		if player.DisplayName == "" {
			player.DisplayName = "未知玩家"
		}
		result[matchID] = append(result[matchID], player)
	}
	return result, rows.Err()
}

func normalizeMatchQuery(input MatchQuery) MatchQuery {
	if input.Page < 1 {
		input.Page = 1
	}
	if input.PageSize < 1 {
		input.PageSize = 20
	}
	if input.PageSize > 100 {
		input.PageSize = 100
	}
	input.Query = strings.TrimSpace(input.Query)
	input.GameType = strings.ToLower(strings.TrimSpace(input.GameType))
	switch strings.ToLower(strings.TrimSpace(input.EndReason)) {
	case history.EndReasonCompleted:
		input.EndReason = history.EndReasonCompleted
	case history.EndReasonAbandoned:
		input.EndReason = history.EndReasonAbandoned
	default:
		input.EndReason = ""
	}
	return input
}
