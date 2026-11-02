package adminrpc

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/nciyuan9264/game-platform/internal/adminanalytics"
	admin "github.com/nciyuan9264/game-platform/kitex_gen/pam/game/admin/v1"
)

type analyticsService interface {
	Overview(context.Context) (adminanalytics.Overview, error)
	Users(context.Context, adminanalytics.UserQuery) (adminanalytics.UsersPage, error)
	UserActivity(context.Context, int64) (adminanalytics.UserActivity, error)
	Games(context.Context) (adminanalytics.Games, error)
	Live(context.Context) (adminanalytics.Live, error)
}

type Handler struct {
	analytics analyticsService
}

func NewHandler(analytics analyticsService) *Handler {
	return &Handler{analytics: analytics}
}

func (h *Handler) GetOverview(ctx context.Context, req *admin.EmptyReq) (*admin.OverviewResp, error) {
	if req == nil {
		return nil, adminError("invalid_request", errors.New("request is required"))
	}
	result, err := h.analytics.Overview(ctx)
	if err != nil {
		return nil, mapAnalyticsError(err)
	}
	return &admin.OverviewResp{StatusCode: http.StatusOK, Message: "ok", Data: mapOverview(result)}, nil
}

func (h *Handler) ListUsers(ctx context.Context, req *admin.ListUsersReq) (*admin.UsersResp, error) {
	if req == nil {
		return nil, adminError("invalid_request", errors.New("request is required"))
	}
	result, err := h.analytics.Users(ctx, adminanalytics.UserQuery{
		Page: int(req.GetPage()), PageSize: int(req.GetPageSize()), Query: req.GetQuery(),
	})
	if err != nil {
		return nil, mapAnalyticsError(err)
	}
	return &admin.UsersResp{StatusCode: http.StatusOK, Message: "ok", Data: mapUsers(result)}, nil
}

func (h *Handler) GetUserActivity(ctx context.Context, req *admin.GetUserActivityReq) (*admin.UserActivityResp, error) {
	if req == nil {
		return nil, adminError("invalid_request", errors.New("request is required"))
	}
	userID, err := strconv.ParseInt(strings.TrimSpace(req.GetUserId()), 10, 64)
	if err != nil || userID <= 0 {
		return nil, adminError("invalid_request", errors.New("positive numeric user_id is required"))
	}
	result, err := h.analytics.UserActivity(ctx, userID)
	if err != nil {
		return nil, mapAnalyticsError(err)
	}
	return &admin.UserActivityResp{
		StatusCode: http.StatusOK,
		Message:    "ok",
		Data:       mapUserActivity(result),
	}, nil
}

func (h *Handler) ListGames(ctx context.Context, req *admin.EmptyReq) (*admin.GamesResp, error) {
	if req == nil {
		return nil, adminError("invalid_request", errors.New("request is required"))
	}
	result, err := h.analytics.Games(ctx)
	if err != nil {
		return nil, mapAnalyticsError(err)
	}
	return &admin.GamesResp{StatusCode: http.StatusOK, Message: "ok", Data: mapGames(result)}, nil
}

func (h *Handler) GetLive(ctx context.Context, req *admin.EmptyReq) (*admin.LiveResp, error) {
	if req == nil {
		return nil, adminError("invalid_request", errors.New("request is required"))
	}
	result, err := h.analytics.Live(ctx)
	if err != nil {
		return nil, mapAnalyticsError(err)
	}
	return &admin.LiveResp{StatusCode: http.StatusOK, Message: "ok", Data: mapLive(result)}, nil
}

func mapAnalyticsError(err error) *admin.GameAdminError {
	if errors.Is(err, adminanalytics.ErrUserNotFound) {
		return adminError("not_found", errors.New("user not found"))
	}
	return adminError("analytics_unavailable", err)
}

func adminError(code string, err error) *admin.GameAdminError {
	message := code
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}
	return &admin.GameAdminError{Code: code, Message: message}
}
