package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nciyuan9264/game-platform/internal/authclient"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/coordination"
	"github.com/nciyuan9264/game-platform/internal/observability"
	"github.com/nciyuan9264/game-platform/internal/store"
)

var (
	errNoRoomHost             = errors.New("no roomhost available")
	errRoomCreationInProgress = errors.New("room creation is already in progress")
	errUnknownGame            = errors.New("unknown game")
	errGameDisabled           = errors.New("game is disabled for new rooms")
)

const (
	roomCreationCompensationTimeout = 5 * time.Second
	roomInitializationTimeout       = 15 * time.Second
	roomIDSuffixLength              = 12
	roomIDAlphabet                  = "0123456789abcdefghjkmnpqrstvwxyz"
)

type roomCommandSender interface {
	SendRoomCommand(ctx context.Context, ownerNode string, cmd contracts.RoomCommand, timeout time.Duration) (contracts.CommandAck, error)
}

type commandLogReader interface {
	ListCommandLog(roomID string, afterSeq int64) ([]contracts.CommandLogRecord, error)
}

type gameCatalog interface {
	GetGameCatalogEntry(context.Context, string) (contracts.GameCatalogEntry, error)
	GetDefaultGameCatalogEntry(context.Context) (contracts.GameCatalogEntry, error)
}

type controlStore interface {
	Increment(context.Context, string) (int64, error)
	ListNodes(context.Context, string) ([]contracts.NodeInfo, error)
	ListRooms(context.Context, string) ([]contracts.RoomRecord, error)
	RoomCountsByOwner(context.Context, time.Time) (map[string]int, map[string]int, error)
	ListAllPresence(context.Context) ([]contracts.PresenceRecord, error)
	ListMatches(context.Context, string, string, string, int) ([]contracts.MatchRecord, error)
	ListSessions(context.Context) ([]contracts.SessionRecord, error)
	ListRuntimeEvents(context.Context, string, int64, int) ([]contracts.RuntimeEvent, error)
	GetRoom(context.Context, string) (contracts.RoomRecord, error)
	GetMatch(context.Context, string) (contracts.MatchRecord, error)
	ReserveRoomCreation(context.Context, contracts.RoomRecord, string, string, int) (contracts.RoomRecord, bool, error)
	ReserveNextMatch(context.Context, string, string, uint64) (contracts.RoomRecord, contracts.MatchRecord, bool, error)
	SetRoom(context.Context, contracts.RoomRecord) error
	DeleteRoomCreation(context.Context, string) error
	UpdateRoom(context.Context, string, func(*contracts.RoomRecord) error) (contracts.RoomRecord, error)
	SaveRuntimeEvent(context.Context, contracts.RuntimeEvent) error
	CleanupOperationalData(context.Context, time.Time) error
}

type roomCoordinator interface {
	InitRoomOwner(context.Context, contracts.RoomRecord, time.Duration) (coordination.RoomOwner, error)
	DeleteRoomOwner(context.Context, string, string, int64) error
}

type Server struct {
	store                  controlStore
	coordinator            roomCoordinator
	gameCatalog            gameCatalog
	roomCommands           roomCommandSender
	commandLogs            commandLogReader
	adminAuth              authclient.TokenVerifier
	schedulerPolicy        string
	schedulerPendingWindow time.Duration
}

func NewServer(store controlStore, coordinator roomCoordinator) *Server {
	return NewServerWithConfig(store, coordinator, Config{SchedulerPolicy: SchedulerLoadAware})
}

type Config struct {
	SchedulerPolicy string
	PendingWindow   time.Duration
	GameCatalog     gameCatalog
	RoomCommands    roomCommandSender
	CommandLogs     commandLogReader
	AdminAuth       authclient.TokenVerifier
}

const (
	SchedulerLoadAware     = "load_aware"
	SchedulerLeastAssigned = "least_assigned"
	SchedulerRoundRobin    = "round_robin"
)

const defaultSchedulerPendingWindow = 2 * time.Minute

