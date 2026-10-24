package authapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/authclient"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/rpcmeta"
)

type fakeAuthClient struct {
	login            authclient.LoginResponse
	err              error
	verifyErr        error
	profileErr       error
	updateErr        error
	verifyCalls      int
	profileCalls     int
	updateCalls      int
	profileUserID    string
	profileUserRoles []string
	updateUserID     string
	updateUserRoles  []string
}

type fakeRateLimiter struct {
	allowed bool
	calls   int
}

func (f *fakeRateLimiter) AllowRateLimit(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	f.calls++
	return f.allowed, time.Minute, nil
}

type codedError struct {
	code    string
	message string
}

func (e codedError) Error() string      { return e.message }
func (e codedError) GetCode() string    { return e.code }
func (e codedError) GetMessage() string { return e.message }

func (f *fakeAuthClient) Register(context.Context, string, string) error { return f.err }
func (f *fakeAuthClient) SendCode(context.Context, string) (authclient.SendCodeResponse, error) {
	return authclient.SendCodeResponse{DevCode: "123456"}, f.err
}
func (f *fakeAuthClient) Login(context.Context, authclient.LoginRequest) (authclient.LoginResponse, error) {
	return f.login, f.err
}
func (f *fakeAuthClient) RefreshToken(context.Context, string) (authclient.LoginResponse, error) {
	return f.login, f.err
}
func (f *fakeAuthClient) Logout(context.Context, string, string) error { return f.err }
func (f *fakeAuthClient) VerifyToken(context.Context, authclient.VerifyTokenRequest) (authclient.VerifyTokenResponse, error) {
	f.verifyCalls++
	return authclient.VerifyTokenResponse{Valid: true, User: contracts.UserIdentity{
		UserID: f.login.User.UserID,
		Roles:  append([]string(nil), f.login.User.Roles...),
	}}, f.verifyErr
}
func (f *fakeAuthClient) GetProfile(ctx context.Context, _ string) (contracts.UserIdentity, error) {
	f.profileCalls++
	f.profileUserID, f.profileUserRoles, _ = rpcmeta.UserIdentityFromContext(ctx)
	if f.profileErr != nil {
		return contracts.UserIdentity{}, f.profileErr
	}
	return f.login.User, f.err
}
func (f *fakeAuthClient) UpdateUser(ctx context.Context, _ authclient.UpdateUserRequest) (contracts.UserIdentity, error) {
	f.updateCalls++
	f.updateUserID, f.updateUserRoles, _ = rpcmeta.UserIdentityFromContext(ctx)
	if f.updateErr != nil {
		return contracts.UserIdentity{}, f.updateErr
	}
	return f.login.User, f.err
}
func (f *fakeAuthClient) UploadAvatar(context.Context, authclient.UploadAvatarRequest) (string, error) {
	return "https://example.com/avatar.png", f.err
}
func (f *fakeAuthClient) GetUser(context.Context, string) (contracts.UserIdentity, error) {
	return f.login.User, f.err
}
func (f *fakeAuthClient) BatchGetPublicProfiles(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, f.err
}
func (f *fakeAuthClient) ListUsers(context.Context, authclient.ListUsersRequest) (authclient.ListUsersResponse, error) {
	return authclient.ListUsersResponse{}, f.err
}
func (f *fakeAuthClient) GetJWKS(context.Context) (string, error) {
	return `{"keys":[]}`, f.err
}

func TestLoginByCodeSetsSecureSessionCookies(t *testing.T) {
	client := &fakeAuthClient{login: authclient.LoginResponse{
		User: contracts.UserIdentity{UserID: "7", Name: "Alice", DisplayName: "Alice"},
		Tokens: authclient.TokenPair{
			AccessToken:          "access",
			RefreshToken:         "refresh",
			AccessExpiresAtUnix:  4102444800,
			RefreshExpiresAtUnix: 4102448400,
		},
	}}
	server := New(client, Config{})
	mux := http.NewServeMux()
	server.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/platform/auth/verify-code", strings.NewReader(`{"email":"alice@example.com","code":"123456"}`))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	cookies := res.Result().Cookies()
	if len(cookies) < 3 {
		t.Fatalf("cookies = %+v", cookies)
	}
	foundAccess := false
	for _, cookie := range cookies {
		if cookie.Name == accessCookieName {
			foundAccess = cookie.Value == "access" && cookie.HttpOnly
		}
	}
	if !foundAccess {
		t.Fatalf("access cookie missing or invalid: %+v", cookies)
	}
	if strings.Contains(res.Body.String(), `"access_token"`) || strings.Contains(res.Body.String(), `"refresh_token"`) {
		t.Fatalf("login response exposed tokens: %s", res.Body.String())
	}
}

