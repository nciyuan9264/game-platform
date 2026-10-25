package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/coordination"
	"github.com/nciyuan9264/game-platform/internal/observability"
	"github.com/nciyuan9264/game-platform/internal/platform/token"
	"github.com/nciyuan9264/game-platform/internal/playerprofile"
	"github.com/nciyuan9264/game-platform/internal/store"
	"github.com/nciyuan9264/game-platform/internal/transport"
)

type Server struct {
	nodeID                 string
	verifier               token.Verifier
	store                  *store.Store
	router                 *transport.Router
	owners                 ownerResolver
	profiles               *playerprofile.Resolver
	profileRefreshInterval time.Duration

	mu    sync.RWMutex
	conns map[string]*clientConnection
}

type ownerResolver interface {
	ReadyRoomOwner(context.Context, string) (coordination.RoomOwner, error)
}

func NewServer(
	nodeID string,
	verifier token.Verifier,
	st *store.Store,
	router *transport.Router,
	owners ownerResolver,
	profiles *playerprofile.Resolver,
	profileRefreshInterval time.Duration,
) *Server {
	return &Server{
		nodeID:                 nodeID,
		verifier:               verifier,
		store:                  st,
		router:                 router,
		owners:                 owners,
		profiles:               profiles,
		profileRefreshInterval: profileRefreshInterval,
		conns:                  map[string]*clientConnection{},
	}
}

const (
	websocketWriteTimeout = 5 * time.Second
	websocketReadLimit    = 64 << 10
	websocketPongWait     = 70 * time.Second
	websocketPingPeriod   = 30 * time.Second
)

type clientConnection struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (c *clientConnection) writeJSON(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(websocketWriteTimeout)); err != nil {
		return err
	}
	return c.conn.WriteJSON(value)
}

func (c *clientConnection) writeControl(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteControl(messageType, data, time.Now().Add(websocketWriteTimeout))
}

func (c *clientConnection) close() error {
	return c.conn.Close()
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/ws", s.handleWS)
	return mux
}

func (s *Server) Start(ctx context.Context) error {
	if err := s.cleanupStaleSessions(ctx); err != nil {
		return err
	}
	if _, err := s.router.SubscribeGatewayDeliveries(s.nodeID, s.deliver); err != nil {
		return err
	}
	go s.inactiveGatewaySessionLoop(ctx)
	if s.profileRefreshInterval > 0 {
		go s.profileRefreshLoop(ctx)
	}
	return nil
}

func (s *Server) profileRefreshLoop(ctx context.Context) {
	ticker := time.NewTicker(s.profileRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.refreshProfiles(ctx); err != nil {
				observability.Warn("gateway profile refresh failed", "nodeID", s.nodeID, "error", err)
			}
		}
	}
}

