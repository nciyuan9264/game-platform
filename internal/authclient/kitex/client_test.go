package kitex

import (
	"context"
	"errors"
	"testing"

	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/cloudwego/kitex/client/callopt"
	"github.com/nciyuan9264/game-platform/internal/authclient"
	"github.com/nciyuan9264/game-platform/internal/rpcmeta"
	auth "github.com/nciyuan9264/game-platform/kitex_gen/pam/auth/v1"
	authservice "github.com/nciyuan9264/game-platform/kitex_gen/pam/auth/v1/authservice"
	"github.com/nciyuan9264/pam-protocol/httpmeta"
)

type fakeRPC struct {
	authservice.Client
	verifyReq     *auth.VerifyTokenReq
	verify        *auth.VerifyTokenResp
	loginReq      any
	login         *auth.UserResp
	loginMeta     *httpmeta.ResponseMeta
	profileReq    *auth.GetProfileReq
	profile       *auth.UserResp
	profileUserID string
	profileRoles  []string
	list          *auth.ListUsersResp
	err           error
}

func (f *fakeRPC) publishLoginMeta(ctx context.Context) error {
	if f.loginMeta == nil {
		return nil
	}
	payload, err := httpmeta.Encode(*f.loginMeta)
	if err != nil {
		return err
	}
	metainfo.SetBackwardValue(ctx, httpmeta.MetadataKey, payload)
	return nil
}

func (f *fakeRPC) Register(context.Context, *auth.RegisterReq, ...callopt.Option) (*auth.BaseResp, error) {
	return &auth.BaseResp{StatusCode: 200, Message: "ok"}, f.err
}
func (f *fakeRPC) SendCode(context.Context, *auth.SendCodeReq, ...callopt.Option) (*auth.SendCodeResp, error) {
	return &auth.SendCodeResp{StatusCode: 200, Message: "ok"}, f.err
}
func (f *fakeRPC) LoginByPassword(ctx context.Context, req *auth.LoginByPasswordReq, _ ...callopt.Option) (*auth.UserResp, error) {
	f.loginReq = req
	if err := f.publishLoginMeta(ctx); err != nil {
		return nil, err
	}
	return f.login, f.err
}
func (f *fakeRPC) LoginByCode(ctx context.Context, req *auth.LoginByCodeReq, _ ...callopt.Option) (*auth.UserResp, error) {
	f.loginReq = req
	if err := f.publishLoginMeta(ctx); err != nil {
		return nil, err
	}
	return f.login, f.err
}
func (f *fakeRPC) RefreshToken(ctx context.Context, _ *auth.RefreshTokenReq, _ ...callopt.Option) (*auth.UserResp, error) {
	if err := f.publishLoginMeta(ctx); err != nil {
		return nil, err
	}
	return f.login, f.err
}
func (f *fakeRPC) VerifyToken(ctx context.Context, req *auth.VerifyTokenReq, _ ...callopt.Option) (*auth.VerifyTokenResp, error) {
	f.verifyReq = req
	return f.verify, f.err
}
func (f *fakeRPC) Logout(context.Context, *auth.LogoutReq, ...callopt.Option) (*auth.BaseResp, error) {
	return &auth.BaseResp{StatusCode: 200, Message: "ok"}, f.err
}
func (f *fakeRPC) GetProfile(ctx context.Context, req *auth.GetProfileReq, _ ...callopt.Option) (*auth.UserResp, error) {
	f.profileReq = req
	f.profileUserID, f.profileRoles, _ = rpcmeta.UserIdentityFromContext(ctx)
	return f.profile, f.err
}
func (f *fakeRPC) UpdateUser(context.Context, *auth.UpdateUserReq, ...callopt.Option) (*auth.UserResp, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeRPC) UploadAvatar(context.Context, *auth.UploadAvatarReq, ...callopt.Option) (*auth.UploadAvatarResp, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeRPC) GetUser(context.Context, *auth.GetUserReq, ...callopt.Option) (*auth.UserResp, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeRPC) ListUsers(context.Context, *auth.ListUsersReq, ...callopt.Option) (*auth.ListUsersResp, error) {
	return f.list, f.err
}
func (f *fakeRPC) GetJWKS(context.Context, *auth.JWKSReq, ...callopt.Option) (*auth.JWKSResp, error) {
	return nil, errors.New("not implemented")
}