func TestSendCodeHidesDevelopmentCodeByDefault(t *testing.T) {
	server := New(&fakeAuthClient{}, Config{})
	mux := http.NewServeMux()
	server.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/platform/auth/send-code", strings.NewReader(`{"email":"alice@example.com"}`))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "123456") || strings.Contains(res.Body.String(), "dev_code") {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestSendCodeCanExposeDevelopmentCodeExplicitly(t *testing.T) {
	server := New(&fakeAuthClient{}, Config{ExposeDevCode: true})
	mux := http.NewServeMux()
	server.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/platform/auth/send-code", strings.NewReader(`{"email":"alice@example.com"}`))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "123456") {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestVerifyTokenReturnsOnlyPrincipal(t *testing.T) {
	client := &fakeAuthClient{login: authclient.LoginResponse{User: contracts.UserIdentity{
		UserID:      "7",
		Name:        "Alice",
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Avatar:      "https://example.com/avatar.png",
		Roles:       []string{"player"},
	}}}
	server := New(client, Config{})
	mux := http.NewServeMux()
	server.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/platform/auth/verify-token", nil)
	req.Header.Set("Authorization", "Bearer access-token")
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	body := res.Body.String()
	if res.Code != http.StatusOK || !strings.Contains(body, `"user_id":"7"`) ||
		!strings.Contains(body, `"roles":["player"]`) {
		t.Fatalf("status = %d body = %s", res.Code, body)
	}
	for _, field := range []string{`"email"`, `"name"`, `"displayName"`, `"avatar"`} {
		if strings.Contains(body, field) {
			t.Fatalf("verify response contains profile field %s: %s", field, body)
		}
	}
	if client.verifyCalls != 1 || client.profileCalls != 0 {
		t.Fatalf("verify calls = %d profile calls = %d", client.verifyCalls, client.profileCalls)
	}
}

func TestAuthUnavailableReturns503(t *testing.T) {
	server := New(&fakeAuthClient{err: errors.New("dial failed")}, Config{})
	mux := http.NewServeMux()
	server.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/platform/auth/send-code", strings.NewReader(`{"email":"alice@example.com"}`))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestSendCodeRateLimitReturns429(t *testing.T) {
	limiter := &fakeRateLimiter{}
	server := New(&fakeAuthClient{}, Config{RateLimiter: limiter})
	mux := http.NewServeMux()
	server.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/platform/auth/send-code", strings.NewReader(`{"email":"alice@example.com"}`))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if res.Header().Get("Retry-After") == "" || limiter.calls != 1 {
		t.Fatalf("retry-after = %q calls = %d", res.Header().Get("Retry-After"), limiter.calls)
	}
}

func TestProfileUserNotFoundReturns401(t *testing.T) {
	client := &fakeAuthClient{
		login:      authclient.LoginResponse{User: contracts.UserIdentity{UserID: "7", Roles: []string{"player"}}},
		profileErr: codedError{code: "user_not_found", message: "user not found"},
	}
	server := New(client, Config{})
	mux := http.NewServeMux()
	server.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/platform/auth/profile", nil)
	req.AddCookie(&http.Cookie{Name: accessCookieName, Value: "stale-access-token"})
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), `"error":"user_not_found"`) {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if client.verifyCalls != 1 || client.profileCalls != 1 ||
		client.profileUserID != "7" || len(client.profileUserRoles) != 1 ||
		client.profileUserRoles[0] != "player" {
		t.Fatalf(
			"verification/profile calls = %d/%d identity = %q %v",
			client.verifyCalls,
			client.profileCalls,
			client.profileUserID,
			client.profileUserRoles,
		)
	}
}

func TestUpdateUserRejectsUnverifiedEmailChange(t *testing.T) {
	client := &fakeAuthClient{
		login: authclient.LoginResponse{User: contracts.UserIdentity{UserID: "7", Roles: []string{"player"}}},
		updateErr: codedError{
			code:    "email_change_requires_verification",
			message: "email change requires verification",
		},
	}
	server := New(client, Config{})
	mux := http.NewServeMux()
	server.Register(mux)
	req := httptest.NewRequest(
		http.MethodPost,
		"/platform/auth/update",
		strings.NewReader(`{"email":"admin@example.com"}`),
	)
	req.AddCookie(&http.Cookie{Name: accessCookieName, Value: "access-token"})
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusConflict ||
		!strings.Contains(res.Body.String(), `"error":"email_change_requires_verification"`) {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if client.verifyCalls != 1 || client.updateCalls != 1 ||
		client.updateUserID != "7" || len(client.updateUserRoles) != 1 ||
		client.updateUserRoles[0] != "player" {
		t.Fatalf(
			"verification/update calls = %d/%d identity = %q %v",
			client.verifyCalls,
			client.updateCalls,
			client.updateUserID,
			client.updateUserRoles,
		)
	}
}