func (s *Server) refreshProfiles(ctx context.Context) error {
	sessions, err := s.store.ListGatewaySessions(ctx, s.nodeID)
	if err != nil {
		return err
	}
	players := make([]playerprofile.Player, 0, len(sessions))
	for _, session := range sessions {
		players = append(players, playerprofile.Player{
			UserID:      session.UserID,
			PlayerID:    session.PlayerID,
			DisplayName: session.DisplayName,
		})
	}
	displayNames := s.profiles.Resolve(ctx, players)
	updatedUsers := make(map[string]string)
	for _, session := range sessions {
		displayName := displayNames[session.PlayerID]
		if displayName == "" || displayName == session.DisplayName {
			continue
		}
		ttl := time.Until(session.ExpiresAt)
		if ttl <= 0 {
			continue
		}
		session.DisplayName = displayName
		if err := s.store.SetSession(ctx, session, ttl); err != nil {
			return err
		}
		updatedUsers[session.UserID] = displayName
	}
	for userID, displayName := range updatedUsers {
		if err := s.store.UpdateMatchPlayerDisplayName(ctx, userID, displayName); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) cleanupStaleSessions(ctx context.Context) error {
	sessions, err := s.store.ListGatewaySessions(ctx, s.nodeID)
	if err != nil {
		return err
	}
	presences, err := s.store.ListGatewayPresence(ctx, s.nodeID)
	if err != nil {
		return err
	}
	staleSessionIDs := map[string]struct{}{}
	for _, session := range sessions {
		if session.GatewayNode != s.nodeID {
			continue
		}
		staleSessionIDs[session.SessionID] = struct{}{}
		if err := s.store.DeleteSession(ctx, session.SessionID); err != nil {
			return err
		}
	}
	for _, presence := range presences {
		if _, stale := staleSessionIDs[presence.SessionID]; !stale {
			continue
		}
		if err := s.store.MarkPresenceOffline(ctx, presence.RoomID, presence.SessionID); err != nil {
			return err
		}
		roomPresences, err := s.store.ListPresence(ctx, presence.RoomID)
		if err != nil {
			return err
		}
		onlineElsewhere := false
		for _, other := range roomPresences {
			if other.SessionID == presence.SessionID || !other.Online {
				continue
			}
			if other.RoomID == presence.RoomID && other.PlayerID == presence.PlayerID {
				onlineElsewhere = true
				break
			}
		}
		if onlineElsewhere {
			continue
		}
		room, roomErr := s.store.GetRoom(ctx, presence.RoomID)
		if roomErr != nil {
			return roomErr
		}
		if err := s.store.MarkMatchPlayerOffline(ctx, room.CurrentMatchID, presence.PlayerID, time.Now().UTC()); err != nil {
			return err
		}
	}
	if len(staleSessionIDs) > 0 {
		observability.Info("gateway stale sessions cleaned", "nodeID", s.nodeID, "count", len(staleSessionIDs))
	}
	return nil
}
func (s *Server) inactiveGatewaySessionLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.cleanupInactiveGatewaySessions(ctx); err != nil {
				observability.Warn("inactive gateway session cleanup failed", "nodeID", s.nodeID, "error", err)
			}
		}
	}
}

func (s *Server) cleanupInactiveGatewaySessions(ctx context.Context) error {
	nodes, err := s.store.ListNodes(ctx, contracts.NodeTypeGateway)
	if err != nil {
		return err
	}
	activeNodes := map[string]struct{}{s.nodeID: {}}
	for _, node := range nodes {
		activeNodes[node.NodeID] = struct{}{}
	}

	sessions, err := s.store.ListSessions(ctx)
	if err != nil {
		return err
	}
	presences, err := s.store.ListAllPresence(ctx)
	if err != nil {
		return err
	}

	staleSessionIDs := map[string]struct{}{}
	for _, session := range sessions {
		if _, active := activeNodes[session.GatewayNode]; active {
			continue
		}
		staleSessionIDs[session.SessionID] = struct{}{}
		if err := s.store.DeleteSession(ctx, session.SessionID); err != nil {
			return err
		}
	}
	if len(staleSessionIDs) == 0 {
		return nil
	}

	activePlayers := map[string]struct{}{}
	for _, presence := range presences {
		if !presence.Online {
			continue
		}
		if _, active := activeNodes[presence.GatewayNode]; !active {
			continue
		}
		activePlayers[presence.RoomID+"\x00"+presence.PlayerID] = struct{}{}
	}
	for _, presence := range presences {
		if _, stale := staleSessionIDs[presence.SessionID]; !stale {
			continue
		}
		if err := s.store.MarkPresenceOffline(ctx, presence.RoomID, presence.SessionID); err != nil {
			return err
		}
		if _, onlineElsewhere := activePlayers[presence.RoomID+"\x00"+presence.PlayerID]; onlineElsewhere {
			continue
		}
		room, roomErr := s.store.GetRoom(ctx, presence.RoomID)
		if roomErr != nil {
			return roomErr
		}
		if err := s.store.MarkMatchPlayerOffline(ctx, room.CurrentMatchID, presence.PlayerID, time.Now().UTC()); err != nil {
			return err
		}
	}
	observability.Info("inactive gateway sessions cleaned", "nodeID", s.nodeID, "count", len(staleSessionIDs))
	return nil
}

var upgrader = websocket.Upgrader{CheckOrigin: websocketOriginAllowed}

func websocketOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	allowedOrigins := os.Getenv("WS_ALLOWED_ORIGINS")
	if allowedOrigins == "" {
		allowedOrigins = os.Getenv("CORS_ALLOWED_ORIGINS")
	}
	for _, allowed := range strings.Split(allowedOrigins, ",") {
		if strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	roomID := r.URL.Query().Get("roomID")
	if roomID == "" {
		http.Error(w, "roomID is required", http.StatusBadRequest)
		return
	}
	matchID := r.URL.Query().Get("matchID")
	if matchID == "" {
		http.Error(w, "matchID is required", http.StatusBadRequest)
		return
	}
	identity, err := s.identity(ctx, r)
	if err != nil {
		observability.Warn("websocket identity verification failed", "roomID", roomID, "error", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rec, err := s.store.GetRoom(ctx, roomID)
	if err != nil {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}
	if contracts.IsTerminalRoomStatus(rec.Status) {
		http.Error(w, "room is closed", http.StatusGone)
		return
	}
	if rec.CurrentMatchID != matchID {
		http.Error(w, "match changed", http.StatusConflict)
		return
	}
	rec, err = s.readyRoute(ctx, rec)
	if err != nil {
		http.Error(w, "room is recovering", http.StatusServiceUnavailable)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(websocketReadLimit)
	sessionID := uuid.NewString()
	playerID := token.PlayerID(identity)
	observability.Info("websocket connected", "nodeID", s.nodeID, "roomID", roomID, "sessionID", sessionID, "playerID", playerID)
	_ = s.store.SaveRuntimeEvent(ctx, contracts.RuntimeEvent{
		RoomID:    roomID,
		NodeID:    s.nodeID,
		Component: "gateway",
		Event:     "websocket_connected",
		Message:   "websocket accepted by gateway",
		Payload: map[string]any{
			"sessionID": sessionID,
			"playerID":  playerID,
			"userID":    identity.UserID,
			"name":      identity.Name,
			"email":     identity.Email,
			"avatar":    identity.Avatar,
		},
	})
	now := time.Now().UTC()
	connRef := contracts.ConnectionRef{GatewayNode: s.nodeID, SessionID: sessionID}
	session := contracts.SessionRecord{ConnectionRef: connRef, UserID: identity.UserID, PlayerID: playerID, Name: identity.Name, Email: identity.Email, Avatar: identity.Avatar, DisplayName: identity.DisplayName, ConnectedAt: now, LastSeen: now, ExpiresAt: now.Add(24 * time.Hour)}
	presence := contracts.PresenceRecord{ConnectionRef: connRef, RoomID: roomID, UserID: identity.UserID, PlayerID: playerID, Online: true, JoinedAt: now, LastSeen: now}
	_ = s.store.SetSession(ctx, session, 24*time.Hour)
	_ = s.store.SetPresence(ctx, presence, 24*time.Hour)
	_ = s.store.UpsertMatchPlayer(ctx, rec.CurrentMatchID, contracts.MatchPlayerRecord{UserID: identity.UserID, PlayerID: playerID, DisplayName: identity.DisplayName, Name: identity.Name, Email: identity.Email, Avatar: identity.Avatar, GatewayNode: s.nodeID, Online: true, JoinedAt: now, LastSeen: now})

	client := &clientConnection{conn: conn}
	s.mu.Lock()
	s.conns[sessionID] = client
	s.mu.Unlock()

	lastSeq := queryInt64(r, "lastSeq", 0)
	if lastSeq > 0 {
		s.backfill(ctx, client, roomID, rec.CurrentMatchID, sessionID, playerID, lastSeq)
	}

	cleanupFailedConnection := func() {
		s.mu.Lock()
		delete(s.conns, sessionID)
		s.mu.Unlock()
		_ = s.store.MarkPresenceOffline(context.Background(), roomID, sessionID)
		_ = s.store.DeleteSession(context.Background(), sessionID)
		_ = s.store.MarkMatchPlayerOffline(context.Background(), rec.CurrentMatchID, playerID, time.Now().UTC())
		_ = client.writeControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "room temporarily unavailable"),
		)
		_ = client.close()
	}

	connectDeadline := time.Now().Add(45 * time.Second)
	for {
		connect := contracts.RoomCommand{SchemaVersion: contracts.RealtimeSchemaVersion, CommandID: uuid.NewString(), TraceID: uuid.NewString(), RoomID: roomID, MatchID: rec.CurrentMatchID, GameType: rec.GameType, Epoch: rec.Epoch, UserID: identity.UserID, PlayerID: playerID, ConnectionRef: connRef, Type: "connect", SentAt: time.Now().UTC()}
		ack, connectErr := s.router.SendRoomCommand(context.Background(), rec.OwnerNode, connect, 3*time.Second)
		if connectErr == nil && !ack.Accepted {
			connectErr = errors.New(strings.TrimSpace(ack.ErrorCode + " " + ack.Message))
		}
		if connectErr == nil {
			break
		}

		observability.Warn("connect command failed", "roomID", roomID, "ownerNode", rec.OwnerNode, "error", connectErr)

		if time.Now().After(connectDeadline) {
			cleanupFailedConnection()
			return
		}

		if err := client.writeControl(websocket.PingMessage, nil); err != nil {
			cleanupFailedConnection()
			return
		}

		time.Sleep(time.Second)
		if latest, err := s.store.GetRoom(context.Background(), roomID); err == nil {
			if routed, routeErr := s.readyRoute(context.Background(), latest); routeErr == nil {
				rec = routed
			}
		}
	}

	go s.readLoop(client, rec, session, playerID)
}

func (s *Server) identity(ctx context.Context, r *http.Request) (contracts.UserIdentity, error) {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.Header.Get("Authorization")
		token = strings.TrimPrefix(token, "Bearer ")
	}
	if token == "" {
		if c, err := r.Cookie("access_token"); err == nil {
			token = c.Value
		}
	}
	if token == "" {
		return contracts.UserIdentity{}, errors.New("missing token")
	}
	return s.verifier.Verify(ctx, token)
}

