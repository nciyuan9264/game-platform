package rpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	platform "github.com/nciyuan9264/game-platform-client/kitex_gen/pam/game/platform/v1"
	"github.com/nciyuan9264/pam-protocol/httpmeta"
	pammeta "github.com/nciyuan9264/pam-protocol/metadata"
)

type identity struct {
	UserID string
	Roles  []string
}

type identityContextKey struct{}

type platformErrorResult interface {
	SetErr(*platform.GamePlatformError)
}

type platformErrorGetter interface {
	GetErr() *platform.GamePlatformError
}

func UserAuthMiddleware(next endpoint.Endpoint) endpoint.Endpoint {
	return func(ctx context.Context, req, resp any) error {
		method := rpcMethodName(ctx)
		authRequired, ok := methodAuthRequired(method)
		if !ok {
			return setPlatformError(resp, platformError("auth_policy_missing", fmt.Errorf("RPC method %q has no authentication policy", method)))
		}
		if !authRequired {
			return next(ctx, req, resp)
		}
		value, exists, err := pammeta.IdentityFromContext(ctx)
		if err != nil {
			return setPlatformError(resp, platformError("unauthorized", err))
		}
		if !exists || strings.TrimSpace(value.UserID) == "" {
			return setPlatformError(resp, platformError("unauthorized", errors.New("verified user identity is required")))
		}
		if parsed, err := strconv.ParseInt(value.UserID, 10, 64); err != nil || parsed <= 0 {
			return setPlatformError(resp, platformError("unauthorized", errors.New("invalid verified user identity")))
		}
		ctx = context.WithValue(ctx, identityContextKey{}, identity{
			UserID: value.UserID,
			Roles:  append([]string(nil), value.Roles...),
		})
		return next(ctx, req, resp)
	}
}

func HTTPResponseMiddleware(next endpoint.Endpoint) endpoint.Endpoint {
	return func(ctx context.Context, req, resp any) error {
		if err := next(ctx, req, resp); err != nil {
			return err
		}
		result, ok := resp.(platformErrorGetter)
		if !ok || result.GetErr() == nil {
			return nil
		}
		return httpmeta.Send(ctx, httpmeta.ResponseMeta{
			Version:    httpmeta.CurrentVersion,
			StatusCode: errorHTTPStatus(result.GetErr().GetCode()),
		})
	}
}

func methodAuthRequired(method string) (bool, bool) {
	descriptor := platform.GetFileDescriptorForService().GetMethodDescriptor("GamePlatformService", method)
	if descriptor == nil {
		return false, false
	}
	values := descriptor.GetAnnotations()["api.auth_required"]
	if len(values) != 1 {
		return false, false
	}
	required, err := strconv.ParseBool(values[0])
	return required, err == nil
}

func authenticatedIdentity(ctx context.Context) (identity, bool) {
	value, ok := ctx.Value(identityContextKey{}).(identity)
	return value, ok && value.UserID != ""
}

func rpcMethodName(ctx context.Context) string {
	if info := rpcinfo.GetRPCInfo(ctx); info != nil && info.Invocation() != nil {
		return info.Invocation().MethodName()
	}
	return ""
}

func setPlatformError(resp any, err *platform.GamePlatformError) error {
	if result, ok := resp.(platformErrorResult); ok {
		result.SetErr(err)
		return nil
	}
	return err
}

func platformError(code string, err error) *platform.GamePlatformError {
	message := code
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}
	return &platform.GamePlatformError{Code: code, Message: message}
}

func errorHTTPStatus(code string) int {
	switch code {
	case "invalid_request", "invalid_game_type", "invalid_visibility", "invalid_pagination", "invalid_sort":
		return http.StatusBadRequest
	case "unauthorized":
		return http.StatusUnauthorized
	case "forbidden":
		return http.StatusForbidden
	case "not_found":
		return http.StatusNotFound
	case "idempotency_conflict", "match_active", "room_not_open":
		return http.StatusConflict
	case "replay_not_supported":
		return http.StatusUnprocessableEntity
	case "too_many_rooms":
		return http.StatusTooManyRequests
	default:
		return http.StatusServiceUnavailable
	}
}

func hasRole(roles []string, required string) bool {
	for _, role := range roles {
		if strings.EqualFold(strings.TrimSpace(role), required) {
			return true
		}
	}
	return false
}
