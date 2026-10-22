package authclient

import (
	"context"
	"errors"
	"testing"

	"github.com/nciyuan9264/game-platform-server/internal/contracts"
	"github.com/nciyuan9264/game-platform-server/internal/rpcmeta"
)

type fakeClient struct {
	resp             VerifyTokenResponse
	err              error
	req              VerifyTokenRequest
	profile          contracts.UserIdentity
	profileErr       error
	profileCalls     int
	profileToken     string
	profileUserID    string
	profileUserRoles []string
}

func (c *fakeClient) VerifyToken(ctx context.Context, req VerifyTokenRequest) (VerifyTokenResponse, error) {
	c.req = req
	return c.resp, c.err
}

func (c *fakeClient) GetProfile(ctx context.Context, token string) (contracts.UserIdentity, error) {
	c.profileCalls++
	c.profileToken = token
	c.profileUserID, c.profileUserRoles, _ = rpcmeta.UserIdentityFromContext(ctx)
	return c.profile, c.profileErr
}

func (c *fakeClient) UpdateUser(context.Context, UpdateUserRequest) (contracts.UserIdentity, error) {
	return contracts.UserIdentity{}, errors.New("not implemented")
}

func (c *fakeClient) UploadAvatar(context.Context, UploadAvatarRequest) (string, error) {
	return "", errors.New("not implemented")
}

func TestVerifierSeparatesPrincipalAndProfileLookup(t *testing.T) {
	client := &fakeClient{resp: VerifyTokenResponse{
		Valid: true,
		User:  contracts.UserIdentity{UserID: "42", Roles: []string{"player"}},
	}, profile: contracts.UserIdentity{
		UserID:      "42",
		DisplayName: "alice",
		Email:       "alice@example.com",
		Roles:       []string{"player"},
	}}
	verifier := NewVerifier(client, client)

	principal, err := verifier.VerifyPrincipal(context.Background(), "access-token")
	if err != nil {
		t.Fatalf("VerifyPrincipal() error = %v", err)
	}
	if client.req.AccessToken != "access-token" || client.req.RequiredAudience != "game-platform" {
		t.Fatalf("unexpected verify request: %+v", client.req)
	}
	if principal.UserID != "42" || len(principal.Roles) != 1 || principal.Email != "" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	if client.profileCalls != 0 {
		t.Fatalf("profile calls after VerifyPrincipal() = %d, want 0", client.profileCalls)
	}

	identity, err := verifier.Verify(context.Background(), "access-token")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if identity.UserID != "42" || identity.DisplayName != "alice" || identity.Email != "alice@example.com" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	if client.profileCalls != 1 || client.profileToken != "access-token" ||
		client.profileUserID != "42" || len(client.profileUserRoles) != 1 ||
		client.profileUserRoles[0] != "player" {
		t.Fatalf(
			"profile call = {calls:%d token:%q user:%q roles:%v}",
			client.profileCalls,
			client.profileToken,
			client.profileUserID,
			client.profileUserRoles,
		)
	}
}

func TestVerifierRejectsInvalidToken(t *testing.T) {
	client := &fakeClient{resp: VerifyTokenResponse{Valid: false, Reason: "token revoked"}}
	verifier := NewVerifier(client, client)

	_, err := verifier.VerifyPrincipal(context.Background(), "bad-token")
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("VerifyPrincipal() error = %v, want unauthenticated", err)
	}
	if client.profileCalls != 0 {
		t.Fatalf("profile calls = %d, want 0", client.profileCalls)
	}
}
