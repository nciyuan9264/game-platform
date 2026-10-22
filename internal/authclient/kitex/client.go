package kitex

import (
	"context"
	"time"

	"github.com/bytedance/gopkg/cloud/metainfo"
	kitexclient "github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/transmeta"
	kitextransport "github.com/cloudwego/kitex/transport"
	"github.com/nciyuan9264/game-platform/internal/authclient"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/rpcmeta"
	"github.com/nciyuan9264/game-platform/internal/servicediscovery"
	auth "github.com/nciyuan9264/game-platform/kitex_gen/pam/auth/v1"
	authservice "github.com/nciyuan9264/game-platform/kitex_gen/pam/auth/v1/authservice"
	"github.com/nciyuan9264/pam-protocol/httpmeta"
)

type Client struct {
	rpc authservice.Client
}

func New(addr string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	options := []kitexclient.Option{
		kitexclient.WithRPCTimeout(timeout),
		kitexclient.WithConnectTimeout(timeout),
		kitexclient.WithTransportProtocol(kitextransport.TTHeader),
		kitexclient.WithMetaHandler(transmeta.ClientTTHeaderHandler),
		kitexclient.WithMiddleware(rpcmeta.ClientMiddleware),
	}
	psm, err := servicediscovery.ServiceName("AUTH_RPC_PSM", "pam.auth.rpc")
	if err != nil {
		return nil, err
	}
	resolver, err := servicediscovery.Resolver()
	if err != nil {
		return nil, err
	}
	if resolver != nil {
		options = append(options, kitexclient.WithResolver(resolver))
	} else {
		options = append(options, kitexclient.WithHostPorts(addr))
	}
	rpc, err := authservice.NewClient(psm, options...)
	if err != nil {
		return nil, err
	}
	return &Client{rpc: rpc}, nil
}

func NewWithRPC(rpc authservice.Client) *Client {
	return &Client{rpc: rpc}
}

func (c *Client) Register(ctx context.Context, email, password string) error {
	_, err := c.rpc.Register(ctx, &auth.RegisterReq{Email: email, Password: password})
	return err
}

func (c *Client) SendCode(ctx context.Context, email string) (authclient.SendCodeResponse, error) {
	resp, err := c.rpc.SendCode(ctx, &auth.SendCodeReq{Email: email})
	if err != nil {
		return authclient.SendCodeResponse{}, err
	}
	out := authclient.SendCodeResponse{}
	if resp != nil && resp.IsSetData() {
		out.DevCode = resp.GetData().GetDevCode()
	}
	return out, nil
}

func (c *Client) Login(ctx context.Context, req authclient.LoginRequest) (authclient.LoginResponse, error) {
	callCtx := metainfo.WithBackwardValues(ctx)
	var (
		resp *auth.UserResp
		err  error
	)
	if req.Code != "" {
		resp, err = c.rpc.LoginByCode(callCtx, &auth.LoginByCodeReq{Email: req.Email, Code: req.Code})
	} else {
		resp, err = c.rpc.LoginByPassword(callCtx, &auth.LoginByPasswordReq{Email: req.Email, Password: req.Password})
	}
	if err != nil {
		return authclient.LoginResponse{}, err
	}
	return loginResponseWithMetadata(callCtx, resp)
}

func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (authclient.LoginResponse, error) {
	callCtx := metainfo.WithBackwardValues(ctx)
	resp, err := c.rpc.RefreshToken(callCtx, &auth.RefreshTokenReq{RefreshToken: refreshToken})
	if err != nil {
		return authclient.LoginResponse{}, err
	}
	return loginResponseWithMetadata(callCtx, resp)
}

func (c *Client) VerifyToken(ctx context.Context, req authclient.VerifyTokenRequest) (authclient.VerifyTokenResponse, error) {
	rpcReq := &auth.VerifyTokenReq{AccessToken: req.AccessToken}
	if req.RequiredAudience != "" {
		rpcReq.RequiredAudience = &req.RequiredAudience
	}
	resp, err := c.rpc.VerifyToken(ctx, rpcReq)
	if err != nil {
		return authclient.VerifyTokenResponse{}, err
	}
	data := resp.GetData()
	if data == nil {
		return authclient.VerifyTokenResponse{}, nil
	}
	out := authclient.VerifyTokenResponse{
		Valid:  data.GetValid(),
		Reason: data.GetReason(),
	}
	if data.IsSetTokenId() {
		out.TokenID = data.GetTokenId()
	}
	if data.IsSetExpiresAtUnix() {
		out.ExpiresAtUnix = data.GetExpiresAtUnix()
	}
	if data.IsSetUser() {
		out.User = fromRPCUser(data.GetUser())
	}
	return out, nil
}

func (c *Client) Logout(ctx context.Context, accessToken, refreshToken string) error {
	req := &auth.LogoutReq{}
	if accessToken != "" {
		req.AccessToken = &accessToken
	}
	if refreshToken != "" {
		req.RefreshToken = &refreshToken
	}
	_, err := c.rpc.Logout(ctx, req)
	return err
}