func NewServerWithConfig(store controlStore, coordinator roomCoordinator, cfg Config) *Server {
	if cfg.PendingWindow <= 0 {
		cfg.PendingWindow = defaultSchedulerPendingWindow
	}
	if cfg.GameCatalog == nil {
		if catalog, ok := store.(gameCatalog); ok {
			cfg.GameCatalog = catalog
		}
	}
	if cfg.CommandLogs == nil {
		if reader, ok := cfg.RoomCommands.(commandLogReader); ok {
			cfg.CommandLogs = reader
		}
	}
	return &Server{
		store:                  store,
		coordinator:            coordinator,
		gameCatalog:            cfg.GameCatalog,
		roomCommands:           cfg.RoomCommands,
		commandLogs:            cfg.CommandLogs,
		adminAuth:              cfg.AdminAuth,
		schedulerPolicy:        normalizeSchedulerPolicy(cfg.SchedulerPolicy),
		schedulerPendingWindow: cfg.PendingWindow,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("/platform/instances", s.requireAdmin(http.HandlerFunc(s.instances)))
	mux.Handle("/platform/events", s.requireAdmin(http.HandlerFunc(s.events)))
	mux.Handle("/platform/command-log", s.requireAdmin(http.HandlerFunc(s.commandLog)))
	mux.Handle("/platform/topology", s.requireAdmin(http.HandlerFunc(s.topology)))
	return mux
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if cookie, err := r.Cookie("access_token"); err == nil && cookie.Value != "" {
			token = cookie.Value
		}
		if token == "" || s.adminAuth == nil {
			writeJSON(w, http.StatusUnauthorized, contracts.APIResponse{StatusCode: http.StatusUnauthorized, Error: "unauthorized", Message: "admin authentication is required"})
			return
		}
		result, err := s.adminAuth.VerifyToken(r.Context(), authclient.VerifyTokenRequest{AccessToken: token, RequiredAudience: "game-platform"})
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, contracts.APIResponse{StatusCode: http.StatusServiceUnavailable, Error: "auth_unavailable", Message: "admin authentication is unavailable"})
			return
		}
		if !result.Valid {
			writeJSON(w, http.StatusUnauthorized, contracts.APIResponse{StatusCode: http.StatusUnauthorized, Error: "unauthorized", Message: "invalid access token"})
			return
		}
		for _, role := range result.User.Roles {
			if role == "admin" {
				next.ServeHTTP(w, r)
				return
			}
		}
		writeJSON(w, http.StatusForbidden, contracts.APIResponse{StatusCode: http.StatusForbidden, Error: "forbidden", Message: "admin role is required"})
	})
}

type createRoomRequest struct {
	GameType           string `json:"gameType"`
	OwnerUserID        string `json:"ownerUserID"`
	OwnerPlayerID      string `json:"ownerPlayerID"`
	MaxPlayers         int    `json:"maxPlayers"`
	Visibility         string `json:"visibility"`
	TurnTimeoutSeconds int    `json:"turnTimeoutSeconds,omitempty"`
	PlayerCount        int    `json:"playerCount,omitempty"`
	RequestID          string `json:"requestID"`
}

