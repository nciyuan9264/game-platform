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
	overviewQuery *adminanalytics.OverviewQuery
	gamesQuery    *adminanalytics.GamesQuery
	matches       adminanalytics.MatchesPage
	matchesQuery  *adminanalytics.MatchQuery
	live          adminanalytics.Live
}

func (f fakeAnalytics) Overview(_ context.Context, query adminanalytics.OverviewQuery) (adminanalytics.Overview, error) {
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

func (f fakeAnalytics) Games(_ context.Context, query adminanalytics.GamesQuery) (adminanalytics.Games, error) {
	if f.gamesQuery != nil {
		*f.gamesQuery = query
	}
	return adminanalytics.Games{}, nil
}

func (f fakeAnalytics) Matches(_ context.Context, query adminanalytics.MatchQuery) (adminanalytics.MatchesPage, error) {
	if f.matchesQuery != nil {
		*f.matchesQuery = query
	}
	return f.matches, nil
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
	var query adminanalytics.OverviewQuery
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
		AcquisitionSources: []adminanalytics.AcquisitionSourceMetric{{
			Source: "friend", Label: "朋友推荐", Users: 2,
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
	if sources := response.GetData().GetAcquisitionSources(); len(sources) != 1 ||
		sources[0].GetSource() != "friend" || sources[0].GetUsers() != 2 {
		t.Fatalf("acquisition sources = %+v", sources)
	}
	if query.DateRange.StartDate != "2026-10-27" || query.DateRange.EndDate != "2026-11-02" || query.AllTime {
		t.Fatalf("query = %+v", query)
	}
}

func TestGetOverviewForwardsAllTime(t *testing.T) {
	allTime := true
	var query adminanalytics.OverviewQuery
	handler := NewHandler(fakeAnalytics{overviewQuery: &query})
	_, err := handler.GetOverview(context.Background(), &admin.OverviewReq{AllTime: &allTime})
	if err != nil {
		t.Fatal(err)
	}
	if !query.AllTime || query.DateRange.StartDate != "" || query.DateRange.EndDate != "" {
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

func TestListGamesForwardsDateRange(t *testing.T) {
	var query adminanalytics.GamesQuery
	handler := NewHandler(fakeAnalytics{gamesQuery: &query})
	_, err := handler.ListGames(context.Background(), &admin.GamesReq{
		StartDate: optionalString("2026-10-01"),
		EndDate:   optionalString("2026-10-15"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if query.DateRange.StartDate != "2026-10-01" || query.DateRange.EndDate != "2026-10-15" || query.AllTime {
		t.Fatalf("query = %+v", query)
	}
}

func TestListGamesForwardsAllTime(t *testing.T) {
	allTime := true
	var query adminanalytics.GamesQuery
	handler := NewHandler(fakeAnalytics{gamesQuery: &query})
	_, err := handler.ListGames(context.Background(), &admin.GamesReq{AllTime: &allTime})
	if err != nil {
		t.Fatal(err)
	}
	if !query.AllTime || query.DateRange.StartDate != "" || query.DateRange.EndDate != "" {
		t.Fatalf("query = %+v", query)
	}
}

func TestListMatchesForwardsFiltersAndMapsPlayers(t *testing.T) {
	userID := int64(7)
	rank := 1
	var query adminanalytics.MatchQuery
	handler := NewHandler(fakeAnalytics{
		matchesQuery: &query,
		matches: adminanalytics.MatchesPage{
			Page: 2, PageSize: 20, Total: 24,
			Matches: []adminanalytics.Match{{
				ID: 42, RoomID: "SP-1042", MatchID: "match-7", GameType: "splendor",
				StartedAt: time.Unix(100, 0), EndedAt: time.Unix(130, 0), DurationSeconds: 30,
				EndReason: "completed", MaxPlayers: 4,
				Players: []adminanalytics.MatchPlayer{{
					UserID: &userID, PlayerID: "player-7", DisplayName: "北海玩家",
					SeatIndex: 1, FinalRank: &rank, IsWinner: true,
				}},
			}},
		},
	})
	response, err := handler.ListMatches(context.Background(), &admin.ListMatchesReq{
		Page: optionalInt32(2), PageSize: optionalInt32(20), Query: optionalString("北海"),
		GameType: optionalString("splendor"), EndReason: optionalString("completed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if query.Page != 2 || query.PageSize != 20 || query.Query != "北海" ||
		query.GameType != "splendor" || query.EndReason != "completed" {
		t.Fatalf("query = %+v", query)
	}
	data := response.GetData()
	player := data.GetMatches()[0].GetPlayers()[0]
	if data.GetTotal() != 24 || data.GetMatches()[0].GetRoomId() != "SP-1042" ||
		player.GetUserId() != "7" || player.GetFinalRank() != 1 || !player.GetIsWinner() {
		t.Fatalf("response = %+v", response)
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

func optionalInt32(value int32) *int32 {
	return &value
}
