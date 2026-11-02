package adminrpc

import (
	"context"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/adminanalytics"
	admin "github.com/nciyuan9264/game-platform/kitex_gen/pam/game/admin/v1"
)

type fakeAnalytics struct {
	overview      adminanalytics.Overview
	overviewError error
	overviewQuery *adminanalytics.DateRangeQuery
	live          adminanalytics.Live
}

func (f fakeAnalytics) Overview(_ context.Context, query adminanalytics.DateRangeQuery) (adminanalytics.Overview, error) {
	if f.overviewQuery != nil {
		*f.overviewQuery = query
	}
	return f.overview, f.overviewError
}

func (fakeAnalytics) Users(context.Context, adminanalytics.UserQuery) (adminanalytics.UsersPage, error) {
	return adminanalytics.UsersPage{}, nil
}

func (fakeAnalytics) UserActivity(context.Context, int64) (adminanalytics.UserActivity, error) {
	return adminanalytics.UserActivity{}, nil
}

func (fakeAnalytics) Games(context.Context) (adminanalytics.Games, error) {
	return adminanalytics.Games{}, nil
}

func (f fakeAnalytics) Live(context.Context) (adminanalytics.Live, error) {
	return f.live, nil
}

func TestGetOverviewMapsAnalyticsResponse(t *testing.T) {
	location, err := time.LoadLocation(adminanalytics.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 11, 2, 0, 0, 0, 0, location)
	var query adminanalytics.DateRangeQuery
	handler := NewHandler(fakeAnalytics{overviewQuery: &query, overview: adminanalytics.Overview{
		Window: adminanalytics.Window{
			Start:       end.AddDate(0, 0, -29),
			End:         end,
			Days:        30,
			GeneratedAt: end.UTC(),
		},
		Metrics: adminanalytics.OverviewMetrics{
			TotalUsers: 12, TotalGames: 20, DAU: 3, MAU: 6, GamesCompleted: 4, GamesAbandoned: 2,
		},
		Daily: []adminanalytics.DailyOverview{{
			Date: "2026-11-02", NewUsers: 2, GamesCompleted: 1, GamesAbandoned: 1,
		}},
	}})
	response, err := handler.GetOverview(context.Background(), &admin.OverviewReq{
		StartDate: optionalString("2026-10-27"),
		EndDate:   optionalString("2026-11-02"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStatusCode() != 200 || response.GetData().GetMetrics().GetTotalUsers() != 12 {
		t.Fatalf("response = %+v", response)
	}
	if got := response.GetData().GetMetrics().GetTotalGames(); got != 20 {
		t.Fatalf("total games = %d", got)
	}
	if got := response.GetData().GetDaily()[0].GetNewUsers_(); got != 2 {
		t.Fatalf("new users = %d", got)
	}
	if got := response.GetData().GetMetrics().GetGamesAbandoned(); got != 2 {
		t.Fatalf("abandoned games = %d", got)
	}
	if got := response.GetData().GetDaily()[0].GetGamesAbandoned(); got != 1 {
		t.Fatalf("daily abandoned games = %d", got)
	}
	if query.StartDate != "2026-10-27" || query.EndDate != "2026-11-02" {
		t.Fatalf("query = %+v", query)
	}
}

func TestGetOverviewMapsInvalidDateRange(t *testing.T) {
	handler := NewHandler(fakeAnalytics{overviewError: adminanalytics.ErrInvalidDateRange})
	_, err := handler.GetOverview(context.Background(), &admin.OverviewReq{})
	if err == nil {
		t.Fatal("expected invalid request error")
	}
	adminErr, ok := err.(*admin.GameAdminError)
	if !ok || adminErr.GetCode() != "invalid_request" {
		t.Fatalf("error = %v", err)
	}
}

func TestGetLiveMapsPlayerNames(t *testing.T) {
	analytics := fakeAnalytics{}
	analytics.live = adminanalytics.Live{
		GeneratedAt: time.Unix(100, 0),
		Rooms: []adminanalytics.LiveRoom{{
			RoomID: "room-1", MatchID: "match-1", GameType: "acquire",
			Status: "preparing", HumanPlayers: 1, StartedAt: time.Unix(90, 0),
			Players: []adminanalytics.LivePlayer{{
				UserID: "7", DisplayName: "北海玩家", Avatar: "https://example.com/avatar.png",
				Online: true, IsOwner: true,
			}},
		}},
	}
	handler := NewHandler(analytics)
	response, err := handler.GetLive(context.Background(), &admin.EmptyReq{})
	if err != nil {
		t.Fatal(err)
	}
	players := response.GetData().GetRooms()[0].GetPlayers()
	if len(players) != 1 || players[0].GetDisplayName() != "北海玩家" || !players[0].GetIsOwner() {
		t.Fatalf("players = %+v", players)
	}
}

func TestGetUserActivityRejectsInvalidUserID(t *testing.T) {
	handler := NewHandler(fakeAnalytics{})
	_, err := handler.GetUserActivity(context.Background(), &admin.GetUserActivityReq{UserId: "not-a-user"})
	if err == nil {
		t.Fatal("expected invalid request error")
	}
	adminErr, ok := err.(*admin.GameAdminError)
	if !ok || adminErr.GetCode() != "invalid_request" {
		t.Fatalf("error = %v", err)
	}
}