func (s *Server) createRoomRecord(ctx context.Context, req createRoomRequest) (contracts.RoomRecord, error) {
	def, err := s.resolveGameDefinition(ctx, req.GameType)
	if err != nil {
		return contracts.RoomRecord{}, err
	}
	req.GameType = def.GameType
	if req.MaxPlayers <= 0 {
		req.MaxPlayers = def.DefaultMaxPlayers
	}
	if req.Visibility == "" {
		req.Visibility = contracts.VisibilityPublic
	}
	if req.Visibility != contracts.VisibilityPublic && req.Visibility != contracts.VisibilityPrivate {
		return contracts.RoomRecord{}, errors.New("invalid room visibility")
	}
	if req.RequestID == "" {
		req.RequestID = uuid.NewString()
	}
	now := time.Now().UTC()
	roomID, err := newRoomID(req.GameType)
	if err != nil {
		return contracts.RoomRecord{}, err
	}
	rec := contracts.RoomRecord{
		RoomID:             roomID,
		GameType:           req.GameType,
		Visibility:         req.Visibility,
		TurnTimeoutSeconds: req.TurnTimeoutSeconds,
		OwnerUserID:        req.OwnerUserID,
		OwnerPlayerID:      req.OwnerPlayerID,
		Epoch:              1,
		Status:             contracts.RoomStatusCreating,
		MaxPlayers:         req.MaxPlayers,
		PlayerCount:        req.PlayerCount,
		EngineVersion:      def.ActiveEngineVersion,
		NextRoundNo:        1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	requestHash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%s\x00%s\x00%d\x00%s\x00%d",
		rec.GameType, rec.OwnerUserID, rec.OwnerPlayerID,
		rec.MaxPlayers, rec.Visibility, rec.TurnTimeoutSeconds,
	))))
	rec, existing, err := s.store.ReserveRoomCreation(ctx, rec, req.RequestID, requestHash, contracts.MaxOwnedRooms)
	if err != nil {
		return contracts.RoomRecord{}, err
	}
	if visibilityStore, ok := s.store.(interface {
		RoomVisibility(context.Context, string) (string, error)
		SetRoomVisibility(context.Context, string, string) error
	}); ok {
		if existing {
			rec.Visibility, err = visibilityStore.RoomVisibility(ctx, rec.RoomID)
			if err != nil {
				return contracts.RoomRecord{}, err
			}
		} else if err := visibilityStore.SetRoomVisibility(ctx, rec.RoomID, rec.Visibility); err != nil {
			return contracts.RoomRecord{}, s.compensateRoomCreation(rec, err)
		}
	}
	if existing {
		if rec.Status != contracts.RoomStatusCreating {
			return rec, nil
		}
		if time.Since(rec.UpdatedAt) < 30*time.Second {
			return contracts.RoomRecord{}, errRoomCreationInProgress
		}
	}

	nodes, err := s.store.ListNodes(ctx, contracts.NodeTypeRoomHost)
	if err != nil || len(nodes) == 0 {
		if err == nil {
			err = errNoRoomHost
		}
		return contracts.RoomRecord{}, s.compensateRoomCreation(rec, err)
	}
	nodes = compatibleRoomHosts(nodes, def.GameType, def.ActiveEngineVersion)
	if len(nodes) == 0 {
		err := fmt.Errorf("%w: no roomhost supports %s@%s", errNoRoomHost, def.GameType, def.ActiveEngineVersion)
		return contracts.RoomRecord{}, s.compensateRoomCreation(rec, err)
	}
	selection, err := s.selectRoomHost(ctx, nodes)
	if err != nil {
		return contracts.RoomRecord{}, s.compensateRoomCreation(rec, err)
	}
	rec.OwnerNode = selection.OwnerNode
	rec.LeaseExpiresAt = time.Now().UTC().Add(coordination.DefaultRoomLease)
	if err := s.store.SetRoom(ctx, rec); err != nil {
		return contracts.RoomRecord{}, s.compensateRoomCreation(rec, fmt.Errorf("persist owner reservation: %w", err))
	}
	owner, err := s.coordinator.InitRoomOwner(ctx, rec, coordination.DefaultRoomLease)
	if err != nil {
		return contracts.RoomRecord{}, s.compensateRoomCreation(rec, fmt.Errorf("initialize room owner: %w", err))
	}
	rec.OwnerNode = owner.OwnerNode
	rec.Epoch = owner.Epoch
	rec.LeaseExpiresAt = owner.LeaseExpiresAt
	if err := s.store.SetRoom(ctx, rec); err != nil {
		return contracts.RoomRecord{}, s.compensateRoomCreation(rec, fmt.Errorf("persist room: %w", err))
	}
	randomSeed, err := newMatchSeed()
	if err != nil {
		return contracts.RoomRecord{}, s.compensateRoomCreation(rec, fmt.Errorf("generate initial match seed: %w", err))
	}
	rec, match, _, err := s.store.ReserveNextMatch(ctx, rec.RoomID, req.RequestID+":round-1", randomSeed)
	if err != nil {
		return contracts.RoomRecord{}, s.compensateRoomCreation(rec, fmt.Errorf("reserve initial match: %w", err))
	}
	rec.Status = contracts.RoomStatusOpen
	rec.Visibility = req.Visibility
	if err := s.store.SetRoom(ctx, rec); err != nil {
		return contracts.RoomRecord{}, s.compensateRoomCreation(rec, fmt.Errorf("activate room: %w", err))
	}
	if err := s.createRoomActor(ctx, rec, match, contracts.RuntimeCommandRoomCreate); err != nil {
		return contracts.RoomRecord{}, s.compensateRoomCreation(rec, fmt.Errorf("create room actor: %w", err))
	}
	_ = s.store.SaveRuntimeEvent(ctx, contracts.RuntimeEvent{
		RoomID:    rec.RoomID,
		TraceID:   rec.RoomID,
		NodeID:    "control-plane",
		Component: "control-plane",
		Event:     "room_created",
		Message:   "room created and owner selected",
		Payload: map[string]any{
			"type":              "create_room",
			"gameType":          rec.GameType,
			"ownerNode":         rec.OwnerNode,
			"epoch":             rec.Epoch,
			"maxPlayers":        rec.MaxPlayers,
			"schedulerPolicy":   selection.Policy,
			"schedulerReason":   selection.Reason,
			"schedulerCounter":  selection.Counter,
			"schedulerWindow":   selection.PendingWindow.String(),
			"schedulerSelected": selection.OwnerNode,
			"candidates":        selection.Candidates,
		},
	})
	observability.Info("room created", "roomID", rec.RoomID, "gameType", rec.GameType, "ownerNode", rec.OwnerNode, "epoch", rec.Epoch, "maxPlayers", rec.MaxPlayers, "schedulerPolicy", selection.Policy, "schedulerReason", selection.Reason)
	return rec, nil
}

