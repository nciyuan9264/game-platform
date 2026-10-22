package rpcmeta

import (
	"context"
	"testing"

	"github.com/bytedance/gopkg/cloud/metainfo"
)

func TestClientMiddlewareForwardsUserToken(t *testing.T) {
	next := func(ctx context.Context, _, _ interface{}) error {
		token, ok := metainfo.GetValue(ctx, userTokenMetadataKey)
		if !ok || token != "user-token" {
			t.Fatalf("metadata token = %q, %v", token, ok)
		}
		userID, roles, ok := UserIdentityFromContext(ctx)
		if !ok || userID != "user-7" || len(roles) != 1 || roles[0] != "admin" {
			t.Fatalf("identity = %q, %v, %v", userID, roles, ok)
		}
		caller, ok := CallerServiceFromContext(ctx)
		if !ok || caller != "pam.gateway.http" {
			t.Fatalf("caller = %q, %v", caller, ok)
		}
		return nil
	}
	ctx := WithUserToken(context.Background(), "user-token")
	ctx = WithUserIdentity(ctx, "user-7", []string{"admin"})
	ctx = WithCallerService(ctx, "pam.gateway.http")
	if err := ClientMiddleware(next)(ctx, nil, nil); err != nil {
		t.Fatalf("ClientMiddleware() error = %v", err)
	}
}

func TestUserTokenFromRPCMetadata(t *testing.T) {
	ctx := metainfo.WithValue(context.Background(), userTokenMetadataKey, "metadata-token")
	token, ok := UserTokenFromContext(ctx)
	if !ok || token != "metadata-token" {
		t.Fatalf("UserTokenFromContext() = %q, %v", token, ok)
	}
}