func (s *Server) readLoop(client *clientConnection, rec contracts.RoomRecord, session contracts.SessionRecord, playerID string) {
	lastCacheRefresh := time.Now().UTC()
	done := make(chan struct{})
	_ = client.conn.SetReadDeadline(time.Now().Add(websocketPongWait))
	client.conn.SetPongHandler(func(string) error {
		return client.conn.SetReadDeadline(time.Now().Add(websocketPongWait))
	})
	go func() {
		ticker := time.NewTicker(websocketPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := client.writeControl(websocket.PingMessage, nil); err != nil {
					_ = client.close()
					return
				}
			}
		}
	}()
	defer func() {
		close(done)
		s.mu.Lock()
		delete(s.conns, session.SessionID)
		s.mu.Unlock()
		_ = client.close()
		disconnect := contracts.RoomCommand{SchemaVersion: contracts.RealtimeSchemaVersion, CommandID: uuid.NewString(), TraceID: uuid.NewString(), RoomID: rec.RoomID, MatchID: rec.CurrentMatchID, GameType: rec.GameType, Epoch: rec.Epoch, UserID: session.UserID, PlayerID: playerID, ConnectionRef: session.ConnectionRef, Type: "disconnect", SentAt: time.Now().UTC()}
		ack, sendErr := s.router.SendRoomCommand(context.Background(), rec.OwnerNode, disconnect, 3*time.Second)
		if roomRouteRefreshRequired(ack, sendErr) && ack.ErrorCode != "stale_match" {
			if latest, err := s.store.GetRoom(context.Background(), rec.RoomID); err == nil {
				if routed, routeErr := s.readyRoute(context.Background(), latest); routeErr == nil {
					if disconnectMatchesCurrentRoute(disconnect, routed) {
						disconnect.GameType = routed.GameType
						disconnect.Epoch = routed.Epoch
						_, _ = s.router.SendRoomCommand(context.Background(), routed.OwnerNode, disconnect, 3*time.Second)
					}
				}
			}
		}
		_ = s.store.MarkPresenceOffline(context.Background(), rec.RoomID, session.SessionID)
		_ = s.store.DeleteSession(context.Background(), session.SessionID)
		_ = s.store.MarkMatchPlayerOffline(context.Background(), rec.CurrentMatchID, playerID, time.Now().UTC())
	}()
	for {
		var msg contracts.ClientCommand
		if err := client.conn.ReadJSON(&msg); err != nil {
			return
		}
		now := time.Now().UTC()
		if now.Sub(lastCacheRefresh) >= time.Minute {
			session.LastSeen = now
			_ = s.store.SetSession(context.Background(), session, 24*time.Hour)
			_ = s.store.SetPresence(context.Background(), contracts.PresenceRecord{
				ConnectionRef: session.ConnectionRef,
				RoomID:        rec.RoomID,
				UserID:        session.UserID,
				PlayerID:      playerID,
				Online:        true,
				JoinedAt:      session.ConnectedAt,
				LastSeen:      now,
			}, 24*time.Hour)
			lastCacheRefresh = now
		}
		if msg.SchemaVersion != contracts.RealtimeSchemaVersion ||
			msg.CommandID == "" ||
			msg.MatchID != rec.CurrentMatchID {
			if err := client.writeJSON(contracts.CommandResult{
				SchemaVersion: contracts.RealtimeSchemaVersion,
				Type:          contracts.MessageTypeCommandResult,
				RoomID:        rec.RoomID,
				MatchID:       rec.CurrentMatchID,
				Seq:           rec.LastAppliedSeq,
				CommandID:     msg.CommandID,
				Accepted:      false,
				ErrorCode:     "invalid_command",
				Message:       "schemaVersion, commandID, and current matchID are required",
			}); err != nil {
				return
			}
			continue
		}
		if !clientCommandTypeAllowed(msg.Type) {
			if err := client.writeJSON(contracts.CommandResult{
				SchemaVersion: contracts.RealtimeSchemaVersion,
				Type:          contracts.MessageTypeCommandResult,
				RoomID:        rec.RoomID,
				MatchID:       rec.CurrentMatchID,
				Seq:           rec.LastAppliedSeq,
				CommandID:     msg.CommandID,
				Accepted:      false,
				ErrorCode:     "unauthorized_command",
				Message:       "runtime commands are reserved for internal services",
			}); err != nil {
				return
			}
			continue
		}
		payload, payloadErr := clientCommandPayload(rec.GameType, msg.Type, msg.Payload)
		if payloadErr != nil {
			if err := client.writeJSON(contracts.CommandResult{
				SchemaVersion: contracts.RealtimeSchemaVersion,
				Type:          contracts.MessageTypeCommandResult,
				RoomID:        rec.RoomID,
				MatchID:       rec.CurrentMatchID,
				Seq:           rec.LastAppliedSeq,
				CommandID:     msg.CommandID,
				Accepted:      false,
				ErrorCode:     "invalid_command",
				Message:       payloadErr.Error(),
			}); err != nil {
				return
			}
			continue
		}
		cmd := contracts.RoomCommand{SchemaVersion: contracts.RealtimeSchemaVersion, CommandID: msg.CommandID, TraceID: uuid.NewString(), RoomID: rec.RoomID, MatchID: rec.CurrentMatchID, GameType: rec.GameType, Epoch: rec.Epoch, UserID: session.UserID, PlayerID: playerID, ConnectionRef: session.ConnectionRef, Type: msg.Type, Payload: payload, SentAt: time.Now().UTC()}
		ack, sendErr := s.router.SendRoomCommand(context.Background(), rec.OwnerNode, cmd, 3*time.Second)
		if roomRouteRefreshRequired(ack, sendErr) {
			previousOwner := rec.OwnerNode
			previousEpoch := rec.Epoch
			latest, lookupErr := s.store.GetRoom(context.Background(), rec.RoomID)
			if lookupErr != nil {
				observability.Warn("room route refresh failed", "roomID", rec.RoomID, "error", lookupErr)
				if err := client.writeJSON(commandResult(rec, cmd.CommandID, contracts.CommandAck{}, lookupErr)); err != nil {
					return
				}
				continue
			}
			routed, routeErr := s.readyRoute(context.Background(), latest)
			if routeErr != nil {
				if err := client.writeJSON(commandResult(rec, cmd.CommandID, contracts.CommandAck{}, routeErr)); err != nil {
					return
				}
				continue
			}
			rec = routed
			cmd.GameType = rec.GameType
			cmd.MatchID = rec.CurrentMatchID
			cmd.Epoch = rec.Epoch
			ack, sendErr = s.router.SendRoomCommand(context.Background(), rec.OwnerNode, cmd, 3*time.Second)
			observability.Info("room route refreshed", "roomID", rec.RoomID, "previousOwner", previousOwner, "ownerNode", rec.OwnerNode, "previousEpoch", previousEpoch, "epoch", rec.Epoch)
		}
		observability.Info("route command", "nodeID", s.nodeID, "roomID", rec.RoomID, "ownerNode", rec.OwnerNode, "epoch", rec.Epoch, "sessionID", session.SessionID, "playerID", playerID, "commandID", cmd.CommandID, "type", cmd.Type)
		_ = s.store.SaveRuntimeEvent(context.Background(), contracts.RuntimeEvent{
			RoomID:    rec.RoomID,
			TraceID:   cmd.TraceID,
			NodeID:    s.nodeID,
			Component: "gateway",
			Event:     "route_command",
			Message:   "gateway routed client command to room owner",
			Payload: map[string]any{
				"ownerNode": rec.OwnerNode,
				"epoch":     rec.Epoch,
				"sessionID": session.SessionID,
				"playerID":  playerID,
				"commandID": cmd.CommandID,
				"type":      cmd.Type,
			},
		})
		if sendErr != nil {
			observability.Warn("send room command failed", "roomID", rec.RoomID, "error", sendErr)
		} else if !ack.Accepted {
			observability.Warn("room command rejected", "roomID", rec.RoomID, "errorCode", ack.ErrorCode, "message", ack.Message)
		}
		if err := client.writeJSON(commandResult(rec, cmd.CommandID, ack, sendErr)); err != nil {
			return
		}
	}
}