func (s *Server) startNextMatch(ctx context.Context, roomID, requestID string) (contracts.RoomRecord, contracts.MatchRecord, error) {
	if requestID == "" {
		requestID = uuid.NewString()
	}
	seed, err := newMatchSeed()
	if err != nil {
		return contracts.RoomRecord{}, contracts.MatchRecord{}, err
	}
	room, match, _, err := s.store.ReserveNextMatch(ctx, roomID, requestID, seed)
	if err != nil {
		return contracts.RoomRecord{}, contracts.MatchRecord{}, err
	}
	if match.Status == contracts.MatchStatusPreparing {
		if err := s.createRoomActor(ctx, room, match, contracts.RuntimeCommandMatchCreate); err != nil {
			return contracts.RoomRecord{}, contracts.MatchRecord{}, fmt.Errorf("create match runtime: %w", err)
		}
	}
	_ = s.store.SaveRuntimeEvent(ctx, contracts.RuntimeEvent{
		RoomID:    room.RoomID,
		MatchID:   match.MatchID,
		TraceID:   requestID,
		NodeID:    "control-plane",
		Component: "control-plane",
		Event:     "match_created",
		Message:   "control plane created a new room match",
		Payload: map[string]any{
			"roundNo": match.RoundNo,
			"owner":   room.OwnerNode,
		},
	})
	return room, match, nil
}

func compatibleRoomHosts(nodes []contracts.NodeInfo, gameType, version string) []contracts.NodeInfo {
	compatible := make([]contracts.NodeInfo, 0, len(nodes))
	for _, node := range nodes {
		if node.Status == contracts.NodeStatusReady && node.GameVersions[gameType] == version {
			compatible = append(compatible, node)
		}
	}
	return compatible
}

func newRoomID(gameType string) (string, error) {
	random := make([]byte, roomIDSuffixLength)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate room id: %w", err)
	}
	for i := range random {
		random[i] = roomIDAlphabet[int(random[i])&31]
	}
	return gameType + "_" + string(random), nil
}

