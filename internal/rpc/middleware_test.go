package rpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	platform "github.com/nciyuan9264/game-platform-client/kitex_gen/pam/game/platform/v1"
)

func TestUserAuthMiddlewareAllowsPublicLeaderboard(t *testing.T) {
	called := false
	endpoint := UserAuthMiddleware(func(context.Context, any, any) error {
		called = true
		return nil
	})
	result := &platform.GamePlatformServiceGetLeaderboardResult{}
	if err := endpoint(rpcContext("GetLeaderboard"), nil, result); err != nil {
		t.Fatal(err)
	}
	if !called || result.GetErr() != nil {
		t.Fatalf("called = %v, error = %v", called, result.GetErr())
	}
}

func TestUserAuthMiddlewareUsesGatewayIdentity(t *testing.T) {
	called := false
	endpoint := UserAuthMiddleware(func(ctx context.Context, _, _ any) error {
		called = true
		value, ok := authenticatedIdentity(ctx)
		if !ok || value.UserID != "7" || !hasRole(value.Roles, "player") {
			t.Fatalf("identity = %+v, %v", value, ok)
		}
		return nil
	})
	result := &platform.GamePlatformServiceListRoomsResult{}
	if err := endpoint(gatewayContext("ListRooms", "7", []string{"player"}), nil, result); err != nil {
		t.Fatal(err)
	}
	if !called || result.GetErr() != nil {
		t.Fatalf("called = %v, error = %v", called, result.GetErr())
	}
}

func TestUserAuthMiddlewareRejectsMissingOrInvalidIdentity(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{name: "missing", ctx: rpcContext("ListRooms")},
		{name: "invalid", ctx: gatewayContext("ListRooms", "not-a-user", []string{"player"})},
		{name: "zero", ctx: gatewayContext("ListRooms", "0", []string{"player"})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			endpoint := UserAuthMiddleware(func(context.Context, any, any) error {
				called = true
				return nil
			})
			result := &platform.GamePlatformServiceListRoomsResult{}
			if err := endpoint(test.ctx, nil, result); err != nil {
				t.Fatal(err)
			}
			if called || result.GetErr() == nil || result.GetErr().GetCode() != "unauthorized" {
				t.Fatalf("called = %v, error = %v", called, result.GetErr())
			}
		})
	}
}

func TestUserAuthMiddlewareFailsClosedForUnknownMethod(t *testing.T) {
	endpoint := UserAuthMiddleware(func(context.Context, any, any) error {
		t.Fatal("next endpoint must not be called")
		return nil
	})
	result := &platform.GamePlatformServiceListRoomsResult{}
	if err := endpoint(rpcContext("Unknown"), nil, result); err != nil {
		t.Fatal(err)
	}
	if result.GetErr() == nil || result.GetErr().GetCode() != "auth_policy_missing" {
		t.Fatalf("error = %v", result.GetErr())
	}
}

func rpcContext(method string) context.Context {
	invocation := rpcinfo.NewInvocation("GamePlatformService", method)
	info := rpcinfo.NewRPCInfo(nil, nil, invocation, nil, nil)
	return rpcinfo.NewCtxWithRPCInfo(context.Background(), info)
}

func gatewayContext(method, userID string, roles []string) context.Context {
	ctx := rpcContext(method)
	ctx = metainfo.WithValue(ctx, "x-pam-user-id", userID)
	encodedRoles, _ := json.Marshal(roles)
	return metainfo.WithValue(ctx, "x-pam-user-roles", string(encodedRoles))
}
