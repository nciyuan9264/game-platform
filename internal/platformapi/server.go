package platformapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nciyuan9264/game-platform/internal/authapi"
	"github.com/nciyuan9264/game-platform/internal/authclient"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	platformtoken "github.com/nciyuan9264/game-platform/internal/platform/token"
	"github.com/nciyuan9264/game-platform/internal/rpcmeta"
	"github.com/nciyuan9264/game-platform/internal/store"
)

type roomController interface {
	CreateRoom(ctx context.Context, gameType, ownerUserID, ownerPlayerID, requestID string, maxPlayers int) (contracts.RoomRecord, error)
	StartMatch(ctx context.Context, roomID, actorUserID string, actorRoles []string, requestID string) (contracts.RoomRecord, string, int, error)
	CloseRoom(ctx context.Context, roomID, actorUserID string, actorRoles []string, reason string) (contracts.RoomRecord, error)
	InterruptRoom(ctx context.Context, roomID, actorUserID string, actorRoles []string, reason string) (contracts.RoomRecord, error)
}

type Server struct {
	store       *store.Store
	auth        authclient.Client
	rooms       roomController
	spectators  spectatorStore
	authHandler *authapi.Server
}

func New(st *store.Store, auth authclient.Client, rooms roomController, authConfig authapi.Config) *Server {
	if authConfig.RateLimiter == nil && st != nil {
		authConfig.RateLimiter = st
	}
	return &Server{
		store:       st,
		auth:        auth,
		rooms:       rooms,
		spectators:  st,
		authHandler: authapi.New(auth, authConfig),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	health := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	mux.HandleFunc("/healthz", health)
	mux.HandleFunc("/platform/healthz", health)
	s.authHandler.Register(mux)
	mux.HandleFunc("/platform/rooms", s.handleRooms)
	mux.HandleFunc("/platform/rooms/", s.handleRoom)
	mux.HandleFunc("/platform/history/games", s.historyGames)
	mux.HandleFunc("/platform/history/games/", s.historyGame)
	mux.HandleFunc("/platform/history/stats", s.historyStats)
	mux.HandleFunc("/platform/rankings", s.rankings)
	mux.HandleFunc("/platform/spectator/rooms/", s.spectatorRoom)
	mux.HandleFunc("/platform/spectator/sessions/", s.spectatorSession)
	mux.HandleFunc("/platform/__loadtest/acquire/rooms", s.loadtestRooms)
	mux.HandleFunc("/platform/__loadtest/acquire/stats", s.loadtestStats)
	mux.HandleFunc("/platform/admin/users", s.adminUsers)
	mux.HandleFunc("/platform/admin/users/", s.adminUser)
	mux.HandleFunc("/platform/admin/matches", s.adminMatches)
	mux.HandleFunc("/platform/admin/matches/", s.adminMatch)
	return withCORS(mux)
}

func (s *Server) handleRooms(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		identity, ok := s.requireIdentity(w, r)
		if !ok {
			return
		}
		rooms, err := s.store.ListRooms(r.Context(), r.URL.Query().Get("gameType"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err)
			return
		}
		active := rooms[:0]
		for _, room := range rooms {
			if !contracts.IsTerminalRoomStatus(room.Status) {
				active = append(active, room)
			}
		}
		if err := s.store.PopulateSpectatorRoomAccess(r.Context(), active, identity.UserID); err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{"rooms": active}})
	case http.MethodPost:
		identity, ok := s.requireIdentity(w, r)
		if !ok {
			return
		}
		var req struct {
			GameType   string `json:"gameType"`
			MaxPlayers int    `json:"maxPlayers"`
			RequestID  string `json:"requestID,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err)
			return
		}
		if s.rooms == nil {
			writeJSON(w, http.StatusServiceUnavailable, apiResponse{StatusCode: http.StatusServiceUnavailable, Error: "control_unavailable", Message: "control plane is unavailable"})
			return
		}
		requestID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if requestID == "" {
			requestID = strings.TrimSpace(req.RequestID)
		}
		if requestID == "" {
			requestID = uuid.NewString()
		}
		room, err := s.rooms.CreateRoom(r.Context(), req.GameType, identity.UserID, platformtoken.LegacyPlayerID(identity), requestID, req.MaxPlayers)
		if err != nil {
			writeRPCError(w, err, "control_unavailable")
			return
		}
		w.Header().Set("Idempotency-Key", requestID)
		writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: room})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleRoom(w http.ResponseWriter, r *http.Request) {
	identity, ok := s.requireIdentity(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/platform/rooms/"), "/")
	parts := strings.Split(path, "/")
	roomID, err := url.PathUnescape(parts[0])
	if err != nil || roomID == "" || len(parts) > 2 {
		writeJSON(w, http.StatusBadRequest, apiResponse{StatusCode: http.StatusBadRequest, Error: "bad_request", Message: "roomID is required"})
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if len(parts) != 1 {
			methodNotAllowed(w)
			return
		}
		room, err := s.store.GetRoom(r.Context(), roomID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", err)
			return
		}
		if !canCloseRoom(identity, room) {
			writeJSON(w, http.StatusForbidden, apiResponse{StatusCode: http.StatusForbidden, Error: "forbidden", Message: "only the room owner or an administrator can close this room"})
			return
		}
		if s.rooms == nil {
			writeJSON(w, http.StatusServiceUnavailable, apiResponse{StatusCode: http.StatusServiceUnavailable, Error: "control_unavailable", Message: "control plane is unavailable"})
			return
		}
		room, err = s.rooms.CloseRoom(r.Context(), roomID, identity.UserID, identity.Roles, "user_closed")
		if err != nil {
			writeRPCError(w, err, "control_unavailable")
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: room})
	case http.MethodPost:
		if len(parts) != 2 || parts[1] != "matches" {
			methodNotAllowed(w)
			return
		}
		room, err := s.store.GetRoom(r.Context(), roomID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", err)
			return
		}
		if !canCloseRoom(identity, room) {
			writeJSON(w, http.StatusForbidden, apiResponse{StatusCode: http.StatusForbidden, Error: "forbidden", Message: "only the room owner or an administrator can start a match"})
			return
		}
		requestID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if requestID == "" {
			requestID = uuid.NewString()
		}
		room, matchID, roundNo, err := s.rooms.StartMatch(r.Context(), roomID, identity.UserID, identity.Roles, requestID)
		if err != nil {
			writeRPCError(w, err, "control_unavailable")
			return
		}
		w.Header().Set("Idempotency-Key", requestID)
		writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{
			"room": room, "matchID": matchID, "roundNo": roundNo,
		}})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if _, ok := s.requireRole(w, r, "admin"); !ok {
		return
	}
	users, err := s.listAdminUsers(r.Context())
	if err != nil {
		writeRPCError(w, err, "auth_unavailable")
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if query != "" {
		filtered := users[:0]
		for _, user := range users {
			if strings.Contains(strings.ToLower(user.UserID), query) ||
				strings.Contains(strings.ToLower(user.DisplayName), query) ||
				strings.Contains(strings.ToLower(user.Name), query) ||
				strings.Contains(strings.ToLower(user.Email), query) {
				filtered = append(filtered, user)
			}
		}
		users = filtered
	}
	writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{"users": users}})
}

func (s *Server) adminUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if _, ok := s.requireRole(w, r, "admin"); !ok {
		return
	}
	userID, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/platform/admin/users/"))
	if err != nil || userID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", errors.New("userID is required"))
		return
	}
	users, err := s.listAdminUsers(r.Context())
	if err != nil {
		writeRPCError(w, err, "auth_unavailable")
		return
	}
	for _, user := range users {
		if user.UserID == userID {
			ctx := r.Context()
			sessions, err := s.store.ListSessions(ctx)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "store_error", err)
				return
			}
			presences, err := s.store.ListAllPresence(ctx)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "store_error", err)
				return
			}
			rooms, err := s.store.ListRooms(ctx, "")
			if err != nil {
				writeError(w, http.StatusInternalServerError, "store_error", err)
				return
			}
			matches, err := s.store.ListMatches(ctx, "", "", "", 500)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "store_error", err)
				return
			}

			userSessions := make([]contracts.SessionRecord, 0)
			for _, session := range sessions {
				if sameAdminUser(user, session.UserID, session.Email) {
					userSessions = append(userSessions, session)
				}
			}
			userPresences := make([]contracts.PresenceRecord, 0)
			for _, presence := range presences {
				if sameAdminUser(user, presence.UserID, "") {
					userPresences = append(userPresences, presence)
				}
			}
			ownedRooms := make([]contracts.RoomRecord, 0)
			for _, room := range rooms {
				if sameAdminUser(user, room.OwnerUserID, "") {
					ownedRooms = append(ownedRooms, room)
				}
			}
			userMatches := make([]contracts.MatchRecord, 0)
			ownedMatches := make([]contracts.MatchRecord, 0)
			for _, match := range matches {
				includeMatch := sameAdminUser(user, match.OwnerUserID, "")
				if includeMatch {
					ownedMatches = append(ownedMatches, match)
				}
				for _, player := range match.Players {
					if sameAdminUser(user, player.UserID, player.Email) {
						includeMatch = true
						break
					}
				}
				if includeMatch {
					userMatches = append(userMatches, match)
				}
			}
			writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{
				"user": user, "sessions": userSessions, "presences": userPresences,
				"matches": userMatches, "ownedMatches": ownedMatches, "ownedRooms": ownedRooms,
			}})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, apiResponse{StatusCode: http.StatusNotFound, Error: "not_found", Message: "user not found"})
}

func (s *Server) adminMatches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if _, ok := s.requireRole(w, r, "admin"); !ok {
		return
	}
	limit := 200
	matches, err := s.store.ListMatches(r.Context(), r.URL.Query().Get("gameType"), r.URL.Query().Get("status"), r.URL.Query().Get("userID"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{"matches": matches}})
}

func (s *Server) adminMatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if _, ok := s.requireRole(w, r, "admin"); !ok {
		return
	}
	matchID, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/platform/admin/matches/"))
	if err != nil || matchID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", errors.New("matchID is required"))
		return
	}
	match, err := s.store.GetMatch(r.Context(), matchID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{"match": match}})
}

func (s *Server) requireIdentity(w http.ResponseWriter, r *http.Request) (contracts.UserIdentity, bool) {
	token, err := requestToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{StatusCode: http.StatusUnauthorized, Error: "missing_token", Message: err.Error()})
		return contracts.UserIdentity{}, false
	}
	result, err := s.auth.VerifyToken(r.Context(), authclient.VerifyTokenRequest{AccessToken: token, RequiredAudience: "game-platform"})
	if err != nil {
		writeRPCError(w, err, "auth_unavailable")
		return contracts.UserIdentity{}, false
	}
	if !result.Valid {
		writeJSON(w, http.StatusUnauthorized, apiResponse{StatusCode: http.StatusUnauthorized, Error: "invalid_token", Message: result.Reason})
		return contracts.UserIdentity{}, false
	}
	ctx := rpcmeta.WithUserToken(r.Context(), token)
	ctx = rpcmeta.WithUserIdentity(ctx, result.User.UserID, result.User.Roles)
	*r = *r.WithContext(ctx)
	return result.User, true
}

func (s *Server) requireRole(w http.ResponseWriter, r *http.Request, required string) (contracts.UserIdentity, bool) {
	identity, ok := s.requireIdentity(w, r)
	if !ok {
		return contracts.UserIdentity{}, false
	}
	if identityHasRole(identity, required) {
		return identity, true
	}
	writeJSON(w, http.StatusForbidden, apiResponse{StatusCode: http.StatusForbidden, Error: "forbidden", Message: required + " role is required"})
	return contracts.UserIdentity{}, false
}

func identityHasRole(identity contracts.UserIdentity, required string) bool {
	for _, role := range identity.Roles {
		if role == required {
			return true
		}
	}
	return false
}

func canCloseRoom(identity contracts.UserIdentity, room contracts.RoomRecord) bool {
	return identity.UserID == room.OwnerUserID || identityHasRole(identity, "admin")
}

func (s *Server) listAdminUsers(ctx context.Context) ([]contracts.AdminUserSummary, error) {
	authUsers := make([]contracts.AuthUserRecord, 0)
	pageToken := ""
	for len(authUsers) < 1000 {
		page, err := s.auth.ListUsers(ctx, authclient.ListUsersRequest{PageSize: 200, PageToken: pageToken})
		if err != nil {
			return nil, err
		}
		for _, record := range page.Users {
			authUsers = append(authUsers, contracts.AuthUserRecord{
				UserID:    record.User.UserID,
				Email:     record.User.Email,
				Name:      record.User.Name,
				Avatar:    record.User.Avatar,
				CreatedAt: record.CreatedAt,
				UpdatedAt: record.UpdatedAt,
			})
		}
		if page.NextPageToken == "" || len(page.Users) == 0 {
			break
		}
		pageToken = page.NextPageToken
	}
	sessions, err := s.store.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	presences, err := s.store.ListAllPresence(ctx)
	if err != nil {
		return nil, err
	}
	rooms, err := s.store.ListRooms(ctx, "")
	if err != nil {
		return nil, err
	}
	matches, err := s.store.ListMatches(ctx, "", "", "", 500)
	if err != nil {
		return nil, err
	}
	return buildAdminUsers(authUsers, sessions, presences, rooms, matches), nil
}

func buildAdminUsers(authUsers []contracts.AuthUserRecord, sessions []contracts.SessionRecord, presences []contracts.PresenceRecord, rooms []contracts.RoomRecord, matches []contracts.MatchRecord) []contracts.AdminUserSummary {
	byID := map[string]*contracts.AdminUserSummary{}
	aliasToUserID := map[string]string{}
	ensure := func(userID string) *contracts.AdminUserSummary {
		if canonical := aliasToUserID[strings.ToLower(strings.TrimSpace(userID))]; canonical != "" {
			userID = canonical
		}
		if userID == "" {
			userID = "unknown"
		}
		if existing := byID[userID]; existing != nil {
			return existing
		}
		user := &contracts.AdminUserSummary{UserID: userID}
		byID[userID] = user
		return user
	}
	activeMatches := map[string]map[string]bool{}
	markActive := func(userID, matchID string) {
		if userID == "" || matchID == "" {
			return
		}
		if activeMatches[userID] == nil {
			activeMatches[userID] = map[string]bool{}
		}
		activeMatches[userID][matchID] = true
	}

	for _, authUser := range authUsers {
		user := ensure(authUser.UserID)
		mergeUserProfile(user, authUser.Name, authUser.Email, authUser.Avatar, "")
		user.FirstSeen = earliest(user.FirstSeen, authUser.CreatedAt)
		user.LastSeen = latest(user.LastSeen, authUser.UpdatedAt)
		registerUserAliases(aliasToUserID, user.UserID, authUser.Email)
	}
	for _, session := range sessions {
		user := ensure(session.UserID)
		mergeUserProfile(user, session.Name, session.Email, session.Avatar, session.DisplayName)
		registerUserAliases(aliasToUserID, user.UserID, user.Email, session.UserID)
		user.ActiveSessions++
		user.Online = true
		user.GatewayNodes = appendUnique(user.GatewayNodes, session.GatewayNode)
		user.FirstSeen = earliest(user.FirstSeen, session.ConnectedAt)
		user.LastSeen = latest(user.LastSeen, session.LastSeen)
	}
	for _, presence := range presences {
		user := ensure(presence.UserID)
		registerUserAliases(aliasToUserID, user.UserID, presence.UserID)
		if presence.Online {
			markActive(user.UserID, presence.RoomID)
		}
		user.RoomIDs = appendUnique(user.RoomIDs, presence.RoomID)
		user.MatchIDs = appendUnique(user.MatchIDs, presence.RoomID)
		user.GatewayNodes = appendUnique(user.GatewayNodes, presence.GatewayNode)
		user.FirstSeen = earliest(user.FirstSeen, presence.JoinedAt)
		user.LastSeen = latest(user.LastSeen, presence.LastSeen)
	}
	for _, room := range rooms {
		if room.OwnerUserID == "" {
			continue
		}
		user := ensure(room.OwnerUserID)
		registerUserAliases(aliasToUserID, user.UserID, room.OwnerUserID)
		user.OwnedRoomIDs = appendUnique(user.OwnedRoomIDs, room.RoomID)
		user.FirstSeen = earliest(user.FirstSeen, room.CreatedAt)
		user.LastSeen = latest(user.LastSeen, room.UpdatedAt)
	}
	for _, match := range matches {
		if match.OwnerUserID != "" {
			user := ensure(match.OwnerUserID)
			registerUserAliases(aliasToUserID, user.UserID, match.OwnerUserID)
			user.MatchIDs = appendUnique(user.MatchIDs, match.MatchID)
			user.OwnedMatchIDs = appendUnique(user.OwnedMatchIDs, match.MatchID)
			user.FirstSeen = earliest(user.FirstSeen, match.StartedAt)
			user.LastSeen = latest(user.LastSeen, match.UpdatedAt)
			if !contracts.IsTerminalMatchStatus(match.Status) {
				markActive(user.UserID, match.MatchID)
			}
		}
		for _, player := range match.Players {
			if player.IsAI {
				continue
			}
			user := ensure(player.UserID)
			mergeUserProfile(user, player.Name, player.Email, player.Avatar, player.DisplayName)
			registerUserAliases(aliasToUserID, user.UserID, player.Email, player.UserID)
			user.MatchIDs = appendUnique(user.MatchIDs, match.MatchID)
			user.GatewayNodes = appendUnique(user.GatewayNodes, player.GatewayNode)
			user.FirstSeen = earliest(user.FirstSeen, player.JoinedAt)
			user.LastSeen = latest(user.LastSeen, player.LastSeen)
		}
	}
	users := make([]contracts.AdminUserSummary, 0, len(byID))
	for _, user := range byID {
		user.ActiveMatches = len(activeMatches[user.UserID])
		user.ActiveRooms = user.ActiveMatches
		if user.DisplayName == "" {
			if user.Name != "" {
				user.DisplayName = user.Name
			} else {
				user.DisplayName = user.UserID
			}
		}
		sort.Strings(user.GatewayNodes)
		sort.Strings(user.RoomIDs)
		sort.Strings(user.MatchIDs)
		sort.Strings(user.OwnedRoomIDs)
		sort.Strings(user.OwnedMatchIDs)
		users = append(users, *user)
	}
	sort.Slice(users, func(i, j int) bool {
		if users[i].Online != users[j].Online {
			return users[i].Online
		}
		return users[i].UserID < users[j].UserID
	})
	return users
}

func registerUserAliases(aliasToUserID map[string]string, userID string, aliases ...string) {
	if userID == "" {
		return
	}
	for _, alias := range aliases {
		alias = strings.ToLower(strings.TrimSpace(alias))
		if alias != "" {
			aliasToUserID[alias] = userID
		}
	}
}

func sameAdminUser(user contracts.AdminUserSummary, candidateID, candidateEmail string) bool {
	if candidateID != "" && candidateID == user.UserID {
		return true
	}
	if user.Email == "" {
		return false
	}
	email := strings.ToLower(strings.TrimSpace(user.Email))
	return strings.EqualFold(candidateID, email) || strings.EqualFold(candidateEmail, email)
}

func mergeUserProfile(user *contracts.AdminUserSummary, name, email, avatar, displayName string) {
	if user.Name == "" && name != "" {
		user.Name = name
	}
	if user.Email == "" && email != "" {
		user.Email = email
	}
	if user.Avatar == "" && avatar != "" {
		user.Avatar = avatar
	}
	if user.DisplayName == "" && displayName != "" {
		user.DisplayName = displayName
	}
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func earliest(left, right time.Time) time.Time {
	if right.IsZero() {
		return left
	}
	if left.IsZero() || right.Before(left) {
		return right
	}
	return left
}

func latest(left, right time.Time) time.Time {
	if right.IsZero() {
		return left
	}
	if left.IsZero() || right.After(left) {
		return right
	}
	return left
}

func requestToken(r *http.Request) (string, error) {
	if cookie, err := r.Cookie("access_token"); err == nil && cookie.Value != "" {
		return cookie.Value, nil
	}
	value := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if value == "" {
		return "", errors.New("missing access token")
	}
	return value, nil
}

type apiResponse struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
	Data       any    `json:"data,omitempty"`
	Error      string `json:"error,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code string, err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	writeJSON(w, status, apiResponse{StatusCode: status, Error: code, Message: message})
}

func writeRPCError(w http.ResponseWriter, err error, fallback string) {
	code := fallback
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
		case "invalid_credentials", "invalid_token", "token_expired", "token_revoked", "user_not_found", "missing_token":
			status = http.StatusUnauthorized
		case "no_roomhost":
			status = http.StatusServiceUnavailable
		case "invalid_request", "invalid_code", "invalid_page_token", "invalid_user_id", "unknown_game":
			status = http.StatusBadRequest
		case "email_conflict", "too_many_rooms", "idempotency_conflict", "creation_in_progress", "match_active", "room_not_open":
			status = http.StatusConflict
		}
	}
	writeJSON(w, status, apiResponse{StatusCode: status, Error: code, Message: message})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, apiResponse{StatusCode: http.StatusMethodNotAllowed, Error: "method_not_allowed", Message: "method not allowed"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowedCORSOrigin(origin, r.Host) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowedCORSOrigin(origin, requestHost string) bool {
	parsed, err := url.Parse(origin)
	if err == nil && parsed.Host == requestHost {
		return true
	}
	for _, allowed := range strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",") {
		if strings.TrimSpace(allowed) == origin {
			return true
		}
	}
	return false
}