func (s *Server) readyRoute(ctx context.Context, rec contracts.RoomRecord) (contracts.RoomRecord, error) {
	if s.owners == nil {
		return contracts.RoomRecord{}, errors.New("room owner resolver is unavailable")
	}
	owner, err := s.owners.ReadyRoomOwner(ctx, rec.RoomID)
	if err != nil {
		return contracts.RoomRecord{}, err
	}
	if owner.OwnerNode == "" || owner.Epoch <= 0 {
		return contracts.RoomRecord{}, errors.New("room owner is not ready")
	}
	if owner.Epoch < rec.Epoch {
		return contracts.RoomRecord{}, errors.New("ready room route is stale")
	}
	rec.OwnerNode = owner.OwnerNode
	rec.Epoch = owner.Epoch
	rec.LeaseExpiresAt = owner.LeaseExpiresAt
	return rec, nil
}

func clientCommandTypeAllowed(commandType string) bool {
	return commandType != "" && !strings.HasPrefix(commandType, "runtime_")
}

func clientCommandPayload(gameType, commandType string, payload any) (any, error) {
	root, ok := payload.(map[string]any)
	if !ok {
		return nil, errors.New("command payload must be a generated union object")
	}
	group := gameType
	variant := ""
	switch commandType {
	case "match_ready":
		group, variant = "common", "matchReady"
	case "match_add_ai", "match_seat":
		group, variant = "common", "matchSeat"
	case "match_remove_player":
		group, variant = "common", "removePlayer"
	case "room_update_settings":
		group, variant = "common", "roomSettings"
	case "match_begin", "game_ready", "game_restart_game":
		group, variant = "common", "empty"
	case "game_place_tile":
		variant = "placeTile"
	case "game_create_company":
		variant = "createCompany"
	case "game_merging_selection":
		variant = "mergingSelection"
	case "game_merging_settle":
		variant = "mergingSettle"
	case "game_buy_stock":
		variant = "buyStock"
	case "game_end_vote":
		variant = "endVote"
	case "game_get_card":
		variant = "getCard"
	case "game_guess_card":
		variant = "guessCard"
	case "game_set_card":
		variant = "setCard"
	case "game_initialize_hand":
		variant = "initializeHand"
	case "game_get_gem":
		variant = "getGem"
	case "game_buy_card":
		variant = "buyCard"
	case "game_preserve_card":
		variant = "preserveCard"
	case "game_place_ring":
		variant = "placeRing"
	case "game_move_ring":
		variant = "moveRing"
	case "game_remove_row":
		variant = "removeRow"
	case "game_remove_ring":
		variant = "removeRing"
	case "game_place_disc":
		variant = "placeDisc"
	case "game_pass":
		variant = "pass"
	case "game_resign":
		variant = "resign"
	default:
		return nil, fmt.Errorf("unsupported command type %q", commandType)
	}
	groupPayload, ok := root[group].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("command payload group %q is required", group)
	}
	value, ok := groupPayload[variant]
	if !ok {
		return nil, fmt.Errorf("command payload variant %q is required", variant)
	}
	if variant == "empty" {
		return nil, nil
	}
	if gameType == contracts.GameSplendor && variant == "buyCard" {
		card, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("buyCard payload is invalid")
		}
		cardID, ok := card["cardID"]
		if !ok {
			return nil, errors.New("buyCard.cardID is required")
		}
		return cardID, nil
	}
	return value, nil
}

