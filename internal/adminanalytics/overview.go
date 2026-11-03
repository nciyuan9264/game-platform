package adminanalytics

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/nciyuan9264/game-platform/internal/history"
)

type authOverview struct {
	totalUsers int64
	newUsers   int64
	daily      map[string]int64
	recentIDs  []int64
}

func (s *Service) Overview(ctx context.Context, query OverviewQuery) (Overview, error) {
	window, windowErr := s.reportingWindowForOverview(ctx, query)
	if windowErr != nil {
		return Overview{}, windowErr
	}
	key := fmt.Sprintf("overview:%t:%s:%s", query.AllTime, window.StartDate(), window.EndDate())
	return cachedValue(ctx, s, key, func(ctx context.Context) (Overview, error) {
		authData, err := s.loadAuthOverview(ctx, window)
		if err != nil {
			return Overview{}, fmt.Errorf("load auth overview: %w", err)
		}
		daily, metrics, err := s.loadGameOverview(ctx, window)
		if err != nil {
			return Overview{}, fmt.Errorf("load game overview: %w", err)
		}
		for index := range daily {
			daily[index].NewUsers = authData.daily[daily[index].Date]
		}
		converted, err := s.countConvertedUsers(ctx, authData.recentIDs, window.EndUTC)
		if err != nil {
			return Overview{}, fmt.Errorf("count converted users: %w", err)
		}
		metrics.TotalUsers = authData.totalUsers
		metrics.NewUsers = authData.newUsers
		metrics.FirstMatchConversionRate = percentage(converted, int64(len(authData.recentIDs)))
		return Overview{Window: window, Metrics: metrics, Daily: daily}, nil
	})
}

func (s *Service) loadAuthOverview(ctx context.Context, window Window) (authOverview, error) {
	result := authOverview{daily: make(map[string]int64)}
	if err := s.authDB.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE created_at >= $1 AND created_at < $2)
		FROM public.users
		WHERE created_at < $2 AND (deleted_at IS NULL OR deleted_at >= $2)`, window.StartUTC, window.EndUTC).Scan(
		&result.totalUsers,
		&result.newUsers,
	); err != nil {
		return authOverview{}, err
	}
	rows, err := s.authDB.QueryContext(ctx, `
		WITH dates AS (
			SELECT generate_series($1::date, $2::date, interval '1 day')::date AS day
		), registrations AS (
			SELECT (created_at AT TIME ZONE 'Asia/Shanghai')::date AS day, COUNT(*) AS users
			FROM public.users
			WHERE created_at >= $3 AND created_at < $4
				AND (deleted_at IS NULL OR deleted_at >= $4)
			GROUP BY 1
		)
		SELECT dates.day, COALESCE(registrations.users, 0)
		FROM dates LEFT JOIN registrations USING (day)
		ORDER BY dates.day`, window.StartDate(), window.EndDate(), window.StartUTC, window.EndUTC)
	if err != nil {
		return authOverview{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var day time.Time
		var count int64
		if err := rows.Scan(&day, &count); err != nil {
			return authOverview{}, err
		}
		result.daily[day.Format(time.DateOnly)] = count
	}
	if err := rows.Err(); err != nil {
		return authOverview{}, err
	}
	rows, err = s.authDB.QueryContext(ctx, `
		SELECT id
		FROM public.users
		WHERE created_at >= $1 AND created_at < $2
			AND (deleted_at IS NULL OR deleted_at >= $2)
		ORDER BY id`, window.StartUTC, window.EndUTC)
	if err != nil {
		return authOverview{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return authOverview{}, err
		}
		result.recentIDs = append(result.recentIDs, id)
	}
	return result, rows.Err()
}

func (s *Service) countConvertedUsers(ctx context.Context, userIDs []int64, endUTC time.Time) (int64, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}
	var count int64
	err := s.gameDB.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT gp.user_id)
		FROM game_players gp
		JOIN games g ON g.id = gp.game_id
		WHERE NOT gp.is_ai AND gp.user_id = ANY($1) AND g.started_at < $2`,
		pq.Array(userIDs),
		endUTC,
	).Scan(&count)
	return count, err
}