func TestVerifyTokenMapsRPCIdentity(t *testing.T) {
	expiresAt := int64(123)
	rpc := &fakeRPC{verify: &auth.VerifyTokenResp{
		StatusCode: 200,
		Message:    "ok",
		Data: &auth.VerifyTokenData{
			Valid: true,
			User: &auth.UserIdentity{
				UserId: "42",
				Roles:  []string{"player"},
			},
			ExpiresAtUnix: &expiresAt,
		},
	}}
	client := NewWithRPC(rpc)

	resp, err := client.VerifyToken(context.Background(), authclient.VerifyTokenRequest{AccessToken: "token", RequiredAudience: "game-platform"})
	if err != nil {
		t.Fatalf("VerifyToken() error = %v", err)
	}
	if rpc.verifyReq.GetAccessToken() != "token" || rpc.verifyReq.GetRequiredAudience() != "game-platform" {
		t.Fatalf("unexpected rpc request: %+v", rpc.verifyReq)
	}
	if !resp.Valid || resp.ExpiresAtUnix != expiresAt || resp.User.UserID != "42" ||
		len(resp.User.Roles) != 1 || resp.User.Email != "" || resp.User.Name != "" ||
		resp.User.DisplayName != "" || resp.User.Avatar != "" {
		t.Fatalf("unexpected verify response: %+v", resp)
	}
}

func TestGetProfilePreservesVerifiedIdentityMetadata(t *testing.T) {
	email := "alice@example.com"
	rpc := &fakeRPC{profile: &auth.UserResp{
		StatusCode: 200,
		Data:       &auth.UserIdentity{UserId: "42", Email: &email, Roles: []string{"player"}},
	}}
	client := NewWithRPC(rpc)
	ctx := rpcmeta.WithUserIdentity(context.Background(), "42", []string{"player"})

	user, err := client.GetProfile(ctx, "access-token")
	if err != nil {
		t.Fatalf("GetProfile() error = %v", err)
	}
	if user.UserID != "42" || user.Email != email {
		t.Fatalf("profile = %+v", user)
	}
	if rpc.profileReq.GetAccessToken() != "access-token" ||
		rpc.profileUserID != "42" || len(rpc.profileRoles) != 1 ||
		rpc.profileRoles[0] != "player" {
		t.Fatalf(
			"profile request = %+v identity = %q %v",
			rpc.profileReq,
			rpc.profileUserID,
			rpc.profileRoles,
		)
	}
}

func TestLoginUsesCodeWhenProvided(t *testing.T) {
	rpc := &fakeRPC{login: loginResponse("42")}
	client := NewWithRPC(rpc)

	resp, err := client.Login(context.Background(), authclient.LoginRequest{Email: "alice@example.com", Code: "123456"})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if _, ok := rpc.loginReq.(*auth.LoginByCodeReq); !ok {
		t.Fatalf("login request type = %T, want LoginByCodeReq", rpc.loginReq)
	}
	if resp.User.UserID != "42" {
		t.Fatalf("unexpected login response: %+v", resp)
	}
}

func TestLoginReadsSessionTokensFromResponseMetadata(t *testing.T) {
	rpc := &fakeRPC{
		login: loginResponse("42"),
		loginMeta: &httpmeta.ResponseMeta{
			Version: httpmeta.CurrentVersion,
			Cookies: []httpmeta.CookieMutation{
				{Action: httpmeta.CookieSet, Name: "access_token", Value: "access-secret", ExpiresAtUnix: 4102444800},
				{Action: httpmeta.CookieSet, Name: "refresh_token", Value: "refresh-secret", ExpiresAtUnix: 4102448400},
			},
		},
	}
	client := NewWithRPC(rpc)

	resp, err := client.Login(
		context.Background(),
		authclient.LoginRequest{Email: "alice.com", Code: "123456"},
	)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if resp.Tokens.AccessToken != "access-secret" ||
		resp.Tokens.RefreshToken != "refresh-secret" ||
		resp.Tokens.AccessExpiresAtUnix != 4102444800 ||
		resp.Tokens.RefreshExpiresAtUnix != 4102448400 {
		t.Fatalf("unexpected login tokens: %+v", resp.Tokens)
	}
}

func TestListUsersMapsProfilesAndCursor(t *testing.T) {
	email := "alice@example.com"
	next := "42"
	rpc := &fakeRPC{list: &auth.ListUsersResp{
		StatusCode: 200,
		Message:    "ok",
		Data: &auth.ListUsersData{
			Users: []*auth.AuthUserRecord{{
				Identity:      &auth.UserIdentity{UserId: "7", Email: &email},
				CreatedAtUnix: 10,
				UpdatedAtUnix: 20,
			}},
			NextPageToken: &next,
		},
	}}
	client := NewWithRPC(rpc)
	resp, err := client.ListUsers(context.Background(), authclient.ListUsersRequest{PageSize: 200})
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(resp.Users) != 1 || resp.Users[0].User.UserID != "7" || resp.Users[0].User.Email != email || resp.NextPageToken != next {
		t.Fatalf("unexpected list response: %+v", resp)
	}
}

func loginResponse(userID string) *auth.UserResp {
	return &auth.UserResp{
		StatusCode: 200,
		Message:    "ok",
		Data:       &auth.UserIdentity{UserId: userID},
	}
}