func (c *Client) GetProfile(ctx context.Context, accessToken string) (contracts.UserIdentity, error) {
	resp, err := c.rpc.GetProfile(ctx, &auth.GetProfileReq{AccessToken: accessToken})
	if err != nil {
		return contracts.UserIdentity{}, err
	}
	return fromRPCUser(resp.GetData()), nil
}

func (c *Client) UpdateUser(ctx context.Context, req authclient.UpdateUserRequest) (contracts.UserIdentity, error) {
	rpcReq := &auth.UpdateUserReq{AccessToken: req.AccessToken}
	if req.Email != "" {
		rpcReq.Email = &req.Email
	}
	if req.Name != "" {
		rpcReq.Name = &req.Name
	}
	if req.Avatar != "" {
		rpcReq.Avatar = &req.Avatar
	}
	resp, err := c.rpc.UpdateUser(ctx, rpcReq)
	if err != nil {
		return contracts.UserIdentity{}, err
	}
	return fromRPCUser(resp.GetData()), nil
}

func (c *Client) UploadAvatar(ctx context.Context, req authclient.UploadAvatarRequest) (string, error) {
	rpcReq := &auth.UploadAvatarReq{AccessToken: req.AccessToken, Content: req.Content}
	if req.FileName != "" {
		rpcReq.FileName = &req.FileName
	}
	if req.ContentType != "" {
		rpcReq.ContentType = &req.ContentType
	}
	resp, err := c.rpc.UploadAvatar(ctx, rpcReq)
	if err != nil {
		return "", err
	}
	if resp.GetData() == nil {
		return "", nil
	}
	return resp.GetData().GetAvatar(), nil
}

func (c *Client) GetUser(ctx context.Context, userID string) (contracts.UserIdentity, error) {
	resp, err := c.rpc.GetUser(ctx, &auth.GetUserReq{UserId: userID})
	if err != nil {
		return contracts.UserIdentity{}, err
	}
	return fromRPCUser(resp.GetData()), nil
}

func (c *Client) ListUsers(ctx context.Context, req authclient.ListUsersRequest) (authclient.ListUsersResponse, error) {
	rpcReq := &auth.ListUsersReq{}
	if req.PageSize > 0 {
		value := int32(req.PageSize)
		rpcReq.PageSize = &value
	}
	if req.PageToken != "" {
		rpcReq.PageToken = &req.PageToken
	}
	resp, err := c.rpc.ListUsers(ctx, rpcReq)
	if err != nil {
		return authclient.ListUsersResponse{}, err
	}
	data := resp.GetData()
	if data == nil {
		return authclient.ListUsersResponse{}, nil
	}
	out := authclient.ListUsersResponse{NextPageToken: data.GetNextPageToken()}
	for _, record := range data.GetUsers() {
		if record == nil {
			continue
		}
		out.Users = append(out.Users, authclient.UserRecord{
			User:      fromRPCUser(record.GetIdentity()),
			CreatedAt: time.Unix(record.GetCreatedAtUnix(), 0).UTC(),
			UpdatedAt: time.Unix(record.GetUpdatedAtUnix(), 0).UTC(),
		})
	}
	return out, nil
}

func (c *Client) GetJWKS(ctx context.Context) (string, error) {
	resp, err := c.rpc.GetJWKS(ctx, &auth.JWKSReq{})
	if err != nil {
		return "", err
	}
	if resp.GetData() == nil {
		return "", nil
	}
	return resp.GetData().GetJwksJson(), nil
}

func fromRPCLogin(resp *auth.UserResp) authclient.LoginResponse {
	if resp == nil || resp.GetData() == nil {
		return authclient.LoginResponse{}
	}
	return authclient.LoginResponse{User: fromRPCUser(resp.GetData())}
}

func loginResponseWithMetadata(ctx context.Context, resp *auth.UserResp) (authclient.LoginResponse, error) {
	result := fromRPCLogin(resp)
	meta, exists, err := httpmeta.Receive(ctx)
	if err != nil {
		return authclient.LoginResponse{}, err
	}
	if !exists {
		return result, nil
	}
	for _, cookie := range meta.Cookies {
		if cookie.Action != httpmeta.CookieSet {
			continue
		}
		switch cookie.Name {
		case "access_token":
			result.Tokens.AccessToken = cookie.Value
			result.Tokens.AccessExpiresAtUnix = cookie.ExpiresAtUnix
		case "refresh_token":
			result.Tokens.RefreshToken = cookie.Value
			result.Tokens.RefreshExpiresAtUnix = cookie.ExpiresAtUnix
		}
	}
	return result, nil
}

func fromRPCUser(user *auth.UserIdentity) contracts.UserIdentity {
	if user == nil {
		return contracts.UserIdentity{}
	}
	return contracts.UserIdentity{
		UserID:      user.GetUserId(),
		Name:        user.GetName(),
		DisplayName: user.GetDisplayName(),
		Email:       user.GetEmail(),
		Avatar:      user.GetAvatar(),
		Roles:       user.GetRoles(),
	}
}
