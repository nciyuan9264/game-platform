package control

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform-server/internal/rpcmeta"
)

func TestRPCActorIdentityComesFromVerifiedToken(t *testing.T) {
	handler := NewRPCHandler(NewServerWithConfig(nil, nil, Config{AdminAuth: playerTokenVerifier{}}))
	ctx := rpcmeta.WithUserToken(context.Background(), "player-token")

	identity, err := handler.callerIdentity(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if identity.UserID != "player" || hasRole(identity.Roles, "admin") {
		t.Fatalf("identity = %+v, want verified non-admin player", identity)
	}
}

func TestRPCInternalActionRequiresAdminUser(t *testing.T) {
	handler := NewRPCHandler(NewServerWithConfig(nil, nil, Config{AdminAuth: playerTokenVerifier{}}))
	playerCtx := rpcmeta.WithUserToken(context.Background(), "player-token")
	if _, err := handler.callerIdentity(playerCtx, true); err == nil {
		t.Fatal("non-admin user was allowed to perform an administrator action")
	}

	adminHandler := NewRPCHandler(NewServerWithConfig(nil, nil, Config{AdminAuth: adminTokenVerifier{}}))
	adminCtx := rpcmeta.WithUserToken(context.Background(), "admin-token")
	identity, err := adminHandler.callerIdentity(adminCtx, true)
	if err != nil {
		t.Fatal(err)
	}
	if identity.UserID != "admin" || !hasRole(identity.Roles, "admin") {
		t.Fatalf("identity = %+v, want verified administrator", identity)
	}
}