func commandResult(rec contracts.RoomRecord, commandID string, ack contracts.CommandAck, err error) contracts.CommandResult {
	if ack.RoomID == "" {
		ack.RoomID = rec.RoomID
	}
	if ack.MatchID == "" {
		ack.MatchID = rec.CurrentMatchID
	}
	if ack.Seq == 0 {
		ack.Seq = rec.LastAppliedSeq
	}
	result := contracts.CommandResult{
		SchemaVersion: contracts.RealtimeSchemaVersion,
		Type:          contracts.MessageTypeCommandResult,
		RoomID:        ack.RoomID,
		MatchID:       ack.MatchID,
		Seq:           ack.Seq,
		CommandID:     commandID,
		Accepted:      ack.Accepted,
		ErrorCode:     ack.ErrorCode,
		Message:       ack.Message,
	}
	if err != nil {
		result.ErrorCode = "command_transport_unavailable"
		result.Message = err.Error()
		result.Retryable = true
		return result
	}
	result.Retryable = !ack.Accepted && retryableCommandError(ack.ErrorCode)
	return result
}

func retryableCommandError(errorCode string) bool {
	switch errorCode {
	case "stale_epoch", "stale_match", "room_error", "roomhost_draining", "command_log_unavailable":
		return true
	default:
		return false
	}
}

