package adminanalytics

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

type activityStats struct {
	firstActiveDate       string
	lastActiveAt          *time.Time
	activeDays            int64
	gamesPlayed           int64
	playerDurationSeconds int64
	favoriteGameType      string
}

func (s *Service) Users(ctx context.Context, query UserQuery) (UsersPage, error) {
	query = normalizeUserQuery(query)
	key := fmt.Sprintf(
		"users:all:%s:%s:%s",
		strings.ToLower(query.Query), query.SortBy, query.SortOrder,
	)
	allUsers, err := cachedValue(ctx, s, key, func(ctx context.Context) (UsersPage, error) {
		window := s.reportingWindow()
		users, err := s.loadAuthUsers(ctx, query)
		if err != nil {
			return UsersPage{}, fmt.Errorf("load auth users: %w", err)
		}
		ids := make([]int64, 0, len(users))
		for _, user := range users {
			ids = append(ids, user.ID)
		}
		stats, err := s.loadActivityStats(ctx, ids, window)
		if err != nil {
			return UsersPage{}, fmt.Errorf("load user activity stats: %w", err)
		}
		for index := range users {
			applyActivityStats(&users[index], stats[users[index].ID])
		}
		sortUsers(users, query)
		total := int64(len(users))
		return UsersPage{
			Window: window, Users: users, Total: total,
		}, nil
	})
	if err != nil {
		return UsersPage{}, err
	}
	allUsers.Users = paginateUsers(allUsers.Users, query.Page, query.PageSize)
	allUsers.Page = query.Page
	allUsers.PageSize = query.PageSize
	return allUsers, nil
}

