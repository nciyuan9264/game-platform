package rpcmeta

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/cloudwego/kitex/pkg/endpoint"
)

const (
	userTokenMetadataKey     = "x-pam-user-token"
	userIDMetadataKey        = "x-pam-user-id"
	userRolesMetadataKey     = "x-pam-user-roles"
	callerServiceMetadataKey = "x-pam-caller-service"
)

type contextKey string

const (
	userTokenContextKey     contextKey = "rpc-user-token"
	userIDContextKey        contextKey = "rpc-user-id"
	userRolesContextKey     contextKey = "rpc-user-roles"
	callerServiceContextKey contextKey = "rpc-caller-service"
)

func ClientMiddleware(next endpoint.Endpoint) endpoint.Endpoint {
	return func(ctx context.Context, req, resp interface{}) error {
		if token, ok := UserTokenFromContext(ctx); ok {
			ctx = metainfo.WithValue(ctx, userTokenMetadataKey, token)
		}
		if userID, roles, ok := UserIdentityFromContext(ctx); ok {
			ctx = metainfo.WithValue(ctx, userIDMetadataKey, userID)
			encodedRoles, _ := json.Marshal(roles)
			ctx = metainfo.WithValue(ctx, userRolesMetadataKey, string(encodedRoles))
		}
		if caller, ok := CallerServiceFromContext(ctx); ok {
			ctx = metainfo.WithValue(ctx, callerServiceMetadataKey, caller)
		}
		return next(ctx, req, resp)
	}
}

func WithUserToken(ctx context.Context, token string) context.Context {
	if strings.TrimSpace(token) == "" {
		return ctx
	}
	return context.WithValue(ctx, userTokenContextKey, token)
}

func UserTokenFromContext(ctx context.Context) (string, bool) {
	if token, ok := ctx.Value(userTokenContextKey).(string); ok && strings.TrimSpace(token) != "" {
		return token, true
	}
	token, ok := metainfo.GetValue(ctx, userTokenMetadataKey)
	return token, ok && strings.TrimSpace(token) != ""
}

func WithUserIdentity(ctx context.Context, userID string, roles []string) context.Context {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ctx
	}
	ctx = context.WithValue(ctx, userIDContextKey, userID)
	return context.WithValue(ctx, userRolesContextKey, append([]string(nil), roles...))
}

func UserIdentityFromContext(ctx context.Context) (string, []string, bool) {
	if userID, ok := ctx.Value(userIDContextKey).(string); ok && strings.TrimSpace(userID) != "" {
		roles, _ := ctx.Value(userRolesContextKey).([]string)
		return userID, append([]string(nil), roles...), true
	}
	userID, ok := metainfo.GetValue(ctx, userIDMetadataKey)
	if !ok || strings.TrimSpace(userID) == "" {
		return "", nil, false
	}
	encodedRoles, _ := metainfo.GetValue(ctx, userRolesMetadataKey)
	var roles []string
	if encodedRoles != "" && json.Unmarshal([]byte(encodedRoles), &roles) != nil {
		return "", nil, false
	}
	return userID, roles, true
}

func WithCallerService(ctx context.Context, caller string) context.Context {
	caller = strings.TrimSpace(caller)
	if caller == "" {
		return ctx
	}
	return context.WithValue(ctx, callerServiceContextKey, caller)
}

func CallerServiceFromContext(ctx context.Context) (string, bool) {
	if caller, ok := ctx.Value(callerServiceContextKey).(string); ok && strings.TrimSpace(caller) != "" {
		return caller, true
	}
	caller, ok := metainfo.GetValue(ctx, callerServiceMetadataKey)
	return caller, ok && strings.TrimSpace(caller) != ""
}
