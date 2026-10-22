package authclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/rpcmeta"
)

var ErrUnauthenticated = errors.New("unauthenticated")

type VerifyTokenRequest struct {
	AccessToken      string
	RequiredAudience string
}

type VerifyTokenResponse struct {
	Valid         bool
	User          contracts.UserIdentity
	TokenID       string
	ExpiresAtUnix int64
	Reason        string
}

type LoginRequest struct {
	Email    string
	Password string
	Code     string
}

type TokenPair struct {
	AccessToken          string
	RefreshToken         string
	AccessExpiresAtUnix  int64
	RefreshExpiresAtUnix int64
}

type LoginResponse struct {
	User   contracts.UserIdentity
	Tokens TokenPair
}

type SendCodeResponse struct {
	DevCode string
}

type UserRecord struct {
	User      contracts.UserIdentity
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ListUsersRequest struct {
	PageSize  int
	PageToken string
}

type ListUsersResponse struct {
	Users         []UserRecord
	NextPageToken string
}

type UpdateUserRequest struct {
	AccessToken string
	Email       string
	Name        string
	Avatar      string
}

type UploadAvatarRequest struct {
	AccessToken string
	Content     []byte
	FileName    string
	ContentType string
}

type TokenVerifier interface {
	VerifyToken(ctx context.Context, req VerifyTokenRequest) (VerifyTokenResponse, error)
}

type LoginService interface {
	Register(ctx context.Context, email, password string) error
	SendCode(ctx context.Context, email string) (SendCodeResponse, error)
	Login(ctx context.Context, req LoginRequest) (LoginResponse, error)
	RefreshToken(ctx context.Context, refreshToken string) (LoginResponse, error)
	Logout(ctx context.Context, accessToken, refreshToken string) error
}

type ProfileService interface {
	GetProfile(ctx context.Context, accessToken string) (contracts.UserIdentity, error)
	UpdateUser(ctx context.Context, req UpdateUserRequest) (contracts.UserIdentity, error)
	UploadAvatar(ctx context.Context, req UploadAvatarRequest) (string, error)
}

type UserDirectory interface {
	GetUser(ctx context.Context, userID string) (contracts.UserIdentity, error)
	ListUsers(ctx context.Context, req ListUsersRequest) (ListUsersResponse, error)
}

type JWKSService interface {
	GetJWKS(ctx context.Context) (string, error)
}

type Client interface {
	TokenVerifier
	LoginService
	ProfileService
	UserDirectory
	JWKSService
}

type Verifier struct {
	tokens   TokenVerifier
	profiles ProfileService
}

func NewVerifier(tokens TokenVerifier, profiles ProfileService) *Verifier {
	return &Verifier{tokens: tokens, profiles: profiles}
}

func (v *Verifier) VerifyPrincipal(ctx context.Context, token string) (contracts.UserIdentity, error) {
	if v == nil || v.tokens == nil {
		return contracts.UserIdentity{}, ErrUnauthenticated
	}
	resp, err := v.tokens.VerifyToken(ctx, VerifyTokenRequest{AccessToken: token, RequiredAudience: "game-platform"})
	if err != nil {
		return contracts.UserIdentity{}, err
	}
	if !resp.Valid {
		if resp.Reason != "" {
			return contracts.UserIdentity{}, fmt.Errorf("%w: %s", ErrUnauthenticated, resp.Reason)
		}
		return contracts.UserIdentity{}, ErrUnauthenticated
	}
	return resp.User, nil
}

func (v *Verifier) Verify(ctx context.Context, token string) (contracts.UserIdentity, error) {
	principal, err := v.VerifyPrincipal(ctx, token)
	if err != nil {
		return contracts.UserIdentity{}, err
	}
	if v.profiles == nil {
		return contracts.UserIdentity{}, ErrUnauthenticated
	}
	ctx = rpcmeta.WithUserIdentity(ctx, principal.UserID, principal.Roles)
	return v.profiles.GetProfile(ctx, token)
}
