package adminanalytics

import (
	"context"
	"fmt"

	"github.com/nciyuan9264/game-platform/internal/history"
)

func (s *Service) Games(ctx context.Context) (Games, error) {
	return cachedValue(ctx, s, "games", func(ctx context.Context) (Games, error) {
		window := s.reportingWindow()
		rows, err := s.gameDB.QueryContext(ctx, `
			WITH game_stats AS (
				SELECT game_type, COUNT(*) AS games_started,
					COUNT(*) FILTER (
						WHERE ended_at IS NOT NULL AND end_reason = $3
					) AS games_completed,
					COUNT(*) FILTER (
						WHERE ended_at IS NOT NULL AND end_reason = $4
					) AS games_abandoned,
					COALESCE(SUM(duration_seconds) FILTER (
						WHERE ended_at IS NOT NULL AND end_reason = $3
					), 0) AS game_seconds
				FROM games
				WHERE started_at >= $1 AND started_at < $2
				GROUP BY game_type
			), player_stats AS (
				SELECT g.game_type, COUNT(DISTINCT gp.user_id) AS active_users,
					COUNT(*) AS player_games,
					COALESCE(SUM(g.duration_seconds) FILTER (WHERE g.ended_at IS NOT NULL), 0) AS player_seconds
				FROM games g
				JOIN game_players gp ON gp.game_id = g.id
				WHERE g.started_at >= $1 AND g.started_at < $2
					AND NOT gp.is_ai AND gp.user_id IS NOT NULL
				GROUP BY g.game_type
			)
			SELECT catalog.game_type,
				COALESCE(player_stats.active_users, 0),
				COALESCE(game_stats.games_started, 0),
				COALESCE(game_stats.games_completed, 0),
				COALESCE(game_stats.games_abandoned, 0),
				COALESCE(game_stats.game_seconds, 0),
				COALESCE(player_stats.player_seconds, 0),
				COALESCE(player_stats.player_games, 0)
			FROM game_catalog catalog
			LEFT JOIN game_stats USING (game_type)
			LEFT JOIN player_stats USING (game_type)
			ORDER BY COALESCE(player_stats.player_seconds, 0) DESC,
				COALESCE(game_stats.games_started, 0) DESC, catalog.game_type`,
			window.StartUTC, window.EndUTC, history.EndReasonCompleted, history.EndReasonAbandoned,
		)
		if err != nil {
			return Games{}, fmt.Errorf("query game analytics: %w", err)
		}
		defer rows.Close()
		items := []GameActivity{}
		for rows.Next() {
			var item GameActivity
			var playerGames int64
			if err := rows.Scan(
				&item.GameType,
				&item.ActiveUsers,
				&item.GamesStarted,
				&item.GamesCompleted,
				&item.GamesAbandoned,
				&item.GameDurationSeconds,
				&item.PlayerDurationSeconds,
				&playerGames,
			); err != nil {
				return Games{}, err
			}
			item.CompletionRate = percentage(item.GamesCompleted, item.GamesStarted)
			if item.GamesCompleted > 0 {
				item.AverageGameDuration = item.GameDurationSeconds / item.GamesCompleted
			}
			if item.GamesStarted > 0 {
				item.AverageHumanPlayerCount = float64(playerGames) / float64(item.GamesStarted)
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			return Games{}, err
		}
		return Games{Window: window, Games: items}, nil
	})
}