func roomRouteRefreshRequired(ack contracts.CommandAck, err error) bool {
	if err != nil {
		return true
	}
	if ack.Accepted {
		return false
	}
	return ack.ErrorCode == "stale_epoch" ||
		ack.ErrorCode == "stale_match" ||
		ack.ErrorCode == "room_error" ||
		ack.ErrorCode == "roomhost_draining"
}

func disconnectMatchesCurrentRoute(disconnect contracts.RoomCommand, room contracts.RoomRecord) bool {
	return disconnect.RoomID != "" &&
		disconnect.RoomID == room.RoomID &&
		disconnect.MatchID != "" &&
		disconnect.MatchID == room.CurrentMatchID
}

func (s *Server) deliver(ctx context.Context, out contracts.OutboundMessage) {
	s.mu.RLock()
	conn := s.conns[out.TargetSessionIDValue()]
	s.mu.RUnlock()
	if conn == nil {
		return
	}
	if err := conn.writeJSON(out.Payload); err != nil {
		observability.Warn("write websocket failed", "sessionID", out.TargetSessionIDValue(), "error", err)
		return
	}
	observability.Info("deliver websocket", "nodeID", s.nodeID, "roomID", out.RoomID, "sessionID", out.TargetSessionIDValue(), "playerID", out.TargetPlayerID, "messageID", out.MessageID, "seq", out.Seq)
	_ = s.store.SaveRuntimeEvent(ctx, contracts.RuntimeEvent{
		RoomID:    out.RoomID,
		TraceID:   out.TraceID,
		NodeID:    s.nodeID,
		Component: "gateway",
		Event:     "deliver_websocket",
		Message:   "gateway delivered message to websocket",
		Payload: map[string]any{
			"sessionID": out.TargetSessionIDValue(),
			"playerID":  out.TargetPlayerID,
			"messageID": out.MessageID,
			"seq":       out.Seq,
		},
	})
	if out.Reliable {
		_ = s.store.MarkOutboundDelivered(ctx, out.MessageID)
	}
	if out.CloseAfter {
		_ = conn.close()
	}
}