func (s *Server) compensateRoomCreation(rec contracts.RoomRecord, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), roomCreationCompensationTimeout)
	defer cancel()

	var cleanupErrors []error
	if s.coordinator != nil {
		if err := s.coordinator.DeleteRoomOwner(ctx, rec.RoomID, rec.OwnerNode, rec.Epoch); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete room owner: %w", err))
		}
	}
	if s.store != nil {
		if err := s.store.DeleteRoomCreation(ctx, rec.RoomID); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete room projections: %w", err))
		}
	}
	if len(cleanupErrors) == 0 {
		observability.Warn("room creation compensated", "roomID", rec.RoomID, "ownerNode", rec.OwnerNode, "epoch", rec.Epoch, "cause", cause)
		return cause
	}
	cleanupErr := errors.Join(cleanupErrors...)
	observability.Error("room creation compensation failed", "roomID", rec.RoomID, "ownerNode", rec.OwnerNode, "epoch", rec.Epoch, "cause", cause, "error", cleanupErr)
	return errors.Join(cause, fmt.Errorf("room creation compensation failed: %w", cleanupErr))
}

func newMatchSeed() (uint64, error) {
	var raw [8]byte
	for {
		if _, err := rand.Read(raw[:]); err != nil {
			return 0, err
		}
		seed := binary.LittleEndian.Uint64(raw[:])
		if seed != 0 {
			return seed, nil
		}
	}
}

func (s *Server) createRoomActor(ctx context.Context, rec contracts.RoomRecord, match contracts.MatchRecord, commandType string) error {
	if s.roomCommands == nil {
		return nil
	}
	cmd := contracts.RoomCommand{
		SchemaVersion: 1,
		CommandID:     uuid.NewString(),
		TraceID:       rec.RoomID,
		RoomID:        rec.RoomID,
		MatchID:       match.MatchID,
		GameType:      rec.GameType,
		Epoch:         rec.Epoch,
		UserID:        rec.OwnerUserID,
		PlayerID:      rec.OwnerPlayerID,
		Type:          commandType,
		SentAt:        time.Now().UTC(),
	}
	ack, err := s.roomCommands.SendRoomCommand(ctx, rec.OwnerNode, cmd, roomInitializationTimeout)
	if err != nil {
		return err
	}
	if !ack.Accepted {
		return fmt.Errorf("roomhost create rejected: %s", ack.Message)
	}
	return nil
}

func (s *Server) resolveGameDefinition(ctx context.Context, gameType string) (contracts.GameCatalogEntry, error) {
	if s.gameCatalog == nil {
		return contracts.GameCatalogEntry{}, errors.New("game catalog is unavailable")
	}
	var (
		entry contracts.GameCatalogEntry
		err   error
	)
	if gameType == "" {
		entry, err = s.gameCatalog.GetDefaultGameCatalogEntry(ctx)
	} else {
		entry, err = s.gameCatalog.GetGameCatalogEntry(ctx, gameType)
	}
	if errors.Is(err, store.ErrGameCatalogNotFound) {
		return contracts.GameCatalogEntry{}, fmt.Errorf("%w: %s", errUnknownGame, gameType)
	}
	if err != nil {
		return contracts.GameCatalogEntry{}, err
	}
	if !entry.EnabledForNewRooms {
		return contracts.GameCatalogEntry{}, fmt.Errorf("%w: %s", errGameDisabled, entry.GameType)
	}
	return entry, nil
}

type roomHostCandidate struct {
	NodeID        string `json:"nodeID"`
	AssignedRooms int    `json:"assignedRooms"`
	ActiveRooms   int    `json:"activeRooms"`
	PendingRooms  int    `json:"pendingRooms"`
	Score         int    `json:"score"`
	Selected      bool   `json:"selected"`
}

