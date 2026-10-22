package authapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nciyuan9264/game-platform/internal/authclient"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/rpcmeta"
)

const (
	accessCookieName  = "access_token"
	refreshCookieName = "refresh_token"
	loginCookieName   = "gamebus-login"
)

type Config struct {
	CookieDomain    string
	CookieSecure    bool
	AvatarMaxSizeMB int64
	ExposeDevCode   bool
	RateLimiter     RateLimiter
}

type RateLimiter interface {
	AllowRateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error)
}

type Server struct {
	client   authclient.Client
	verifier *authclient.Verifier
	config   Config
	limiter  RateLimiter
}

func New(client authclient.Client, cfg Config) *Server {
	if cfg.AvatarMaxSizeMB <= 0 {
		cfg.AvatarMaxSizeMB = 5
	}
	return &Server{
		client:   client,
		verifier: authclient.NewVerifier(client, client),
		config:   cfg,
		limiter:  cfg.RateLimiter,
	}
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("/platform/auth/register", s.register)
	mux.HandleFunc("/platform/auth/send-code", s.sendCode)
	mux.HandleFunc("/platform/auth/login/password", s.loginByPassword)
	mux.HandleFunc("/platform/auth/verify-code", s.loginByCode)
	mux.HandleFunc("/platform/auth/refresh", s.refresh)
	mux.HandleFunc("/platform/auth/verify-token", s.verifyToken)
	mux.HandleFunc("/platform/auth/logout", s.logout)
	mux.HandleFunc("/platform/auth/profile", s.profile)
	mux.HandleFunc("/platform/auth/update", s.updateUser)
	mux.HandleFunc("/platform/auth/avatar/upload", s.uploadAvatar)
	mux.HandleFunc("/.well-known/jwks.json", s.jwks)
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.allowAuthRequest(w, r, "register", req.Email, 3, 10, time.Hour) {
		return
	}
	if err := s.client.Register(r.Context(), req.Email, req.Password); err != nil {
		writeRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response{StatusCode: http.StatusOK, Message: "注册成功"})
}

func (s *Server) sendCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.allowAuthRequest(w, r, "send_code", req.Email, 3, 10, 10*time.Minute) {
		return
	}
	result, err := s.client.SendCode(r.Context(), req.Email)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	data := map[string]any{}
	if s.config.ExposeDevCode && result.DevCode != "" {
		data["dev_code"] = result.DevCode
	}
	writeJSON(w, http.StatusOK, response{StatusCode: http.StatusOK, Message: "验证码发送成功", Data: data})
}

func (s *Server) loginByPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.allowAuthRequest(w, r, "password_login", req.Email, 10, 50, 10*time.Minute) {
		return
	}
	result, err := s.client.Login(r.Context(), authclient.LoginRequest{Email: req.Email, Password: req.Password})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	s.writeLogin(w, result)
}

func (s *Server) loginByCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.allowAuthRequest(w, r, "code_login", req.Email, 5, 30, 10*time.Minute) {
		return
	}
	result, err := s.client.Login(r.Context(), authclient.LoginRequest{Email: req.Email, Code: req.Code})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	s.writeLogin(w, result)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	refreshToken, err := cookieValue(r, refreshCookieName)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, response{StatusCode: http.StatusUnauthorized, Error: "missing_token", Message: "missing refresh token"})
		return
	}
	result, err := s.client.RefreshToken(r.Context(), refreshToken)
	if err != nil {
		s.clearCookies(w)
		writeRPCError(w, err)
		return
	}
	s.writeLogin(w, result)
}

