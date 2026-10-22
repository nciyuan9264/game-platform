package platformapi

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

type roleAuthClient struct {
	identity       contracts.UserIdentity
	valid          bool
	listUserID     *string
	listRoles      *[]string
	listUsersError error
}

func (f roleAuthClient) Register(context.Context, string, string) error { return nil }
func (f roleAuthClient) SendCode(context.Context, string) (authclient.SendCodeResponse, error) {
	return authclient.SendCodeResponse{}, nil
}
func (f roleAuthClient) Login(context.Context, authclient.LoginRequest) (authclient.LoginResponse, error) {
	return authclient.LoginResponse{}, nil
}
func (f roleAuthClient) RefreshToken(context.Context, string) (authclient.LoginResponse, error) {
	return authclient.LoginResponse{}, nil
}
func (f roleAuthClient) Logout(context.Context, string, string) error { return nil }
func (f roleAuthClient) VerifyToken(context.Context, authclient.VerifyTokenRequest) (authclient.VerifyTokenResponse, error) {
	return authclient.VerifyTokenResponse{Valid: f.valid, User: f.identity}, nil
}
func (f roleAuthClient) GetProfile(context.Context, string) (contracts.UserIdentity, error) {
	return f.identity, nil
}
func (f roleAuthClient) UpdateUser(context.Context, authclient.UpdateUserRequest) (contracts.UserIdentity, error) {
	return f.identity, nil
}
func (f roleAuthClient) UploadAvatar(context.Context, authclient.UploadAvatarRequest) (string, error) {
	return "", nil
}
func (f roleAuthClient) GetUser(context.Context, string) (contracts.UserIdentity, error) {
	return f.identity, nil
}
func (f roleAuthClient) ListUsers(ctx context.Context, _ authclient.ListUsersRequest) (authclient.ListUsersResponse, error) {
	userID, roles, _ := rpcmeta.UserIdentityFromContext(ctx)
	if f.listUserID != nil {
		*f.listUserID = userID
	}
	if f.listRoles != nil {
		*f.listRoles = roles
	}
	return authclient.ListUsersResponse{}, f.listUsersError
}
func (f roleAuthClient) GetJWKS(context.Context) (string, error) { return `{"keys":[]}`, nil }

type platformCodedError struct {
	code    string
	message string
}

func (e platformCodedError) Error() string      { return e.message }
func (e platformCodedError) GetCode() string    { return e.code }
func (e platformCodedError) GetMessage() string { return e.message }