type roomHostSelection struct {
	OwnerNode     string              `json:"ownerNode"`
	Policy        string              `json:"policy"`
	Reason        string              `json:"reason"`
	Counter       int64               `json:"counter,omitempty"`
	PendingWindow time.Duration       `json:"-"`
	Candidates    []roomHostCandidate `json:"candidates"`
}

func (s *Server) selectRoomHost(ctx context.Context, nodes []contracts.NodeInfo) (roomHostSelection, error) {
	pendingWindow := s.schedulerPendingWindow
	cutoff := time.Now().UTC().Add(-pendingWindow)
	assigned, pending, err := s.store.RoomCountsByOwner(ctx, cutoff)
	if err != nil {
		return roomHostSelection{}, err
	}
	candidates := make([]roomHostCandidate, 0, len(nodes))
	for _, node := range nodes {
		candidates = append(candidates, roomHostCandidate{
			NodeID:        node.NodeID,
			AssignedRooms: assigned[node.NodeID],
			ActiveRooms:   node.ActiveRooms,
			PendingRooms:  pending[node.NodeID],
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].NodeID < candidates[j].NodeID
	})
	if len(candidates) == 0 {
		return roomHostSelection{}, errNoRoomHost
	}
	switch s.schedulerPolicy {
	case SchedulerLoadAware:
		return selectRoomHostLoadAware(candidates, pendingWindow), nil
	case SchedulerRoundRobin:
		return s.selectRoomHostRoundRobin(ctx, candidates)
	default:
		return selectRoomHostLeastAssigned(candidates, SchedulerLeastAssigned, "selected lowest assignedRooms+activeRooms score"), nil
	}
}

func selectRoomHostLoadAware(candidates []roomHostCandidate, pendingWindow time.Duration) roomHostSelection {
	for i := range candidates {
		candidates[i].Score = candidates[i].ActiveRooms + candidates[i].PendingRooms
	}
	sorted := append([]roomHostCandidate(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Score != sorted[j].Score {
			return sorted[i].Score < sorted[j].Score
		}
		if sorted[i].ActiveRooms != sorted[j].ActiveRooms {
			return sorted[i].ActiveRooms < sorted[j].ActiveRooms
		}
		if sorted[i].PendingRooms != sorted[j].PendingRooms {
			return sorted[i].PendingRooms < sorted[j].PendingRooms
		}
		return sorted[i].NodeID < sorted[j].NodeID
	})
	selected := sorted[0].NodeID
	for i := range candidates {
		candidates[i].Selected = candidates[i].NodeID == selected
	}
	return roomHostSelection{
		OwnerNode:     selected,
		Policy:        SchedulerLoadAware,
		Reason:        "selected lowest activeRooms+recentPendingRooms score; historical assignedRooms are observability only",
		PendingWindow: pendingWindow,
		Candidates:    candidates,
	}
}

func selectRoomHostLeastAssigned(candidates []roomHostCandidate, policy, reason string) roomHostSelection {
	for i := range candidates {
		candidates[i].Score = candidates[i].AssignedRooms + candidates[i].ActiveRooms
	}
	sorted := append([]roomHostCandidate(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Score != sorted[j].Score {
			return sorted[i].Score < sorted[j].Score
		}
		if sorted[i].AssignedRooms != sorted[j].AssignedRooms {
			return sorted[i].AssignedRooms < sorted[j].AssignedRooms
		}
		if sorted[i].ActiveRooms != sorted[j].ActiveRooms {
			return sorted[i].ActiveRooms < sorted[j].ActiveRooms
		}
		return sorted[i].NodeID < sorted[j].NodeID
	})
	selected := sorted[0].NodeID
	for i := range candidates {
		candidates[i].Selected = candidates[i].NodeID == selected
	}
	return roomHostSelection{
		OwnerNode:  selected,
		Policy:     policy,
		Reason:     reason,
		Candidates: candidates,
	}
}