func (s *Service) loadAuthUsers(ctx context.Context, query UserQuery) ([]User, error) {
	filter := strings.TrimSpace(query.Query)
	rows, err := s.authDB.QueryContext(ctx, `
		SELECT
			id,
			COALESCE(NULLIF(btrim(name), ''), NULLIF(split_part(email, '@', 1), ''), '用户 ' || id::text),
			COALESCE(email, ''),
			COALESCE(avatar, ''),
			COALESCE(created_at, to_timestamp(0))
		FROM public.users
		WHERE deleted_at IS NULL
			AND ($1 = '' OR id::text = $1 OR COALESCE(name, '') ILIKE '%' || $1 || '%'
				OR COALESCE(email, '') ILIKE '%' || $1 || '%')
		ORDER BY created_at DESC NULLS LAST, id DESC`, filter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.DisplayName, &user.Email, &user.Avatar, &user.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Service) loadActivityStats(ctx context.Context, userIDs []int64, window Window) (map[int64]activityStats, error) {
	result := make(map[int64]activityStats, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	rows, err := s.gameDB.QueryContext(ctx, `
		WITH requested AS (
			SELECT unnest($1::bigint[]) AS user_id
		), user_games AS (
			SELECT gp.user_id, g.game_type, g.started_at, g.duration_seconds,
				(g.started_at AT TIME ZONE 'Asia/Shanghai')::date AS active_date
			FROM game_players gp
			JOIN games g ON g.id = gp.game_id
			WHERE NOT gp.is_ai AND gp.user_id = ANY($1)
		), all_stats AS (
			SELECT user_id, MIN(active_date) AS first_active_date, MAX(started_at) AS last_active_at
			FROM user_games GROUP BY user_id
		), window_stats AS (
			SELECT user_id,
				COUNT(DISTINCT active_date) AS active_days,
				COUNT(*) AS games_played,
				COALESCE(SUM(duration_seconds), 0) AS player_seconds
			FROM user_games
			WHERE started_at >= $2 AND started_at < $3
			GROUP BY user_id
		), game_counts AS (
			SELECT user_id, game_type, COUNT(*) AS games_played
			FROM user_games
			WHERE started_at >= $2 AND started_at < $3
			GROUP BY user_id, game_type
		), favorites AS (
			SELECT user_id, game_type
			FROM (
				SELECT user_id, game_type,
					ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY games_played DESC, game_type) AS rank
				FROM game_counts
			) ranked
			WHERE rank = 1
		)
		SELECT requested.user_id, all_stats.first_active_date, all_stats.last_active_at,
			COALESCE(window_stats.active_days, 0), COALESCE(window_stats.games_played, 0),
			COALESCE(window_stats.player_seconds, 0), COALESCE(favorites.game_type, '')
		FROM requested
		LEFT JOIN all_stats USING (user_id)
		LEFT JOIN window_stats USING (user_id)
		LEFT JOIN favorites USING (user_id)`, pq.Array(userIDs), window.StartUTC, window.EndUTC)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var firstActive sql.NullTime
		var lastActive sql.NullTime
		var stats activityStats
		if err := rows.Scan(
			&id, &firstActive, &lastActive, &stats.activeDays, &stats.gamesPlayed,
			&stats.playerDurationSeconds, &stats.favoriteGameType,
		); err != nil {
			return nil, err
		}
		if firstActive.Valid {
			stats.firstActiveDate = firstActive.Time.Format(time.DateOnly)
		}
		if lastActive.Valid {
			value := lastActive.Time.UTC()
			stats.lastActiveAt = &value
		}
		result[id] = stats
	}
	return result, rows.Err()
}

func applyActivityStats(user *User, stats activityStats) {
	user.FirstActiveDate = stats.firstActiveDate
	user.LastActiveAt = stats.lastActiveAt
	user.ActiveDays = stats.activeDays
	user.GamesPlayed = stats.gamesPlayed
	user.PlayerDurationSeconds = stats.playerDurationSeconds
	user.FavoriteGameType = stats.favoriteGameType
}

func sortUsers(users []User, query UserQuery) {
	sort.SliceStable(users, func(leftIndex, rightIndex int) bool {
		left := users[leftIndex]
		right := users[rightIndex]
		leftMissing := userSortValueMissing(left, query.SortBy)
		rightMissing := userSortValueMissing(right, query.SortBy)
		if leftMissing != rightMissing {
			return !leftMissing
		}
		comparison := compareUserSortValue(left, right, query.SortBy)
		if comparison == 0 {
			comparison = cmp.Compare(left.ID, right.ID)
		}
		if query.SortOrder == "desc" {
			return comparison > 0
		}
		return comparison < 0
	})
}

func userSortValueMissing(user User, sortBy string) bool {
	switch sortBy {
	case "last_active_at":
		return user.LastActiveAt == nil
	case "favorite_game":
		return user.FavoriteGameType == ""
	default:
		return false
	}
}

func compareUserSortValue(left, right User, sortBy string) int {
	switch sortBy {
	case "user":
		return strings.Compare(strings.ToLower(left.DisplayName), strings.ToLower(right.DisplayName))
	case "created_at":
		return left.CreatedAt.Compare(right.CreatedAt)
	case "last_active_at":
		if left.LastActiveAt == nil || right.LastActiveAt == nil {
			return 0
		}
		return left.LastActiveAt.Compare(*right.LastActiveAt)
	case "active_days":
		return cmp.Compare(left.ActiveDays, right.ActiveDays)
	case "games_played":
		return cmp.Compare(left.GamesPlayed, right.GamesPlayed)
	case "player_duration":
		return cmp.Compare(left.PlayerDurationSeconds, right.PlayerDurationSeconds)
	case "favorite_game":
		return strings.Compare(left.FavoriteGameType, right.FavoriteGameType)
	default:
		return 0
	}
}

func paginateUsers(users []User, page, pageSize int) []User {
	start := (page - 1) * pageSize
	if start >= len(users) {
		return []User{}
	}
	end := min(start+pageSize, len(users))
	return users[start:end]
}

func (s *Service) UserActivity(ctx context.Context, userID int64) (UserActivity, error) {
	key := fmt.Sprintf("user:%d", userID)
	return cachedValue(ctx, s, key, func(ctx context.Context) (UserActivity, error) {
		window := s.reportingWindow()
		user, err := s.loadAuthUser(ctx, userID)
		if err != nil {
			return UserActivity{}, err
		}
		stats, err := s.loadActivityStats(ctx, []int64{userID}, window)
		if err != nil {
			return UserActivity{}, fmt.Errorf("load user activity stats: %w", err)
		}
		applyActivityStats(&user, stats[userID])
		daily, err := s.loadUserDailyActivity(ctx, userID, window)
		if err != nil {
			return UserActivity{}, fmt.Errorf("load user daily activity: %w", err)
		}
		games, err := s.loadUserGameActivity(ctx, userID, window, user.GamesPlayed)
		if err != nil {
			return UserActivity{}, fmt.Errorf("load user game activity: %w", err)
		}
		return UserActivity{Window: window, User: user, Daily: daily, Games: games}, nil
	})
}

func (s *Service) loadAuthUser(ctx context.Context, userID int64) (User, error) {
	var user User
	err := s.authDB.QueryRowContext(ctx, `
		SELECT id,
			COALESCE(NULLIF(btrim(name), ''), NULLIF(split_part(email, '@', 1), ''), '用户 ' || id::text),
			COALESCE(email, ''), COALESCE(avatar, ''), COALESCE(created_at, to_timestamp(0))
		FROM public.users
		WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(
		&user.ID, &user.DisplayName, &user.Email, &user.Avatar, &user.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load auth user: %w", err)
	}
	return user, nil
}

func (s *Service) loadUserDailyActivity(ctx context.Context, userID int64, window Window) ([]UserDailyActivity, error) {
	rows, err := s.gameDB.QueryContext(ctx, `
		SELECT (g.started_at AT TIME ZONE 'Asia/Shanghai')::date AS day,
			COUNT(*), COALESCE(SUM(g.duration_seconds), 0)
		FROM game_players gp
		JOIN games g ON g.id = gp.game_id
		WHERE gp.user_id = $1 AND NOT gp.is_ai AND g.started_at >= $2 AND g.started_at < $3
		GROUP BY 1 ORDER BY 1`, userID, window.StartUTC, window.EndUTC)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDate := make(map[string]UserDailyActivity, window.Days)
	for rows.Next() {
		var day time.Time
		var item UserDailyActivity
		if err := rows.Scan(&day, &item.GamesPlayed, &item.PlayerDurationSeconds); err != nil {
			return nil, err
		}
		item.Date = day.Format(time.DateOnly)
		byDate[item.Date] = item
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]UserDailyActivity, 0, window.Days)
	for day := window.Start; !day.After(window.End); day = day.AddDate(0, 0, 1) {
		date := day.Format(time.DateOnly)
		item := byDate[date]
		item.Date = date
		result = append(result, item)
	}
	return result, nil
}

func (s *Service) loadUserGameActivity(ctx context.Context, userID int64, window Window, totalGames int64) ([]UserGameActivity, error) {
	rows, err := s.gameDB.QueryContext(ctx, `
		SELECT g.game_type, COUNT(*),
			COUNT(DISTINCT (g.started_at AT TIME ZONE 'Asia/Shanghai')::date),
			COALESCE(SUM(g.duration_seconds), 0)
		FROM game_players gp
		JOIN games g ON g.id = gp.game_id
		WHERE gp.user_id = $1 AND NOT gp.is_ai AND g.started_at >= $2 AND g.started_at < $3
		GROUP BY g.game_type
		ORDER BY COUNT(*) DESC, g.game_type`, userID, window.StartUTC, window.EndUTC)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []UserGameActivity{}
	for rows.Next() {
		var item UserGameActivity
		if err := rows.Scan(
			&item.GameType, &item.GamesPlayed, &item.ActiveDays, &item.PlayerDurationSecond,
		); err != nil {
			return nil, err
		}
		item.ShareRate = percentage(item.GamesPlayed, totalGames)
		result = append(result, item)
	}
	return result, rows.Err()
}
