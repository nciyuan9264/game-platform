package adminrpc

import (
	"context"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/adminanalytics"
	admin "github.com/nciyuan9264/game-platform/kitex_gen/pam/game/admin/v1"
)

type fakeAnalytics struct {
	overview adminanalytics.Overview
}

func (f fakeAnalytics) Overview(context.Context) (adminanalytics.Overview, error) {
	return f.overview, nil
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

func (fakeAnalytics) Live(context.Context) (adminanalytics.Live, error) {
	return adminanalytics.Live{}, nil
}

func TestGetOverviewMapsAnalyticsResponse(t *testing.T) {
	location, err := time.LoadLocation(adminanalytics.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 11, 2, 0, 0, 0, 0, location)
	handler := NewHandler(fakeAnalytics{overview: adminanalytics.Overview{
		Window: adminanalytics.Window{
			Start:       end.AddDate(0, 0, -29),
			End:         end,
			Days:        30,
			GeneratedAt: end.UTC(),
		},
		Metrics: adminanalytics.OverviewMetrics{TotalUsers: 12, DAU: 3, MAU: 6},
		Daily:   []adminanalytics.DailyOverview{{Date: "2026-11-02", NewUsers: 2}},
	}})
	response, err := handler.GetOverview(context.Background(), &admin.EmptyReq{})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStatusCode() != 200 || response.GetData().GetMetrics().GetTotalUsers() != 12 {
		t.Fatalf("response = %+v", response)
	}
	if got := response.GetData().GetDaily()[0].GetNewUsers_(); got != 2 {
		t.Fatalf("new users = %d", got)
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