func (s *Server) selectRoomHostRoundRobin(ctx context.Context, candidates []roomHostCandidate) (roomHostSelection, error) {
	counter, err := s.store.Increment(ctx, "scheduler:roomhost:round_robin")
	if err != nil {
		selection := selectRoomHostLeastAssigned(candidates, SchedulerLeastAssigned, "round_robin counter unavailable; fell back to least_assigned")
		return selection, nil
	}
	idx := int((counter - 1) % int64(len(candidates)))
	selected := candidates[idx].NodeID
	for i := range candidates {
		candidates[i].Selected = candidates[i].NodeID == selected
	}
	return roomHostSelection{
		OwnerNode:  selected,
		Policy:     SchedulerRoundRobin,
		Reason:     fmt.Sprintf("selected by shared Redis round-robin counter %d over %d roomhosts", counter, len(candidates)),
		Counter:    counter,
		Candidates: candidates,
	}, nil
}

func normalizeSchedulerPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", SchedulerLoadAware, "load", "load-aware", "loadaware":
		return SchedulerLoadAware
	case SchedulerLeastAssigned, "least", "least-assigned":
		return SchedulerLeastAssigned
	case SchedulerRoundRobin, "round-robin", "rr":
		return SchedulerRoundRobin
	default:
		observability.Warn("unknown room scheduler policy; using load_aware", "policy", policy)
		return SchedulerLoadAware
	}
}

func (s *Server) instances(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListNodes(r.Context(), r.URL.Query().Get("type"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, contracts.APIResponse{StatusCode: http.StatusInternalServerError, Error: "store_error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, contracts.APIResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{"instances": nodes}})
}

func natsSubjectCatalog(roomID, roomOwnerNode, gatewayNode string) []contracts.NATSSubjectInfo {
	if roomID == "" {
		roomID = "<roomID>"
	}
	if roomOwnerNode == "" {
		roomOwnerNode = "<roomhost-node>"
	}
	if gatewayNode == "" {
		gatewayNode = "<gateway-node>"
	}
	return []contracts.NATSSubjectInfo{
		{
			Name:        "Room command request",
			Subject:     "room.cmd." + roomOwnerNode,
			Mode:        "Core NATS request/reply",
			Description: "Gateway 将客户端命令按房间 owner 投递给 RoomHost。",
		},
		{
			Name:        "Gateway delivery",
			Subject:     "gateway.deliver." + gatewayNode,
			Mode:        "Core NATS publish/subscribe",
			Description: "RoomHost 将对局广播消息按连接所在 Gateway 投递回去。",
		},
		{
			Name:        "Command replay log",
			Subject:     "cmdlog.<matchID>",
			Mode:        "JetStream stream GAME_COMMANDS",
			Description: "RoomHost 将已应用命令持久化，故障恢复时按 Match 回放。",
		},
		{
			Name:        "Command stream wildcard",
			Subject:     "cmdlog.*",
			Mode:        "JetStream subject",
			Description: "GAME_COMMANDS stream 覆盖的持久化命令主题集合。",
		},
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, contracts.APIResponse{StatusCode: http.StatusMethodNotAllowed, Error: "method_not_allowed"})
		return
	}
	roomID := r.URL.Query().Get("roomID")
	if roomID == "" {
		writeJSON(w, http.StatusBadRequest, contracts.APIResponse{StatusCode: http.StatusBadRequest, Error: "bad_request", Message: "roomID is required"})
		return
	}
	afterID, _ := strconv.ParseInt(r.URL.Query().Get("afterID"), 10, 64)
	events, err := s.store.ListRuntimeEvents(r.Context(), roomID, afterID, 200)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, contracts.APIResponse{StatusCode: http.StatusInternalServerError, Error: "store_error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, contracts.APIResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{"events": events}})
}

func (s *Server) commandLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, contracts.APIResponse{StatusCode: http.StatusMethodNotAllowed, Error: "method_not_allowed"})
		return
	}
	if s.commandLogs == nil {
		writeJSON(w, http.StatusServiceUnavailable, contracts.APIResponse{StatusCode: http.StatusServiceUnavailable, Error: "command_log_unavailable", Message: "command log reader is not configured"})
		return
	}
	roomID := r.URL.Query().Get("roomID")
	matchID := r.URL.Query().Get("matchID")
	if matchID == "" && roomID != "" {
		if s.store == nil {
			matchID = roomID
		} else {
			room, err := s.store.GetRoom(r.Context(), roomID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, contracts.APIResponse{StatusCode: http.StatusNotFound, Error: "not_found", Message: err.Error()})
				return
			}
			matchID = room.CurrentMatchID
		}
	}
	if matchID == "" {
		writeJSON(w, http.StatusBadRequest, contracts.APIResponse{StatusCode: http.StatusBadRequest, Error: "bad_request", Message: "matchID or roomID is required"})
		return
	}
	afterSeq, _ := strconv.ParseInt(r.URL.Query().Get("afterSeq"), 10, 64)
	limit := queryLimit(r, 200, 500)
	commands, err := s.commandLogs.ListCommandLog(matchID, afterSeq)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, contracts.APIResponse{StatusCode: http.StatusInternalServerError, Error: "command_log_error", Message: err.Error()})
		return
	}
	if len(commands) > limit {
		commands = commands[:limit]
	}
	writeJSON(w, http.StatusOK, contracts.APIResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{
		"roomID":   roomID,
		"matchID":  matchID,
		"afterSeq": afterSeq,
		"commands": commands,
	}})
}