func (s *Service) loadGameOverview(ctx context.Context, window Window) ([]DailyOverview, OverviewMetrics, error) {
	metrics := OverviewMetrics{}
	if err := s.gameDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM games
		WHERE started_at < $1`, window.EndUTC).Scan(&metrics.TotalGames); err != nil {
		return nil, OverviewMetrics{}, err
	}
	rows, err := s.gameDB.QueryContext(ctx, `
		WITH dates AS (
			SELECT generate_series($1::date, $2::date, interval '1 day')::date AS day
		), game_daily AS (
			SELECT
				(started_at AT TIME ZONE 'Asia/Shanghai')::date AS day,
				COUNT(*) AS games_started,
				COUNT(*) FILTER (
					WHERE ended_at IS NOT NULL AND end_reason = $5
				) AS games_completed,
				COUNT(*) FILTER (
					WHERE ended_at IS NOT NULL AND end_reason = $6
				) AS games_abandoned,
				COALESCE(SUM(duration_seconds) FILTER (
					WHERE ended_at IS NOT NULL AND end_reason = $5
				), 0) AS game_seconds
			FROM games
			WHERE started_at >= $3 AND started_at < $4
			GROUP BY 1
		), player_daily AS (
			SELECT
				(g.started_at AT TIME ZONE 'Asia/Shanghai')::date AS day,
				COUNT(DISTINCT gp.user_id) AS active_users,
				COALESCE(SUM(g.duration_seconds) FILTER (WHERE g.ended_at IS NOT NULL), 0) AS player_seconds
			FROM games g
			JOIN game_players gp ON gp.game_id = g.id
			WHERE g.started_at >= $3 AND g.started_at < $4
				AND NOT gp.is_ai AND gp.user_id IS NOT NULL
			GROUP BY 1
		)
		SELECT
			dates.day,
			COALESCE(player_daily.active_users, 0),
			COALESCE(game_daily.games_started, 0),
			COALESCE(game_daily.games_completed, 0),
			COALESCE(game_daily.games_abandoned, 0),
			COALESCE(game_daily.game_seconds, 0),
			COALESCE(player_daily.player_seconds, 0)
		FROM dates
		LEFT JOIN game_daily USING (day)
		LEFT JOIN player_daily USING (day)
		ORDER BY dates.day`,
		window.StartDate(), window.EndDate(), window.StartUTC, window.EndUTC,
		history.EndReasonCompleted, history.EndReasonAbandoned,
	)
	if err != nil {
		return nil, OverviewMetrics{}, err
	}
	defer rows.Close()
	daily := make([]DailyOverview, 0, window.Days)
	for rows.Next() {
		var day time.Time
		var item DailyOverview
		if err := rows.Scan(
			&day,
			&item.ActiveUsers,
			&item.GamesStarted,
			&item.GamesCompleted,
			&item.GamesAbandoned,
			&item.GameDurationSeconds,
			&item.PlayerDurationSeconds,
		); err != nil {
			return nil, OverviewMetrics{}, err
		}
		item.Date = day.Format(time.DateOnly)
		daily = append(daily, item)
		metrics.GamesStarted += item.GamesStarted
		metrics.GamesCompleted += item.GamesCompleted
		metrics.GamesAbandoned += item.GamesAbandoned
		metrics.GameDurationSeconds += item.GameDurationSeconds
		metrics.PlayerDurationSeconds += item.PlayerDurationSeconds
	}
	if err := rows.Err(); err != nil {
		return nil, OverviewMetrics{}, err
	}
	if err := s.loadActiveUserMetrics(ctx, window, &metrics); err != nil {
		return nil, OverviewMetrics{}, err
	}
	if err := s.loadRetentionMetrics(ctx, window, &metrics); err != nil {
		return nil, OverviewMetrics{}, err
	}
	metrics.DAUMAURate = percentage(metrics.DAU, metrics.MAU)
	metrics.CompletionRate = percentage(metrics.GamesCompleted, metrics.GamesStarted)
	if metrics.GamesCompleted > 0 {
		metrics.AverageGameDurationSecond = metrics.GameDurationSeconds / metrics.GamesCompleted
	}
	if metrics.MAU > 0 {
		metrics.AveragePlayerDuration = metrics.PlayerDurationSeconds / metrics.MAU
	}
	return daily, metrics, nil
}

func (s *Service) loadActiveUserMetrics(ctx context.Context, window Window, metrics *OverviewMetrics) error {
	wauStart := window.End.AddDate(0, 0, -6).Format(time.DateOnly)
	return s.gameDB.QueryRowContext(ctx, `
		WITH user_days AS (
			SELECT DISTINCT
				gp.user_id,
				(g.started_at AT TIME ZONE 'Asia/Shanghai')::date AS day
			FROM games g
			JOIN game_players gp ON gp.game_id = g.id
			WHERE g.started_at >= $1 AND g.started_at < $2
				AND NOT gp.is_ai AND gp.user_id IS NOT NULL
		)
		SELECT
			COUNT(DISTINCT user_id) FILTER (WHERE day = $3::date),
			COUNT(DISTINCT user_id) FILTER (WHERE day >= $4::date),
			COUNT(DISTINCT user_id)
		FROM user_days`, window.StartUTC, window.EndUTC, window.EndDate(), wauStart).Scan(
		&metrics.DAU,
		&metrics.WAU,
		&metrics.MAU,
	)
}

func (s *Service) loadRetentionMetrics(ctx context.Context, window Window, metrics *OverviewMetrics) error {
	cohortRange := func(offsetStart, offsetEnd int) (string, string) {
		return window.End.AddDate(0, 0, -offsetStart).Format(time.DateOnly),
			window.End.AddDate(0, 0, -offsetEnd).Format(time.DateOnly)
	}
	d1Start, d1End := cohortRange(30, 1)
	d7Start, d7End := cohortRange(36, 7)
	d30Start, d30End := cohortRange(59, 30)
	var d1Base, d1Retained, d7Base, d7Retained, d30Base, d30Retained int64
	err := s.gameDB.QueryRowContext(ctx, `
		WITH user_days AS (
			SELECT DISTINCT gp.user_id, (g.started_at AT TIME ZONE 'Asia/Shanghai')::date AS day
			FROM games g
			JOIN game_players gp ON gp.game_id = g.id
			WHERE NOT gp.is_ai AND gp.user_id IS NOT NULL
		), first_activity AS (
			SELECT user_id, MIN(day) AS first_day
			FROM user_days
			GROUP BY user_id
		)
		SELECT
			COUNT(*) FILTER (WHERE first_day BETWEEN $1::date AND $2::date),
			COUNT(*) FILTER (WHERE first_day BETWEEN $1::date AND $2::date AND EXISTS (
				SELECT 1 FROM user_days d WHERE d.user_id = first_activity.user_id AND d.day = first_day + 1
			)),
			COUNT(*) FILTER (WHERE first_day BETWEEN $3::date AND $4::date),
			COUNT(*) FILTER (WHERE first_day BETWEEN $3::date AND $4::date AND EXISTS (
				SELECT 1 FROM user_days d WHERE d.user_id = first_activity.user_id AND d.day = first_day + 7
			)),
			COUNT(*) FILTER (WHERE first_day BETWEEN $5::date AND $6::date),
			COUNT(*) FILTER (WHERE first_day BETWEEN $5::date AND $6::date AND EXISTS (
				SELECT 1 FROM user_days d WHERE d.user_id = first_activity.user_id AND d.day = first_day + 30
			))
		FROM first_activity`, d1Start, d1End, d7Start, d7End, d30Start, d30End).Scan(
		&d1Base, &d1Retained, &d7Base, &d7Retained, &d30Base, &d30Retained,
	)
	if err != nil {
		return err
	}
	metrics.D1RetentionRate = percentage(d1Retained, d1Base)
	metrics.D7RetentionRate = percentage(d7Retained, d7Base)
	metrics.D30RetentionRate = percentage(d30Retained, d30Base)
	return nil
}
