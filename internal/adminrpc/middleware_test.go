package adminrpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	admin "github.com/nciyuan9264/game-platform/kitex_gen/pam/game/admin/v1"
)

func TestAdminAuthMiddlewareRequiresAdminRole(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		wantCode string
		wantCall bool
	}{
		{name: "missing identity", ctx: adminRPCContext("GetOverview"), wantCode: "unauthorized"},
		{name: "player forbidden", ctx: adminGatewayContext("GetOverview", "7", []string{"player"}), wantCode: "forbidden"},
		{name: "admin allowed", ctx: adminGatewayContext("GetOverview", "7", []string{"player", "admin"}), wantCall: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			endpoint := AdminAuthMiddleware(func(context.Context, any, any) error {
				called = true
				return nil
			})
			result := &admin.GameAdminServiceGetOverviewResult{}
			if err := endpoint(test.ctx, nil, result); err != nil {
				t.Fatal(err)
			}
			if called != test.wantCall {
				t.Fatalf("called = %v, want %v", called, test.wantCall)
			}
			if test.wantCode == "" {
				if result.GetErr() != nil {
					t.Fatalf("unexpected error = %v", result.GetErr())
				}
				return
			}
			if result.GetErr() == nil || result.GetErr().GetCode() != test.wantCode {
				t.Fatalf("error = %v, want %s", result.GetErr(), test.wantCode)
			}
		})
	}
}

func TestAdminAuthMiddlewareFailsClosedForUnknownMethod(t *testing.T) {
	endpoint := AdminAuthMiddleware(func(context.Context, any, any) error {
		t.Fatal("next endpoint must not be called")
		return nil
	})
	result := &admin.GameAdminServiceGetOverviewResult{}
	if err := endpoint(
		adminGatewayContext("Unknown", "7", []string{"admin"}),
		nil,
		result,
	); err != nil {
		t.Fatal(err)
	}
	if result.GetErr() == nil || result.GetErr().GetCode() != "auth_policy_missing" {
		t.Fatalf("error = %v", result.GetErr())
	}
}

func adminRPCContext(method string) context.Context {
	invocation := rpcinfo.NewInvocation("GameAdminService", method)
	info := rpcinfo.NewRPCInfo(nil, nil, invocation, nil, nil)
	return rpcinfo.NewCtxWithRPCInfo(context.Background(), info)
}

func adminGatewayContext(method, userID string, roles []string) context.Context {
	ctx := adminRPCContext(method)
	ctx = metainfo.WithValue(ctx, "x-pam-user-id", userID)
	encodedRoles, _ := json.Marshal(roles)
	return metainfo.WithValue(ctx, "x-pam-user-roles", string(encodedRoles))
}