func queryLimit(r *http.Request, fallback, max int) int {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		return fallback
	}
	if limit > max {
		return max
	}
	return limit
}

func (s *Server) topology(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, contracts.APIResponse{StatusCode: http.StatusMethodNotAllowed, Error: "method_not_allowed"})
		return
	}
	ctx := r.Context()
	nodes, err := s.store.ListNodes(ctx, "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, contracts.APIResponse{StatusCode: http.StatusInternalServerError, Error: "store_error", Message: err.Error()})
		return
	}
	rooms, err := s.store.ListRooms(ctx, "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, contracts.APIResponse{StatusCode: http.StatusInternalServerError, Error: "store_error", Message: err.Error()})
		return
	}
	matches, err := s.store.ListMatches(ctx, "", "", "", 200)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, contracts.APIResponse{StatusCode: http.StatusInternalServerError, Error: "store_error", Message: err.Error()})
		return
	}
	sessions, err := s.store.ListSessions(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, contracts.APIResponse{StatusCode: http.StatusInternalServerError, Error: "store_error", Message: err.Error()})
		return
	}
	presences, err := s.store.ListAllPresence(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, contracts.APIResponse{StatusCode: http.StatusInternalServerError, Error: "store_error", Message: err.Error()})
		return
	}

	gatewaySessions := map[string][]contracts.SessionRecord{}
	onlineSessionIDs := map[string]bool{}
	for _, presence := range presences {
		if presence.Online {
			onlineSessionIDs[presence.SessionID] = true
		}
	}
	for _, session := range sessions {
		if !onlineSessionIDs[session.SessionID] {
			continue
		}
		gatewaySessions[session.GatewayNode] = append(gatewaySessions[session.GatewayNode], session)
	}
	roomhostRooms := map[string][]contracts.RoomRecord{}
	for _, room := range rooms {
		roomhostRooms[room.OwnerNode] = append(roomhostRooms[room.OwnerNode], room)
	}
	roomPresence := map[string][]contracts.PresenceRecord{}
	for _, presence := range presences {
		roomPresence[presence.RoomID] = append(roomPresence[presence.RoomID], presence)
	}

	writeJSON(w, http.StatusOK, contracts.APIResponse{
		StatusCode: http.StatusOK,
		Message:    "ok",
		Data: map[string]any{
			"nodes":           nodes,
			"rooms":           rooms,
			"matches":         matches,
			"sessions":        sessions,
			"presences":       presences,
			"gatewaySessions": gatewaySessions,
			"roomhostRooms":   roomhostRooms,
			"roomPresence":    roomPresence,
			"natsSubjects":    natsSubjectCatalog("", "", ""),
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