func (s *Server) ActiveSessions() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.conns)
}

func (s *Server) backfill(ctx context.Context, conn *clientConnection, roomID, matchID, sessionID, playerID string, afterSeq int64) {
	messages, err := s.store.ListOutboundMessages(ctx, roomID, matchID, playerID, afterSeq, 200)
	if err != nil {
		observability.Warn("backfill failed", "roomID", roomID, "playerID", playerID, "afterSeq", afterSeq, "error", err)
		return
	}
	if len(messages) == 0 {
		minSeq, maxSeq, err := s.store.OutboundSeqRange(ctx, roomID, matchID, playerID)
		if err == nil && afterSeq+1 < minSeq {
			if !s.sendLatestPlayerView(ctx, conn, roomID, matchID, playerID) {
				_ = conn.writeJSON(contracts.ErrorMessage{Type: contracts.MessageTypeError, Code: "BACKFILL_UNAVAILABLE", Message: "full room snapshot is unavailable"})
			}
		}
		if err == nil && afterSeq > maxSeq {
			observability.Warn("client lastSeq is ahead of server", "roomID", roomID, "playerID", playerID, "lastSeq", afterSeq, "maxSeq", maxSeq)
		}
		return
	}
	observability.Info("backfill messages", "nodeID", s.nodeID, "roomID", roomID, "sessionID", sessionID, "playerID", playerID, "afterSeq", afterSeq, "count", len(messages))
	_ = s.store.SaveRuntimeEvent(ctx, contracts.RuntimeEvent{
		RoomID:    roomID,
		NodeID:    s.nodeID,
		Component: "gateway",
		Event:     "backfill_messages",
		Message:   "gateway backfilled reliable messages",
		Payload: map[string]any{
			"sessionID": sessionID,
			"playerID":  playerID,
			"afterSeq":  afterSeq,
			"count":     len(messages),
		},
	})
	for _, msg := range messages {
		if err := conn.writeJSON(msg.Payload); err != nil {
			observability.Warn("backfill write failed", "roomID", roomID, "playerID", playerID, "messageID", msg.MessageID, "error", err)
			return
		}
		if msg.Reliable {
			_ = s.store.MarkOutboundDelivered(ctx, msg.MessageID)
		}
	}
	if len(messages) == 200 {
		last := messages[len(messages)-1]
		latest, err := s.store.LatestOutboundMessage(ctx, roomID, matchID, playerID)
		if err == nil && latest.Seq > last.Seq {
			if err := conn.writeJSON(latest.Payload); err == nil && latest.Reliable {
				_ = s.store.MarkOutboundDelivered(ctx, latest.MessageID)
			}
		}
	}
}

func (s *Server) sendLatestPlayerView(ctx context.Context, conn *clientConnection, roomID, matchID, playerID string) bool {
	latest, err := s.store.LatestOutboundMessage(ctx, roomID, matchID, playerID)
	if err != nil {
		observability.Warn("latest player view unavailable", "roomID", roomID, "playerID", playerID, "error", err)
		return false
	}
	if err := conn.writeJSON(latest.Payload); err != nil {
		observability.Warn("latest player view write failed", "roomID", roomID, "playerID", playerID, "error", err)
		return false
	}
	if latest.Reliable {
		_ = s.store.MarkOutboundDelivered(ctx, latest.MessageID)
	}
	observability.Info("latest player view backfilled", "roomID", roomID, "playerID", playerID, "seq", latest.Seq)
	return true
}

func queryInt64(r *http.Request, key string, fallback int64) int64 {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
