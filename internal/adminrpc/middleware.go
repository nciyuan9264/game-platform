package adminrpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	admin "github.com/nciyuan9264/game-platform/kitex_gen/pam/game/admin/v1"
	"github.com/nciyuan9264/pam-protocol/httpmeta"
	pammeta "github.com/nciyuan9264/pam-protocol/metadata"
)

type adminErrorResult interface {
	SetErr(*admin.GameAdminError)
}

type adminErrorGetter interface {
	GetErr() *admin.GameAdminError
}

var adminMethods = map[string]struct{}{
	"GetOverview":     {},
	"ListUsers":       {},
	"GetUserActivity": {},
	"ListGames":       {},
	"GetLive":         {},
}

func AdminAuthMiddleware(next endpoint.Endpoint) endpoint.Endpoint {
	return func(ctx context.Context, req, resp any) error {
		method := rpcMethodName(ctx)
		if _, ok := adminMethods[method]; !ok {
			return setAdminError(resp, adminError(
				"auth_policy_missing",
				fmt.Errorf("RPC method %q has no authentication policy", method),
			))
		}
		identity, exists, err := pammeta.IdentityFromContext(ctx)
		if err != nil {
			return setAdminError(resp, adminError("unauthorized", err))
		}
		if !exists || strings.TrimSpace(identity.UserID) == "" {
			return setAdminError(resp, adminError("unauthorized", errors.New("verified user identity is required")))
		}
		if parsed, err := strconv.ParseInt(identity.UserID, 10, 64); err != nil || parsed <= 0 {
			return setAdminError(resp, adminError("unauthorized", errors.New("invalid verified user identity")))
		}
		if !hasRole(identity.Roles, "admin") {
			return setAdminError(resp, adminError("forbidden", errors.New("administrator role is required")))
		}
		return next(ctx, req, resp)
	}
}

func HTTPResponseMiddleware(next endpoint.Endpoint) endpoint.Endpoint {
	return func(ctx context.Context, req, resp any) error {
		if err := next(ctx, req, resp); err != nil {
			return err
		}
		result, ok := resp.(adminErrorGetter)
		if !ok || result.GetErr() == nil {
			return nil
		}
		return httpmeta.Send(ctx, httpmeta.ResponseMeta{
			Version: httpmeta.CurrentVersion, StatusCode: errorHTTPStatus(result.GetErr().GetCode()),
		})
	}
}

func rpcMethodName(ctx context.Context) string {
	if info := rpcinfo.GetRPCInfo(ctx); info != nil && info.Invocation() != nil {
		return info.Invocation().MethodName()
	}
	return ""
}

func setAdminError(resp any, err *admin.GameAdminError) error {
	if result, ok := resp.(adminErrorResult); ok {
		result.SetErr(err)
		return nil
	}
	return err
}

func hasRole(roles []string, required string) bool {
	for _, role := range roles {
		if strings.EqualFold(strings.TrimSpace(role), required) {
			return true
		}
	}
	return false
}

func errorHTTPStatus(code string) int {
	switch code {
	case "invalid_request":
		return http.StatusBadRequest
	case "unauthorized":
		return http.StatusUnauthorized
	case "forbidden":
		return http.StatusForbidden
	case "not_found":
		return http.StatusNotFound
	default:
		return http.StatusServiceUnavailable
	}
}