func (s *Server) verifyToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	token, err := accessToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, response{StatusCode: http.StatusUnauthorized, Error: "missing_token", Message: err.Error()})
		return
	}
	principal, err := s.verifier.VerifyPrincipal(r.Context(), token)
	if err != nil {
		writeVerificationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response{
		StatusCode: http.StatusOK,
		Message:    "校验token成功",
		Data:       publicPrincipal(principal),
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	access, _ := accessToken(r)
	refresh, _ := cookieValue(r, refreshCookieName)
	if err := s.client.Logout(r.Context(), access, refresh); err != nil {
		writeRPCError(w, err)
		return
	}
	s.clearCookies(w)
	writeJSON(w, http.StatusOK, response{StatusCode: http.StatusOK, Message: "退出登录成功"})
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	token, err := accessToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, response{StatusCode: http.StatusUnauthorized, Error: "missing_token", Message: err.Error()})
		return
	}
	user, err := s.verifier.Verify(r.Context(), token)
	if err != nil {
		writeVerificationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response{StatusCode: http.StatusOK, Message: "ok", Data: publicUser(user)})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	token, err := accessToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, response{StatusCode: http.StatusUnauthorized, Error: "missing_token", Message: err.Error()})
		return
	}
	var req struct {
		Email  string `json:"email"`
		Name   string `json:"name"`
		Avatar string `json:"avatar"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	principal, err := s.verifier.VerifyPrincipal(r.Context(), token)
	if err != nil {
		writeVerificationError(w, err)
		return
	}
	ctx := rpcmeta.WithUserIdentity(r.Context(), principal.UserID, principal.Roles)
	user, err := s.client.UpdateUser(ctx, authclient.UpdateUserRequest{
		AccessToken: token,
		Email:       req.Email,
		Name:        req.Name,
		Avatar:      req.Avatar,
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response{StatusCode: http.StatusOK, Message: "用户信息更新成功", Data: publicUser(user)})
}

func (s *Server) uploadAvatar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	token, err := accessToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, response{StatusCode: http.StatusUnauthorized, Error: "missing_token", Message: err.Error()})
		return
	}
	maxBytes := s.config.AvatarMaxSizeMB * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1024*1024)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, response{StatusCode: http.StatusRequestEntityTooLarge, Error: "avatar_too_large", Message: "avatar file is too large"})
		return
	}
	file, header, err := r.FormFile("avatar")
	if err != nil {
		file, header, err = r.FormFile("file")
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, response{StatusCode: http.StatusBadRequest, Error: "invalid_request", Message: "avatar file is required"})
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, response{StatusCode: http.StatusBadRequest, Error: "invalid_request", Message: "failed to read avatar"})
		return
	}
	if int64(len(content)) > maxBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, response{StatusCode: http.StatusRequestEntityTooLarge, Error: "avatar_too_large", Message: "avatar file is too large"})
		return
	}
	principal, err := s.verifier.VerifyPrincipal(r.Context(), token)
	if err != nil {
		writeVerificationError(w, err)
		return
	}
	ctx := rpcmeta.WithUserIdentity(r.Context(), principal.UserID, principal.Roles)
	url, err := s.client.UploadAvatar(ctx, authclient.UploadAvatarRequest{
		AccessToken: token,
		Content:     content,
		FileName:    header.Filename,
		ContentType: header.Header.Get("Content-Type"),
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response{
		StatusCode: http.StatusOK,
		Message:    "头像上传成功",
		Data:       map[string]string{"avatar": url, "url": url},
	})
}

func (s *Server) jwks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	value, err := s.client.GetJWKS(r.Context())
	if err != nil {
		writeRPCError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(value))
}

func (s *Server) writeLogin(w http.ResponseWriter, result authclient.LoginResponse) {
	now := time.Now()
	s.setCookie(w, accessCookieName, result.Tokens.AccessToken, time.Unix(result.Tokens.AccessExpiresAtUnix, 0).Sub(now), true)
	s.setCookie(w, refreshCookieName, result.Tokens.RefreshToken, time.Unix(result.Tokens.RefreshExpiresAtUnix, 0).Sub(now), true)
	s.setCookie(w, loginCookieName, result.User.Avatar, time.Unix(result.Tokens.RefreshExpiresAtUnix, 0).Sub(now), false)
	writeJSON(w, http.StatusOK, response{
		StatusCode: http.StatusOK,
		Message:    "登录成功",
		Data: map[string]any{
			"user":                    publicUser(result.User),
			"access_expires_at_unix":  result.Tokens.AccessExpiresAtUnix,
			"refresh_expires_at_unix": result.Tokens.RefreshExpiresAtUnix,
		},
	})
}

func (s *Server) allowAuthRequest(w http.ResponseWriter, r *http.Request, action, account string, accountLimit, ipLimit int, window time.Duration) bool {
	if s.limiter == nil {
		return true
	}
	keys := []struct {
		key   string
		limit int
	}{
		{key: authRateLimitKey(action, "account", strings.ToLower(strings.TrimSpace(account))), limit: accountLimit},
		{key: authRateLimitKey(action, "ip", requestIP(r)), limit: ipLimit},
	}
	for _, item := range keys {
		allowed, retryAfter, err := s.limiter.AllowRateLimit(r.Context(), item.key, item.limit, window)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, response{StatusCode: http.StatusServiceUnavailable, Error: "rate_limit_unavailable", Message: "authentication rate limiter is unavailable"})
			return false
		}
		if !allowed {
			seconds := int64((retryAfter + time.Second - 1) / time.Second)
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
			writeJSON(w, http.StatusTooManyRequests, response{StatusCode: http.StatusTooManyRequests, Error: "rate_limited", Message: "too many authentication attempts"})
			return false
		}
	}
	return true
}

func authRateLimitKey(action, dimension, value string) string {
	digest := sha256.Sum256([]byte(value))
	return "rate:auth:" + action + ":" + dimension + ":" + fmt.Sprintf("%x", digest[:])
}

func requestIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(forwarded) != nil {
		return forwarded
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (s *Server) clearCookies(w http.ResponseWriter) {
	s.setCookie(w, accessCookieName, "", -time.Hour, true)
	s.setCookie(w, refreshCookieName, "", -time.Hour, true)
	s.setCookie(w, loginCookieName, "", -time.Hour, false)
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration, httpOnly bool) {
	maxAge := int(ttl.Seconds())
	if ttl < 0 {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Domain:   s.config.CookieDomain,
		MaxAge:   maxAge,
		HttpOnly: httpOnly,
		Secure:   s.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func accessToken(r *http.Request) (string, error) {
	if cookie, err := cookieValue(r, accessCookieName); err == nil && cookie != "" {
		return cookie, nil
	}
	value := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if value == "" {
		return "", errors.New("missing access token")
	}
	return value, nil
}

func cookieValue(r *http.Request, name string) (string, error) {
	cookie, err := r.Cookie(name)
	if err != nil || cookie.Value == "" {
		return "", errors.New("missing cookie")
	}
	return cookie.Value, nil
}

type response struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
	Data       any    `json:"data,omitempty"`
	Error      string `json:"error,omitempty"`
}

func publicUser(user contracts.UserIdentity) map[string]any {
	return map[string]any{
		"id":          user.UserID,
		"user_id":     user.UserID,
		"userID":      user.UserID,
		"name":        user.Name,
		"displayName": user.DisplayName,
		"email":       user.Email,
		"avatar":      user.Avatar,
		"roles":       user.Roles,
	}
}

func publicPrincipal(user contracts.UserIdentity) map[string]any {
	return map[string]any{
		"user_id": user.UserID,
		"roles":   user.Roles,
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, response{StatusCode: http.StatusBadRequest, Error: "invalid_request", Message: err.Error()})
		return false
	}
	return true
}

func methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, response{StatusCode: http.StatusMethodNotAllowed, Error: "method_not_allowed", Message: "method not allowed"})
}

func writeRPCError(w http.ResponseWriter, err error) {
	code := "auth_unavailable"
	message := err.Error()
	status := http.StatusServiceUnavailable
	var coded interface {
		GetCode() string
		GetMessage() string
	}
	if errors.As(err, &coded) {
		code = coded.GetCode()
		message = coded.GetMessage()
		switch code {
		case "invalid_request", "invalid_code", "invalid_page_token", "invalid_user_id":
			status = http.StatusBadRequest
		case "invalid_credentials", "invalid_token", "token_expired", "token_revoked", "user_not_found":
			status = http.StatusUnauthorized
		case "email_conflict":
			status = http.StatusConflict
		case "email_change_requires_verification":
			status = http.StatusConflict
		case "avatar_too_large":
			status = http.StatusRequestEntityTooLarge
		case "unsupported_avatar_type":
			status = http.StatusUnsupportedMediaType
		}
	}
	writeJSON(w, status, response{StatusCode: status, Error: code, Message: message})
}

func writeVerificationError(w http.ResponseWriter, err error) {
	if errors.Is(err, authclient.ErrUnauthenticated) {
		writeJSON(w, http.StatusUnauthorized, response{
			StatusCode: http.StatusUnauthorized,
			Error:      "invalid_token",
			Message:    err.Error(),
		})
		return
	}
	writeRPCError(w, err)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
