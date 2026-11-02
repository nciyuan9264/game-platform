package adminrpc

import (
	"strconv"

	"github.com/nciyuan9264/game-platform/internal/adminanalytics"
	admin "github.com/nciyuan9264/game-platform/kitex_gen/pam/game/admin/v1"
)

func mapWindow(value adminanalytics.Window) *admin.AnalyticsWindow {
	return &admin.AnalyticsWindow{
		Timezone:        adminanalytics.Timezone,
		StartDate:       value.StartDate(),
		EndDate:         value.EndDate(),
		Days:            int32(value.Days),
		GeneratedAtUnix: value.GeneratedAt.Unix(),
	}
}

func mapOverview(value adminanalytics.Overview) *admin.OverviewData {
	daily := make([]*admin.DailyOverview, 0, len(value.Daily))
	for _, item := range value.Daily {
		daily = append(daily, &admin.DailyOverview{
			Date:                  item.Date,
			NewUsers_:             item.NewUsers,
			ActiveUsers:           item.ActiveUsers,
			GamesStarted:          item.GamesStarted,
			GamesCompleted:        item.GamesCompleted,
			GamesAbandoned:        item.GamesAbandoned,
			GameDurationSeconds:   item.GameDurationSeconds,
			PlayerDurationSeconds: item.PlayerDurationSeconds,
		})
	}
	metrics := value.Metrics
	return &admin.OverviewData{
		Window: mapWindow(value.Window),
		Metrics: &admin.OverviewMetrics{
			TotalUsers:                   metrics.TotalUsers,
			NewUsers_:                    metrics.NewUsers,
			Dau:                          metrics.DAU,
			Wau:                          metrics.WAU,
			Mau:                          metrics.MAU,
			DauMauRate:                   metrics.DAUMAURate,
			FirstMatchConversionRate:     metrics.FirstMatchConversionRate,
			D1RetentionRate:              metrics.D1RetentionRate,
			D7RetentionRate:              metrics.D7RetentionRate,
			D30RetentionRate:             metrics.D30RetentionRate,
			GamesStarted:                 metrics.GamesStarted,
			GamesCompleted:               metrics.GamesCompleted,
			GamesAbandoned:               metrics.GamesAbandoned,
			CompletionRate:               metrics.CompletionRate,
			GameDurationSeconds:          metrics.GameDurationSeconds,
			PlayerDurationSeconds:        metrics.PlayerDurationSeconds,
			AverageGameDurationSeconds:   metrics.AverageGameDurationSecond,
			AveragePlayerDurationSeconds: metrics.AveragePlayerDuration,
		},
		Daily: daily,
	}
}

func mapUser(value adminanalytics.User) *admin.AdminUserSummary {
	result := &admin.AdminUserSummary{
		UserId:                formatUserID(value.ID),
		DisplayName:           value.DisplayName,
		CreatedAtUnix:         value.CreatedAt.Unix(),
		ActiveDays:            value.ActiveDays,
		GamesPlayed:           value.GamesPlayed,
		PlayerDurationSeconds: value.PlayerDurationSeconds,
	}
	result.Email = optionalString(value.Email)
	result.Avatar = optionalString(value.Avatar)
	result.FirstActiveDate = optionalString(value.FirstActiveDate)
	result.FavoriteGameType = optionalString(value.FavoriteGameType)
	if value.LastActiveAt != nil {
		unix := value.LastActiveAt.Unix()
		result.LastActiveAtUnix = &unix
	}
	return result
}

func mapUsers(value adminanalytics.UsersPage) *admin.UsersData {
	users := make([]*admin.AdminUserSummary, 0, len(value.Users))
	for _, user := range value.Users {
		users = append(users, mapUser(user))
	}
	return &admin.UsersData{
		Window:   mapWindow(value.Window),
		Users:    users,
		Page:     int32(value.Page),
		PageSize: int32(value.PageSize),
		Total:    value.Total,
	}
}

func mapUserActivity(value adminanalytics.UserActivity) *admin.UserActivityData {
	daily := make([]*admin.UserDailyActivity, 0, len(value.Daily))
	for _, item := range value.Daily {
		daily = append(daily, &admin.UserDailyActivity{
			Date: item.Date, GamesPlayed: item.GamesPlayed, PlayerDurationSeconds: item.PlayerDurationSeconds,
		})
	}
	games := make([]*admin.UserGameActivity, 0, len(value.Games))
	for _, item := range value.Games {
		games = append(games, &admin.UserGameActivity{
			GameType: item.GameType, GamesPlayed: item.GamesPlayed, ActiveDays: item.ActiveDays,
			PlayerDurationSeconds: item.PlayerDurationSecond, ShareRate: item.ShareRate,
		})
	}
	return &admin.UserActivityData{Window: mapWindow(value.Window), User: mapUser(value.User), Daily: daily, Games: games}
}

func mapGames(value adminanalytics.Games) *admin.GamesData {
	games := make([]*admin.GameActivitySummary, 0, len(value.Games))
	for _, item := range value.Games {
		games = append(games, &admin.GameActivitySummary{
			GameType: item.GameType, ActiveUsers: item.ActiveUsers,
			GamesStarted: item.GamesStarted, GamesCompleted: item.GamesCompleted,
			GamesAbandoned: item.GamesAbandoned,
			CompletionRate: item.CompletionRate, GameDurationSeconds: item.GameDurationSeconds,
			PlayerDurationSeconds:      item.PlayerDurationSeconds,
			AverageGameDurationSeconds: item.AverageGameDuration,
			AverageHumanPlayers:        item.AverageHumanPlayerCount,
		})
	}
	return &admin.GamesData{Window: mapWindow(value.Window), Games: games}
}

func mapLive(value adminanalytics.Live) *admin.LiveData {
	games := make([]*admin.LiveGameSummary, 0, len(value.Games))
	for _, item := range value.Games {
		games = append(games, &admin.LiveGameSummary{
			GameType: item.GameType, ActiveRooms: item.ActiveRooms,
			ActiveMatches: item.ActiveMatches, OnlinePlayers: item.OnlinePlayers,
		})
	}
	rooms := make([]*admin.LiveRoomSummary, 0, len(value.Rooms))
	for _, item := range value.Rooms {
		rooms = append(rooms, &admin.LiveRoomSummary{
			RoomId: item.RoomID, MatchId: item.MatchID, GameType: item.GameType,
			Status: item.Status, HumanPlayers: item.HumanPlayers, OnlinePlayers: item.OnlinePlayers,
			StartedAtUnix: item.StartedAt.Unix(),
		})
	}
	return &admin.LiveData{
		GeneratedAtUnix: value.GeneratedAt.Unix(), ActiveRooms: value.ActiveRooms,
		ActiveMatches: value.ActiveMatches, OnlinePlayers: value.OnlinePlayers,
		Games: games, Rooms: rooms,
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func formatUserID(value int64) string {
	return strconv.FormatInt(value, 10)
}