func TestRequireRole(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		roles      []string
		wantOK     bool
		wantStatus int
	}{
		{name: "missing token", wantStatus: http.StatusUnauthorized},
		{name: "player forbidden", token: "player-token", roles: []string{"player"}, wantStatus: http.StatusForbidden},
		{name: "admin allowed", token: "admin-token", roles: []string{"player", "admin"}, wantOK: true, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &Server{auth: roleAuthClient{
				valid:    tt.token != "",
				identity: contracts.UserIdentity{UserID: "user-1", Roles: tt.roles},
			}}
			req := httptest.NewRequest(http.MethodGet, "/platform/admin/users", nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			rec := httptest.NewRecorder()

			_, ok := server.requireRole(rec, req, "admin")
			if ok != tt.wantOK {
				t.Fatalf("requireRole() ok = %v, want %v", ok, tt.wantOK)
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestAdminUserDirectoryReceivesVerifiedIdentity(t *testing.T) {
	stopAfterListUsers := errors.New("stop after list users")
	var receivedUserID string
	var receivedRoles []string
	server := &Server{auth: roleAuthClient{
		valid:          true,
		identity:       contracts.UserIdentity{UserID: "admin-1", Roles: []string{"player", "admin"}},
		listUserID:     &receivedUserID,
		listRoles:      &receivedRoles,
		listUsersError: stopAfterListUsers,
	}}
	req := httptest.NewRequest(http.MethodGet, "/platform/admin/users", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	if _, ok := server.requireRole(rec, req, "admin"); !ok {
		t.Fatalf("requireRole() status = %d body = %s", rec.Code, rec.Body.String())
	}
	if _, err := server.listAdminUsers(req.Context()); !errors.Is(err, stopAfterListUsers) {
		t.Fatalf("listAdminUsers() error = %v, want %v", err, stopAfterListUsers)
	}
	if receivedUserID != "admin-1" ||
		len(receivedRoles) != 2 ||
		receivedRoles[0] != "player" ||
		receivedRoles[1] != "admin" {
		t.Fatalf("ListUsers identity = %q %v", receivedUserID, receivedRoles)
	}
}

func TestCanCloseRoom(t *testing.T) {
	room := contracts.RoomRecord{RoomID: "room-1", OwnerUserID: "owner"}
	tests := []struct {
		name     string
		identity contracts.UserIdentity
		want     bool
	}{
		{name: "owner", identity: contracts.UserIdentity{UserID: "owner"}, want: true},
		{name: "administrator", identity: contracts.UserIdentity{UserID: "admin", Roles: []string{"admin"}}, want: true},
		{name: "other player", identity: contracts.UserIdentity{UserID: "other", Roles: []string{"player"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canCloseRoom(tt.identity, room); got != tt.want {
				t.Fatalf("canCloseRoom() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAllowedCORSOrigin(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://admin.example.com, http://localhost:18032")

	if !allowedCORSOrigin("http://localhost:8080", "localhost:8080") {
		t.Fatal("same-origin request should be allowed")
	}
	if !allowedCORSOrigin("https://admin.example.com", "api.example.com") {
		t.Fatal("configured origin should be allowed")
	}
	if allowedCORSOrigin("https://evil.example.com", "api.example.com") {
		t.Fatal("unconfigured origin should be rejected")
	}
}

func TestBuildAdminUsersAggregatesSessionsPresenceAndMatches(t *testing.T) {
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	users := buildAdminUsers(
		nil,
		[]contracts.SessionRecord{{
			ConnectionRef: contracts.ConnectionRef{SessionID: "s1", GatewayNode: "gateway-a"},
			UserID:        "alice",
			Name:          "Alice Auth",
			Email:         "alice@example.com",
			Avatar:        "https://example.com/a.png",
			DisplayName:   "Alice",
			ConnectedAt:   now,
			LastSeen:      now.Add(2 * time.Minute),
		}},
		[]contracts.PresenceRecord{
			{
				ConnectionRef: contracts.ConnectionRef{SessionID: "s1", GatewayNode: "gateway-a"},
				RoomID:        "davinci-room-1",
				UserID:        "alice",
				Online:        true,
				JoinedAt:      now.Add(time.Minute),
				LastSeen:      now.Add(3 * time.Minute),
			},
			{
				ConnectionRef: contracts.ConnectionRef{SessionID: "s2", GatewayNode: "gateway-b"},
				RoomID:        "davinci-room-2",
				UserID:        "bob",
				JoinedAt:      now,
				LastSeen:      now.Add(time.Minute),
			},
		},
		[]contracts.RoomRecord{{
			RoomID:      "davinci-room-1",
			OwnerUserID: "alice",
			CreatedAt:   now,
			UpdatedAt:   now.Add(4 * time.Minute),
		}},
		[]contracts.MatchRecord{{
			MatchID:     "davinci-room-1",
			GameType:    contracts.GameDaVinci,
			Status:      contracts.RoomStatusPlaying,
			OwnerUserID: "alice",
			StartedAt:   now,
			UpdatedAt:   now.Add(4 * time.Minute),
			Players: []contracts.MatchPlayerRecord{{
				UserID:      "alice",
				DisplayName: "Alice",
				Name:        "Alice Auth",
				Email:       "alice@example.com",
				Avatar:      "https://example.com/a.png",
				GatewayNode: "gateway-a",
				Online:      true,
				JoinedAt:    now,
				LastSeen:    now.Add(4 * time.Minute),
			}},
		}},
	)

	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	alice := users[0]
	if alice.UserID != "alice" || !alice.Online {
		t.Fatalf("expected alice online first, got %+v", alice)
	}
	if alice.Name != "Alice Auth" || alice.Email != "alice@example.com" || alice.Avatar == "" {
		t.Fatalf("unexpected alice profile: %+v", alice)
	}
	if alice.ActiveSessions != 1 || alice.ActiveRooms != 1 || alice.ActiveMatches != 1 {
		t.Fatalf("unexpected alice counts: %+v", alice)
	}
	if len(alice.OwnedMatchIDs) != 1 || alice.OwnedMatchIDs[0] != "davinci-room-1" {
		t.Fatalf("unexpected alice owned matches: %+v", alice.OwnedMatchIDs)
	}
	if users[1].UserID != "bob" || users[1].Online {
		t.Fatalf("expected bob offline second, got %+v", users[1])
	}
}

func TestBuildAdminUsersIncludesAuthUsersWithoutRuntimeState(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	users := buildAdminUsers(
		[]contracts.AuthUserRecord{{
			UserID:    "7",
			Email:     "user@example.com",
			Name:      "User Example",
			Avatar:    "https://example.com/avatar.png",
			CreatedAt: now.Add(-time.Hour),
			UpdatedAt: now,
		}},
		nil,
		nil,
		nil,
		nil,
	)

	if len(users) != 1 {
		t.Fatalf("expected 1 auth user, got %d", len(users))
	}
	user := users[0]
	if user.UserID != "7" || user.Email != "user@example.com" || user.Name != "User Example" {
		t.Fatalf("unexpected auth user summary: %+v", user)
	}
	if user.DisplayName != "User Example" || user.ActiveSessions != 0 || user.Online {
		t.Fatalf("unexpected auth-only user state: %+v", user)
	}
}

func TestBuildAdminUsersMergesAuthUserWithEmailRuntimeIdentity(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	users := buildAdminUsers(
		[]contracts.AuthUserRecord{{
			UserID:    "1",
			Email:     "player@example.com",
			Name:      "Player One",
			CreatedAt: now.Add(-time.Hour),
			UpdatedAt: now,
		}},
		[]contracts.SessionRecord{{
			ConnectionRef: contracts.ConnectionRef{SessionID: "s1", GatewayNode: "gateway-a"},
			UserID:        "player@example.com",
			DisplayName:   "player@example.com",
			ConnectedAt:   now,
			LastSeen:      now.Add(time.Minute),
		}},
		[]contracts.PresenceRecord{{
			ConnectionRef: contracts.ConnectionRef{SessionID: "s1", GatewayNode: "gateway-a"},
			RoomID:        "davinci-room-1",
			UserID:        "player@example.com",
			Online:        true,
			JoinedAt:      now,
			LastSeen:      now.Add(time.Minute),
		}},
		[]contracts.RoomRecord{{
			RoomID:      "davinci-room-1",
			OwnerUserID: "player@example.com",
			CreatedAt:   now,
			UpdatedAt:   now.Add(time.Minute),
		}},
		[]contracts.MatchRecord{{
			MatchID:     "davinci-room-1",
			Status:      contracts.RoomStatusMatch,
			OwnerUserID: "player@example.com",
			StartedAt:   now,
			UpdatedAt:   now.Add(time.Minute),
		}},
	)

	if len(users) != 1 {
		t.Fatalf("expected merged auth/runtime user, got %d: %+v", len(users), users)
	}
	user := users[0]
	if user.UserID != "1" || user.Email != "player@example.com" {
		t.Fatalf("unexpected merged identity: %+v", user)
	}
	if !user.Online || user.ActiveSessions != 1 || user.ActiveMatches != 1 {
		t.Fatalf("unexpected merged runtime state: %+v", user)
	}
	if len(user.OwnedMatchIDs) != 1 || user.OwnedMatchIDs[0] != "davinci-room-1" {
		t.Fatalf("unexpected owned matches: %+v", user.OwnedMatchIDs)
	}
}

func TestAuthUserNotFoundRPCErrorReturns401(t *testing.T) {
	res := httptest.NewRecorder()
	writeRPCError(res, platformCodedError{code: "user_not_found", message: "user not found"}, "auth_unavailable")
	if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), `"error":"user_not_found"`) {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}
