package roomhost

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/coordination"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/gamelog"
	"github.com/nciyuan9264/game-platform/internal/history"
	"github.com/nciyuan9264/game-platform/internal/observability"
	"github.com/nciyuan9264/game-platform/internal/store"
	"github.com/nciyuan9264/game-platform/internal/transport"
)

const snapshotEverySeq = 5
const aiDecisionTimeout = 2 * time.Second

var ErrEngineVersionMismatch = errors.New("engine version mismatch")

type storeClient interface {
	SaveRuntimeEvent(context.Context, contracts.RuntimeEvent) error
	GetRoom(context.Context, string) (contracts.RoomRecord, error)
	ListRooms(context.Context, string) ([]contracts.RoomRecord, error)
	GetMatch(context.Context, string) (contracts.MatchRecord, error)
	SetRoom(context.Context, contracts.RoomRecord) error
	SetRoomVisibilityIfOwner(context.Context, string, string, string, int64) error
	SetMatchRuntime(context.Context, string, string, string, int64, int64, int) error
	ClaimMatchRuntime(context.Context, string, string, int64) error
	FinishMatch(context.Context, string, string, string, string, string, int64, int64) error
	ListExpiredRooms(context.Context, time.Time, int) ([]contracts.RoomRecord, error)
	ReserveRoomOwner(context.Context, string, int64, string, time.Time, time.Duration) (contracts.RoomRecord, error)
	VerifyRoomOwnerCandidate(context.Context, string, string, int64) error
	MarkRoomOwnerReady(context.Context, string, string, int64, time.Time) error
	RenewRoomOwnerProjection(context.Context, string, string, int64, time.Time) error
	GetSession(context.Context, string) (contracts.SessionRecord, error)
	ListPresence(context.Context, string) ([]contracts.PresenceRecord, error)
	LatestSnapshot(context.Context, string) (contracts.SnapshotRecord, error)
	SaveSnapshot(context.Context, contracts.SnapshotRecord) error
	SaveSpectatorSnapshot(context.Context, contracts.SpectatorSnapshotRecord) error
	GetCommandAck(context.Context, string, string, string, string) (contracts.CommandAck, error)
	SaveCommandAck(context.Context, contracts.CommandAck) error
	AppendGameLogEvents(context.Context, []contracts.GameLogEvent) error
	SaveOutboundMessage(context.Context, contracts.OutboundMessage) error
	SyncMatchRoster(context.Context, string, []contracts.MatchPlayerRecord) error
	ListMatchPlayers(context.Context, string) ([]contracts.MatchPlayerRecord, error)
	StartHistoryGame(context.Context, history.Game, []history.Player) (int64, error)
	OpenHistoryGame(context.Context, string, string) (int64, int, error)
	ResetOpenHistoryGame(context.Context, string, string) error
	AppendHistoryEvent(context.Context, history.Event) error
	FinishHistoryGame(context.Context, int64, history.FinalizeInput) error
	DiscardHistoryGame(context.Context, int64) error
}

type coordinatorClient interface {
	EnsureRoomOwner(context.Context, contracts.RoomRecord, string, time.Duration) (coordination.RoomOwner, error)
	ClaimExpiredRoom(context.Context, contracts.RoomRecord, string, time.Duration) (coordination.RoomOwner, bool, error)
	RenewRoomOwner(context.Context, string, string, int64, time.Duration) (coordination.RoomOwner, error)
	VerifyRoomOwner(context.Context, string, string, int64) error
	PublishRoomReady(context.Context, string, string, int64) error
	DeleteRoomOwner(context.Context, string, string, int64) error
}

type routerClient interface {
	SubscribeRoomCommands(string, func(context.Context, contracts.RoomCommand) contracts.CommandAck) (*nats.Subscription, error)
	AppendOwnerFence(contracts.CommandLogRecord) (contracts.CommandLogAppendResult, error)
	AppendCommandLog(contracts.CommandLogRecord) (contracts.CommandLogAppendResult, error)
	ListCommandLog(string, int64) ([]contracts.CommandLogRecord, error)
	Deliver(contracts.OutboundMessage) error
}

func aiThinkDelay() time.Duration {
	minMs := envInt("ROOMHOST_AI_THINK_DELAY_MIN_MS", envInt("DAVINCI_AI_THINK_DELAY_MIN_MS", 1500))
	maxMs := envInt("ROOMHOST_AI_THINK_DELAY_MAX_MS", envInt("DAVINCI_AI_THINK_DELAY_MAX_MS", 2500))
	if minMs < 0 {
		minMs = 0
	}
	if maxMs < minMs {
		maxMs = minMs
	}
	if maxMs == minMs {
		return time.Duration(minMs) * time.Millisecond
	}
	return time.Duration(minMs+rand.Intn(maxMs-minMs+1)) * time.Millisecond
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

type Host struct {
	nodeID string
	store  storeClient
	coord  coordinatorClient
	router routerClient
	games  *game.Registry

	mu         sync.Mutex
	rooms      map[string]*RoomActor
	emptySince map[string]time.Time
	draining   bool
}

func NewHost(nodeID string, st storeClient, coord coordinatorClient, router routerClient, games *game.Registry) *Host {
	if games == nil {
		games = game.NewRegistry()
	}
	return &Host{
		nodeID:     nodeID,
		store:      st,
		coord:      coord,
		router:     router,
		games:      games,
		rooms:      map[string]*RoomActor{},
		emptySince: map[string]time.Time{},
	}
}

func (h *Host) Start(ctx context.Context) error {
	_, err := h.router.SubscribeRoomCommands(h.nodeID, h.HandleCommand)
	if err != nil {
		return err
	}
	h.bootstrapOwnedRooms(ctx)
	go h.recoveryLoop(ctx)
	go h.leaseLoop(ctx)
	go h.cleanupLoop(ctx)
	go h.projectionRetryLoop(ctx)
	return nil
}

func (h *Host) bootstrapOwnedRooms(ctx context.Context) {
	rooms, err := h.store.ListRooms(ctx, "")
	if err != nil {
		observability.Warn("list owned rooms at startup failed", "nodeID", h.nodeID, "error", err)
		return
	}
	now := time.Now().UTC()
	for _, rec := range rooms {
		if rec.OwnerNode != h.nodeID ||
			contracts.IsTerminalRoomStatus(rec.Status) ||
			rec.LeaseExpiresAt.IsZero() ||
			!rec.LeaseExpiresAt.After(now) {
			continue
		}
		owner, err := h.coord.EnsureRoomOwner(ctx, rec, h.nodeID, coordination.DefaultRoomLease)
		if err != nil {
			observability.Warn("verify startup room owner failed", "roomID", rec.RoomID, "epoch", rec.Epoch, "error", err)
			continue
		}
		rec.LeaseExpiresAt = owner.LeaseExpiresAt
		if err := h.store.VerifyRoomOwnerCandidate(ctx, rec.RoomID, h.nodeID, rec.Epoch); err != nil {
			observability.Warn("verify startup owner generation failed", "roomID", rec.RoomID, "epoch", rec.Epoch, "error", err)
			continue
		}
		if _, err := h.loadRoom(ctx, rec); err != nil {
			observability.Warn("bootstrap owned room failed", "roomID", rec.RoomID, "epoch", rec.Epoch, "error", err)
		}
	}
}

func (h *Host) HandleCommand(ctx context.Context, cmd contracts.RoomCommand) contracts.CommandAck {
	h.mu.Lock()
	draining := h.draining
	h.mu.Unlock()
	if draining {
		return contracts.CommandAck{
			CommandID: cmd.CommandID,
			RoomID:    cmd.RoomID,
			Accepted:  false,
			ErrorCode: "roomhost_draining",
			Message:   "roomhost is shutting down",
		}
	}
	if err := h.validateCommandSource(ctx, cmd); err != nil {
		return contracts.CommandAck{
			CommandID: cmd.CommandID,
			RoomID:    cmd.RoomID,
			Accepted:  false,
			ErrorCode: "unauthorized_command",
			Message:   err.Error(),
		}
	}
	observability.Info("room command received", "nodeID", h.nodeID, "roomID", cmd.RoomID, "type", cmd.Type, "playerID", cmd.PlayerID)
	_ = h.store.SaveRuntimeEvent(ctx, contracts.RuntimeEvent{
		RoomID:    cmd.RoomID,
		TraceID:   cmd.TraceID,
		NodeID:    h.nodeID,
		Component: "roomhost",
		Event:     "room_command_received",
		Message:   "roomhost received room command",
		Payload: map[string]any{
			"commandID":   cmd.CommandID,
			"type":        cmd.Type,
			"playerID":    cmd.PlayerID,
			"sessionID":   cmd.SessionID,
			"gatewayNode": cmd.GatewayNode,
			"epoch":       cmd.Epoch,
		},
	})
	if cmd.Type == contracts.RuntimeCommandRoomCreate {
		room, err := h.room(ctx, cmd)
		if err != nil {
			return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "room_error", Message: err.Error()}
		}
		room.mu.Lock()
		if room.seq == 0 {
			err = room.saveSnapshot(ctx)
		}
		room.mu.Unlock()
		if err != nil {
			return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "store_error", Message: err.Error()}
		}
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: room.match.MatchID, Accepted: true, Seq: room.seq}
	}
	if cmd.Type == contracts.RuntimeCommandMatchCreate {
		room, err := h.room(ctx, cmd)
		if err != nil {
			return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: cmd.MatchID, Accepted: false, ErrorCode: "room_error", Message: err.Error()}
		}
		if err := room.StartMatch(ctx, cmd.MatchID); err != nil {
			return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: cmd.MatchID, Accepted: false, ErrorCode: "match_error", Message: err.Error()}
		}
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: cmd.MatchID, Accepted: true, Seq: 0}
	}
	if cmd.Type == contracts.RuntimeCommandRoomClose {
		room, err := h.room(ctx, cmd)
		if err != nil {
			return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "room_error", Message: err.Error()}
		}
		ack := room.Close(ctx, cmd)
		if ack.Accepted {
			if h.coord != nil {
				if err := h.coord.DeleteRoomOwner(ctx, cmd.RoomID, h.nodeID, room.record.Epoch); err != nil {
					observability.Warn("closed room owner release failed", "roomID", cmd.RoomID, "nodeID", h.nodeID, "error", err)
				}
			}
			h.unloadRoom(cmd.RoomID)
		}
		return ack
	}
	room, err := h.room(ctx, cmd)
	if err != nil {
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "room_error", Message: err.Error()}
	}
	ack := room.Apply(ctx, cmd)
	return ack
}

func (h *Host) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		return nil
	}
	h.draining = true
	rooms := make([]*RoomActor, 0, len(h.rooms))
	for _, room := range h.rooms {
		rooms = append(rooms, room)
	}
	h.mu.Unlock()

	var shutdownErr error
	for _, room := range rooms {
		room.mu.Lock()
		if !room.closed {
			if err := room.saveSnapshot(ctx); err != nil {
				shutdownErr = errors.Join(shutdownErr, fmt.Errorf("save room %s snapshot: %w", room.record.RoomID, err))
			}
		}
		rec := room.record
		room.mu.Unlock()

		rec.LeaseExpiresAt = time.Now().UTC()
		if err := h.store.SetRoom(ctx, rec); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("expire room %s lease: %w", rec.RoomID, err))
		}
		if err := h.coord.DeleteRoomOwner(ctx, rec.RoomID, h.nodeID, rec.Epoch); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("release room %s owner: %w", rec.RoomID, err))
		}
		room.shutdown()
	}

	h.mu.Lock()
	h.rooms = map[string]*RoomActor{}
	h.emptySince = map[string]time.Time{}
	h.mu.Unlock()
	return shutdownErr
}

func (h *Host) validateCommandSource(ctx context.Context, cmd contracts.RoomCommand) error {
	switch cmd.Type {
	case contracts.RuntimeCommandRoomCreate, contracts.RuntimeCommandRoomClose, contracts.RuntimeCommandMatchCreate:
		if cmd.ConnectionRef.Valid() {
			return errors.New("runtime command cannot originate from a gateway session")
		}
		return nil
	default:
		if strings.HasPrefix(cmd.Type, "runtime_") {
			return errors.New("unknown runtime command")
		}
		return h.validateGatewayCommand(ctx, cmd)
	}
}

func (h *Host) validateGatewayCommand(ctx context.Context, cmd contracts.RoomCommand) error {
	if !cmd.ConnectionRef.Valid() {
		return errors.New("gateway connection reference is required")
	}
	session, err := h.store.GetSession(ctx, cmd.ConnectionRef.SessionID)
	if err != nil {
		return fmt.Errorf("gateway session not found: %w", err)
	}
	if session.GatewayNode != cmd.ConnectionRef.GatewayNode ||
		session.UserID != cmd.UserID ||
		session.PlayerID != cmd.PlayerID {
		return errors.New("gateway session identity mismatch")
	}
	return nil
}

func (h *Host) room(ctx context.Context, cmd contracts.RoomCommand) (*RoomActor, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if room, ok := h.rooms[cmd.RoomID]; ok {
		return room, nil
	}
	rec, err := h.store.GetRoom(ctx, cmd.RoomID)
	if err != nil {
		return nil, err
	}
	if rec.OwnerNode != h.nodeID {
		return nil, fmt.Errorf("room owner mismatch: want %s got %s", h.nodeID, rec.OwnerNode)
	}
	if contracts.IsTerminalRoomStatus(rec.Status) {
		return nil, errors.New("room is closed")
	}
	if rec.LeaseExpiresAt.IsZero() || !rec.LeaseExpiresAt.After(time.Now().UTC()) {
		return nil, coordination.ErrNotOwner
	}
	owner, err := h.coord.EnsureRoomOwner(ctx, rec, h.nodeID, coordination.DefaultRoomLease)
	if err != nil {
		return nil, err
	}
	rec.Epoch = owner.Epoch
	rec.LeaseExpiresAt = owner.LeaseExpiresAt
	if err := h.store.VerifyRoomOwnerCandidate(ctx, rec.RoomID, h.nodeID, rec.Epoch); err != nil {
		return nil, err
	}
	return h.activateRoomLocked(ctx, rec)
}

func (h *Host) activateRoomLocked(ctx context.Context, rec contracts.RoomRecord) (*RoomActor, error) {
	room, err := h.prepareRoomLocked(ctx, rec)
	if err != nil {
		return nil, err
	}
	return h.finalizeRoomLocked(ctx, rec, room)
}

func (h *Host) prepareRoomLocked(ctx context.Context, rec contracts.RoomRecord) (*RoomActor, error) {
	room, err := h.newRoomActor(ctx, rec)
	if err != nil {
		return nil, err
	}
	if _, err := h.router.AppendOwnerFence(contracts.CommandLogRecord{
		RoomID:    rec.RoomID,
		MatchID:   room.match.MatchID,
		Epoch:     rec.Epoch,
		OwnerNode: h.nodeID,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		room.shutdown()
		return nil, fmt.Errorf("append owner fence: %w", err)
	}
	if err := room.restore(ctx); err != nil {
		room.shutdown()
		return nil, fmt.Errorf("restore room %s: %w", rec.RoomID, err)
	}
	if err := h.store.ClaimMatchRuntime(ctx, room.match.MatchID, h.nodeID, rec.Epoch); err != nil {
		room.shutdown()
		return nil, fmt.Errorf("claim match runtime: %w", err)
	}
	if err := room.syncRoster(ctx); err != nil {
		observability.Warn("room roster restore failed", "roomID", rec.RoomID, "error", err)
	}
	if err := room.broadcast(ctx, "owner-ready"); err != nil {
		room.shutdown()
		return nil, fmt.Errorf("persist recovery broadcast: %w", err)
	}
	return room, nil
}

func (h *Host) finalizeRoomLocked(ctx context.Context, rec contracts.RoomRecord, room *RoomActor) (*RoomActor, error) {
	owner, err := h.coord.RenewRoomOwner(ctx, rec.RoomID, h.nodeID, rec.Epoch, coordination.DefaultRoomLease)
	if err != nil {
		room.shutdown()
		return nil, fmt.Errorf("renew owner before ready: %w", err)
	}
	room.record.LeaseExpiresAt = owner.LeaseExpiresAt
	if err := h.store.MarkRoomOwnerReady(ctx, rec.RoomID, h.nodeID, rec.Epoch, owner.LeaseExpiresAt); err != nil {
		room.shutdown()
		return nil, fmt.Errorf("mark owner ready: %w", err)
	}
	if err := room.syncRoomMetadataLocked(ctx); err != nil {
		observability.Warn("restored room metadata projection failed", "roomID", rec.RoomID, "error", err)
	}
	h.rooms[rec.RoomID] = room
	if err := h.coord.PublishRoomReady(ctx, rec.RoomID, h.nodeID, rec.Epoch); err != nil {
		delete(h.rooms, rec.RoomID)
		room.shutdown()
		return nil, fmt.Errorf("publish ready route: %w", err)
	}
	return room, nil
}

func (h *Host) loadRoom(ctx context.Context, rec contracts.RoomRecord) (*RoomActor, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if room, ok := h.rooms[rec.RoomID]; ok {
		return room, nil
	}
	room, err := h.activateRoomLocked(ctx, rec)
	if err != nil {
		return nil, err
	}
	observability.Info("room recovered", "nodeID", h.nodeID, "roomID", rec.RoomID, "epoch", rec.Epoch)
	return room, nil
}

func (h *Host) newRoomActor(ctx context.Context, rec contracts.RoomRecord) (*RoomActor, error) {
	def, err := h.definition(rec.GameType)
	if err != nil {
		return nil, err
	}
	if rec.EngineVersion == "" {
		rec.EngineVersion = def.Version
	} else if rec.EngineVersion != def.Version {
		return nil, fmt.Errorf("%w: room %s requires %s, host provides %s", ErrEngineVersionMismatch, rec.RoomID, rec.EngineVersion, def.Version)
	}
	if rec.CurrentMatchID == "" {
		rec.CurrentMatchID = rec.RoomID
	}
	match, err := h.store.GetMatch(ctx, rec.CurrentMatchID)
	if err != nil {
		return nil, fmt.Errorf("load current match: %w", err)
	}
	if match.EngineVersion != "" && match.EngineVersion != def.Version {
		return nil, fmt.Errorf("%w: match %s requires %s, host provides %s", ErrEngineVersionMismatch, match.MatchID, match.EngineVersion, def.Version)
	}
	state, err := def.Engine.InitRoom(ctx, game.RoomConfig{
		RoomID:        rec.RoomID,
		GameType:      rec.GameType,
		OwnerUserID:   rec.OwnerUserID,
		OwnerPlayerID: rec.OwnerPlayerID,
		MaxPlayers:    rec.MaxPlayers,
		RandomSeed:    match.RandomSeed,
		Visibility:    rec.Visibility,
	})
	if err != nil {
		return nil, err
	}
	actorCtx, cancel := context.WithCancel(context.Background())
	return &RoomActor{
		host:       h,
		record:     rec,
		match:      match,
		definition: def,
		state:      state,
		ctx:        actorCtx,
		cancel:     cancel,
		processed:  map[string]contracts.CommandAck{},
	}, nil
}

func (h *Host) definition(gameType string) (game.Definition, error) {
	def, ok := h.games.Get(gameType)
	if !ok {
		return game.Definition{}, fmt.Errorf("%w: %s", game.ErrUnknownGame, gameType)
	}
	return def, nil
}

func (h *Host) ActiveRooms() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.rooms)
}

func (h *Host) recoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rooms, err := h.store.ListExpiredRooms(ctx, time.Now().UTC(), 100)
			if err != nil {
				continue
			}
			for _, rec := range rooms {
				if !h.supportsRoomVersion(rec) {
					continue
				}
				if !h.shouldRecover(ctx, rec) {
					continue
				}
				reserved, err := h.store.ReserveRoomOwner(
					ctx,
					rec.RoomID,
					rec.Epoch,
					h.nodeID,
					time.Now().UTC(),
					coordination.DefaultRoomLease,
				)
				if err != nil {
					continue
				}
				owner, claimed, err := h.coord.ClaimExpiredRoom(ctx, reserved, h.nodeID, coordination.DefaultRoomLease)
				if err != nil || !claimed {
					continue
				}
				reserved.LeaseExpiresAt = owner.LeaseExpiresAt
				if err := h.store.VerifyRoomOwnerCandidate(ctx, reserved.RoomID, h.nodeID, reserved.Epoch); err != nil {
					_ = h.coord.DeleteRoomOwner(ctx, reserved.RoomID, h.nodeID, reserved.Epoch)
					continue
				}
				recoveryCtx, cancelRecovery := context.WithCancel(ctx)
				renewDone := make(chan error, 1)
				go h.renewCandidateLease(recoveryCtx, reserved, renewDone)
				h.mu.Lock()
				var (
					room     *RoomActor
					loadErr  error
					prepared bool
				)
				if existing := h.rooms[reserved.RoomID]; existing != nil {
					room = existing
				} else {
					room, loadErr = h.prepareRoomLocked(ctx, reserved)
					prepared = room != nil
				}
				cancelRecovery()
				renewErr := <-renewDone
				if loadErr == nil && renewErr == nil && h.rooms[reserved.RoomID] == nil {
					room, loadErr = h.finalizeRoomLocked(ctx, reserved, room)
					if loadErr == nil {
						observability.Info("room recovered", "nodeID", h.nodeID, "roomID", reserved.RoomID, "epoch", reserved.Epoch)
					}
				}
				if (loadErr != nil || renewErr != nil) && prepared && room != nil {
					room.shutdown()
				}
				h.mu.Unlock()
				if loadErr != nil || renewErr != nil {
					if renewErr == nil && errors.Is(loadErr, sql.ErrNoRows) {
						closed, closeErr := h.closeOrphanedEmptyRoom(ctx, reserved)
						if closeErr != nil {
							observability.Warn("close orphaned empty room failed", "roomID", reserved.RoomID, "epoch", reserved.Epoch, "error", closeErr)
						}
						if closed {
							continue
						}
					}
					_ = h.coord.DeleteRoomOwner(ctx, reserved.RoomID, h.nodeID, reserved.Epoch)
					observability.Warn("room recovery failed", "roomID", reserved.RoomID, "epoch", reserved.Epoch, "loadError", loadErr, "leaseError", renewErr)
				}
			}
		}
	}
}

func (h *Host) closeOrphanedEmptyRoom(ctx context.Context, rec contracts.RoomRecord) (bool, error) {
	if rec.Status != contracts.RoomStatusOpen || rec.PlayerCount > 0 {
		return false, nil
	}
	presences, err := h.store.ListPresence(ctx, rec.RoomID)
	if err != nil {
		return false, err
	}
	for _, presence := range presences {
		if presence.Online {
			return false, nil
		}
	}
	rec.Status = contracts.RoomStatusClosed
	rec.UpdatedAt = time.Now().UTC()
	if err := h.store.SetRoom(ctx, rec); err != nil {
		return false, err
	}
	observability.Info("orphaned empty room closed", "nodeID", h.nodeID, "roomID", rec.RoomID, "epoch", rec.Epoch)
	if h.coord != nil {
		if err := h.coord.DeleteRoomOwner(ctx, rec.RoomID, h.nodeID, rec.Epoch); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (h *Host) renewCandidateLease(ctx context.Context, rec contracts.RoomRecord, done chan<- error) {
	ticker := time.NewTicker(coordination.DefaultRoomLease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			owner, err := h.coord.RenewRoomOwner(ctx, rec.RoomID, h.nodeID, rec.Epoch, coordination.DefaultRoomLease)
			if err != nil {
				done <- err
				return
			}
			if err := h.store.RenewRoomOwnerProjection(ctx, rec.RoomID, h.nodeID, rec.Epoch, owner.LeaseExpiresAt); err != nil {
				done <- err
				return
			}
		}
	}
}

func (h *Host) projectionRetryLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.mu.Lock()
			rooms := make([]*RoomActor, 0, len(h.rooms))
			for _, room := range h.rooms {
				rooms = append(rooms, room)
			}
			h.mu.Unlock()
			for _, room := range rooms {
				room.mu.Lock()
				if room.closed {
					room.mu.Unlock()
					continue
				}
				if room.historyDirty {
					if err := room.reconcileHistoryLocked(ctx); err != nil {
						if room.suppressIrrecoverableHistoryProjectionLocked(err, "history projection retry skipped") {
							// The projection cannot be rebuilt from the current command log shape.
						} else {
							observability.Warn("history projection retry failed", "roomID", room.record.RoomID, "error", err)
						}
					}
				}
				if room.operationDirty {
					if err := room.reconcileOperationEventsLocked(ctx); err != nil {
						observability.Warn("operation projection retry failed", "roomID", room.record.RoomID, "error", err)
					}
				}
				if room.snapshotDirty {
					if err := room.saveSnapshot(ctx); err != nil {
						observability.Warn("snapshot projection retry failed", "roomID", room.record.RoomID, "seq", room.seq, "error", err)
					} else {
						room.snapshotDirty = false
					}
				}
				if room.spectatorDirty {
					if err := room.saveSpectatorSnapshot(ctx); err != nil {
						observability.Warn("spectator projection retry failed", "roomID", room.record.RoomID, "seq", room.seq, "error", err)
					} else {
						room.spectatorDirty = false
					}
				}
				if room.metadataDirty {
					if err := room.syncRoomMetadataLocked(ctx); err != nil {
						observability.Warn("room metadata projection retry failed", "roomID", room.record.RoomID, "error", err)
					}
				}
				if room.outboundDirty {
					if err := room.broadcast(ctx, "projection-retry"); err != nil {
						observability.Warn("outbound projection retry failed", "roomID", room.record.RoomID, "seq", room.seq, "error", err)
					} else {
						room.outboundDirty = false
					}
				}
				room.mu.Unlock()
			}
		}
	}
}

func (h *Host) supportsRoomVersion(rec contracts.RoomRecord) bool {
	def, err := h.definition(rec.GameType)
	if err != nil {
		return false
	}
	return rec.EngineVersion == "" || rec.EngineVersion == def.Version
}

func (h *Host) shouldRecover(ctx context.Context, rec contracts.RoomRecord) bool {
	if contracts.IsTerminalRoomStatus(rec.Status) {
		return false
	}
	if rec.PlayerCount > 0 {
		return true
	}
	presences, err := h.store.ListPresence(ctx, rec.RoomID)
	if err != nil {
		return false
	}
	for _, presence := range presences {
		if presence.Online {
			return true
		}
	}
	return rec.Status == contracts.RoomStatusOpen
}

func (h *Host) leaseLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.mu.Lock()
			roomIDs := make([]string, 0, len(h.rooms))
			for roomID := range h.rooms {
				roomIDs = append(roomIDs, roomID)
			}
			h.mu.Unlock()
			for _, roomID := range roomIDs {
				h.mu.Lock()
				room := h.rooms[roomID]
				var epoch int64
				if room != nil {
					epoch = room.record.Epoch
				}
				h.mu.Unlock()
				if room == nil {
					continue
				}
				owner, err := h.coord.RenewRoomOwner(ctx, roomID, h.nodeID, epoch, coordination.DefaultRoomLease)
				if err != nil {
					observability.Warn("room lease renew failed", "roomID", roomID, "nodeID", h.nodeID, "error", err)
					h.unloadRoom(roomID)
					continue
				}
				h.mu.Lock()
				if room := h.rooms[roomID]; room != nil {
					room.record.Epoch = owner.Epoch
					room.record.LeaseExpiresAt = owner.LeaseExpiresAt
				}
				h.mu.Unlock()
				if err := h.store.RenewRoomOwnerProjection(ctx, roomID, h.nodeID, owner.Epoch, owner.LeaseExpiresAt); err != nil {
					observability.Warn("room lease projection failed", "roomID", roomID, "nodeID", h.nodeID, "error", err)
					h.unloadRoom(roomID)
				}
			}
		}
	}
}

func (h *Host) unloadRoom(roomID string) {
	h.mu.Lock()
	room := h.rooms[roomID]
	delete(h.rooms, roomID)
	delete(h.emptySince, roomID)
	h.mu.Unlock()
	if room != nil {
		room.shutdown()
	}
}

func (h *Host) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.cleanupRooms(ctx, now)
		}
	}
}

func (h *Host) cleanupRooms(ctx context.Context, now time.Time) {
	h.mu.Lock()
	rooms := make([]*RoomActor, 0, len(h.rooms))
	for _, room := range h.rooms {
		rooms = append(rooms, room)
	}
	h.mu.Unlock()
	for _, room := range rooms {
		presences, err := h.store.ListPresence(ctx, room.record.RoomID)
		if err != nil {
			continue
		}
		hasHuman := false
		for _, presence := range presences {
			if presence.Online {
				hasHuman = true
				break
			}
		}
		if _, ok := room.definition.Engine.(game.HumanPresenceObserver); ok {
			check, err := room.observeHumanPresence(ctx, hasHuman)
			if err != nil {
				observability.Warn("observe room human presence failed", "roomID", room.record.RoomID, "error", err)
				continue
			}
			if !hasHuman {
				observability.Info("room has no human presence", "roomID", room.record.RoomID, "count", check.ConsecutiveEmpty, "limit", check.Limit)
			}
			if check.ShouldClose {
				h.closeRoomForNoHumans(ctx, room, now, "no_human_3_checks")
			}
			continue
		}
		h.mu.Lock()
		if hasHuman {
			delete(h.emptySince, room.record.RoomID)
			h.mu.Unlock()
			continue
		}
		since := h.emptySince[room.record.RoomID]
		if since.IsZero() {
			h.emptySince[room.record.RoomID] = now
			h.mu.Unlock()
			continue
		}
		h.mu.Unlock()
		if now.Sub(since) < 3*time.Minute {
			continue
		}
		h.closeRoomForNoHumans(ctx, room, now, "no_human_180s")
	}
}

func (h *Host) closeRoomForNoHumans(ctx context.Context, room *RoomActor, now time.Time, reason string) {
	cmd := contracts.RoomCommand{
		SchemaVersion: 1,
		CommandID:     uuid.NewString(),
		TraceID:       uuid.NewString(),
		RoomID:        room.record.RoomID,
		MatchID:       room.match.MatchID,
		GameType:      room.record.GameType,
		Epoch:         room.record.Epoch,
		UserID:        "system",
		PlayerID:      "system",
		Type:          contracts.RuntimeCommandRoomClose,
		Payload:       map[string]any{"status": contracts.RoomStatusClosed, "reason": reason},
		SentAt:        now.UTC(),
	}
	if ack := room.Close(ctx, cmd); ack.Accepted {
		if h.coord != nil {
			if err := h.coord.DeleteRoomOwner(ctx, room.record.RoomID, h.nodeID, room.record.Epoch); err != nil {
				observability.Warn("empty room owner release failed", "roomID", room.record.RoomID, "nodeID", h.nodeID, "error", err)
			}
		}
		h.unloadRoom(room.record.RoomID)
	}
}

type RoomActor struct {
	host           *Host
	record         contracts.RoomRecord
	match          contracts.MatchRecord
	definition     game.Definition
	state          game.State
	ctx            context.Context
	cancel         context.CancelFunc
	closed         bool
	seq            int64
	processed      map[string]contracts.CommandAck
	mu             sync.Mutex
	timer          *time.Timer
	timerKey       string
	timerGen       uint64
	historyID      int64
	historySeq     int
	historyDirty   bool
	operationDirty bool
	outboundDirty  bool
	snapshotDirty  bool
	spectatorDirty bool
	metadataDirty  bool
}

type commandLogSink struct {
	operations    []game.OperationEvent
	forceSnapshot bool
}

func (s *commandLogSink) AppendOperation(_ context.Context, event game.OperationEvent) error {
	s.operations = append(s.operations, event)
	return nil
}

func (s *commandLogSink) SaveSnapshotHint(_ context.Context, hint game.SnapshotHint) error {
	s.forceSnapshot = s.forceSnapshot || hint.Force
	return nil
}

func commandOperationRecords(record contracts.RoomRecord, cmd contracts.RoomCommand, seq int64, createdAt time.Time, events []game.OperationEvent) []contracts.GameLogEvent {
	out := make([]contracts.GameLogEvent, 0, len(events))
	for index, event := range events {
		playerID := event.PlayerID
		if playerID == "" {
			playerID = cmd.PlayerID
		}
		out = append(out, contracts.GameLogEvent{
			RoomID:     record.RoomID,
			MatchID:    cmd.MatchID,
			GameType:   record.GameType,
			Seq:        seq,
			EventID:    fmt.Sprintf("%s:%d:%d", cmd.MatchID, seq, index),
			CommandID:  cmd.CommandID,
			PlayerID:   playerID,
			Visibility: string(event.Visibility),
			Type:       event.Type,
			Payload:    event.Payload,
			CreatedAt:  createdAt,
		})
	}
	return out
}

func (r *RoomActor) observeHumanPresence(ctx context.Context, hasHuman bool) (game.HumanPresenceCheck, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return game.HumanPresenceCheck{}, nil
	}
	observer := r.definition.Engine.(game.HumanPresenceObserver)
	return observer.ObserveHumanPresence(ctx, r.state, hasHuman)
}

func (r *RoomActor) shutdown() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	r.stopTimerLocked()
}

func (r *RoomActor) StartMatch(ctx context.Context, matchID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("room actor is closed")
	}
	match, err := r.host.store.GetMatch(ctx, matchID)
	if err != nil {
		return err
	}
	if match.RuntimeRoomID != r.record.RoomID {
		return errors.New("match does not belong to room")
	}
	if r.match.MatchID == match.MatchID && (match.Status != contracts.MatchStatusPreparing || r.seq > 0) {
		return nil
	}
	if r.match.MatchID != "" && r.match.MatchID != match.MatchID {
		if r.historyDirty {
			if err := r.reconcileHistoryLocked(ctx); err != nil {
				return fmt.Errorf("finalize previous match history: %w", err)
			}
		}
		if r.operationDirty {
			if err := r.reconcileOperationEventsLocked(ctx); err != nil {
				return fmt.Errorf("finalize previous match operation log: %w", err)
			}
		}
		if r.snapshotDirty {
			if err := r.saveSnapshot(ctx); err != nil {
				return fmt.Errorf("finalize previous match snapshot: %w", err)
			}
		}
		if r.outboundDirty {
			if err := r.broadcast(ctx, "match-finalize"); err != nil {
				return fmt.Errorf("finalize previous match outbound: %w", err)
			}
		}
	}
	if _, err := r.host.router.AppendOwnerFence(contracts.CommandLogRecord{
		RoomID:    r.record.RoomID,
		MatchID:   match.MatchID,
		Epoch:     r.record.Epoch,
		OwnerNode: r.host.nodeID,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("append initial match owner fence: %w", err)
	}
	state, err := r.definition.Engine.InitRoom(ctx, game.RoomConfig{
		RoomID:        r.record.RoomID,
		GameType:      r.record.GameType,
		OwnerUserID:   r.record.OwnerUserID,
		OwnerPlayerID: r.record.OwnerPlayerID,
		MaxPlayers:    r.record.MaxPlayers,
		RandomSeed:    match.RandomSeed,
		Visibility:    r.record.Visibility,
	})
	if err != nil {
		return err
	}
	presences, err := r.host.store.ListPresence(ctx, r.record.RoomID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, presence := range presences {
		if !presence.Online {
			continue
		}
		cmd := contracts.RoomCommand{
			SchemaVersion: 1,
			RoomID:        r.record.RoomID,
			MatchID:       match.MatchID,
			GameType:      r.record.GameType,
			Epoch:         r.record.Epoch,
			UserID:        presence.UserID,
			PlayerID:      presence.PlayerID,
			Type:          "connect",
			SentAt:        now,
		}
		if _, err := r.definition.Engine.ApplyCommand(gamelog.WithSink(ctx, gamelog.NopSink{}), state, cmd); err != nil {
			return fmt.Errorf("restore lobby player %s: %w", presence.PlayerID, err)
		}
	}
	if r.cancel != nil {
		r.cancel()
	}
	r.stopTimerLocked()
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.state = state
	r.match = match
	r.record.CurrentMatchID = match.MatchID
	r.record.LastAppliedSeq = 0
	r.record.ProgressCurrent = 0
	r.record.ProgressTotal = 0
	r.seq = 0
	r.processed = map[string]contracts.CommandAck{}
	r.historyID = 0
	r.historySeq = 0
	r.historyDirty = false
	r.operationDirty = false
	r.outboundDirty = false
	r.snapshotDirty = false
	r.spectatorDirty = false
	r.metadataDirty = false
	if err := r.saveSnapshot(ctx); err != nil {
		return err
	}
	if err := r.syncRoomMetadataLocked(ctx); err != nil {
		observability.Warn("new match metadata projection failed", "roomID", r.record.RoomID, "matchID", match.MatchID, "error", err)
	}
	if err := r.syncRosterLocked(ctx, presences); err != nil {
		return err
	}
	if err := r.host.store.SetMatchRuntime(ctx, match.MatchID, contracts.MatchStatusPreparing, r.host.nodeID, r.record.Epoch, 0, r.record.PlayerCount); err != nil {
		return err
	}
	if err := r.host.store.SetRoom(ctx, r.record); err != nil {
		return err
	}
	if err := r.broadcast(ctx, "match-create"); err != nil {
		return err
	}
	r.rearmScheduledLocked(ctx, "match-create")
	return nil
}

func (r *RoomActor) Close(ctx context.Context, cmd contracts.RoomCommand) contracts.CommandAck {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cmd.Epoch != r.record.Epoch {
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "stale_epoch", Message: "room owner epoch changed"}
	}
	if r.closed {
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: true, Seq: r.seq}
	}
	if r.host.coord != nil {
		if err := r.host.coord.VerifyRoomOwner(ctx, r.record.RoomID, r.host.nodeID, r.record.Epoch); err != nil {
			return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "stale_epoch", Message: "room owner lease is no longer valid"}
		}
	}
	if r.historyDirty {
		if err := r.reconcileHistoryLocked(ctx); err != nil {
			return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "history_projection_unavailable", Message: err.Error()}
		}
	}
	if err := r.saveSnapshot(ctx); err != nil {
		observability.Warn("final snapshot save failed", "roomID", cmd.RoomID, "error", err)
	}
	if err := r.closeHistoryLocked(ctx); err != nil {
		observability.Warn("close game history failed", "roomID", cmd.RoomID, "error", err)
	}
	status := contracts.RoomStatusClosed
	if payload, ok := cmd.Payload.(map[string]any); ok {
		if value, ok := payload["status"].(string); ok && value != "" {
			status = value
		}
	}
	if !contracts.IsTerminalRoomStatus(status) {
		status = contracts.RoomStatusClosed
	}
	r.record.Status = status
	r.record.UpdatedAt = time.Now().UTC()
	if err := r.host.store.SetRoom(ctx, r.record); err != nil {
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "store_error", Message: err.Error()}
	}
	if r.match.MatchID != "" && !contracts.IsTerminalMatchStatus(r.match.Status) {
		if err := r.host.store.FinishMatch(
			ctx,
			r.record.RoomID,
			r.match.MatchID,
			contracts.MatchStatusAborted,
			"room_closed",
			r.host.nodeID,
			r.record.Epoch,
			r.seq,
		); err != nil {
			return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: r.match.MatchID, Accepted: false, ErrorCode: "store_error", Message: err.Error()}
		}
		r.match.Status = contracts.MatchStatusAborted
	}
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	r.stopTimerLocked()
	ack := contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: r.match.MatchID, Accepted: true, Seq: r.seq}
	_ = r.persistAck(ctx, ack)
	return ack
}

func (r *RoomActor) restore(ctx context.Context) error {
	snap, err := r.host.store.LatestSnapshot(ctx, r.match.MatchID)
	if err == nil && len(snap.State) > 0 {
		if snap.EngineVersion != "" && snap.EngineVersion != r.definition.Version {
			return fmt.Errorf("%w: snapshot %s requires %s, host provides %s", ErrEngineVersionMismatch, snap.RoomID, snap.EngineVersion, r.definition.Version)
		}
		state, err := r.definition.Engine.DecodeState(ctx, snap.State)
		if err != nil {
			return err
		}
		r.state = state
		r.seq = snap.Seq
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	logs, err := r.host.router.ListCommandLog(r.match.MatchID, 0)
	if err != nil {
		return err
	}
	expectedSeq := r.seq + 1
	var (
		activeFence        *contracts.CommandLogRecord
		lastStreamSequence uint64
	)
	for _, rec := range logs {
		if rec.StreamSequence > 0 {
			if lastStreamSequence > 0 && rec.StreamSequence <= lastStreamSequence {
				return fmt.Errorf("command log physical order regressed for match %s", r.match.MatchID)
			}
			lastStreamSequence = rec.StreamSequence
		}
		if rec.Kind == contracts.CommandLogKindOwnerFence {
			if activeFence != nil && rec.Epoch < activeFence.Epoch {
				return fmt.Errorf("owner fence epoch regressed for match %s: %d after %d", r.match.MatchID, rec.Epoch, activeFence.Epoch)
			}
			if rec.Epoch > r.record.Epoch {
				return fmt.Errorf("owner fence epoch %d is newer than room epoch %d", rec.Epoch, r.record.Epoch)
			}
			fence := rec
			activeFence = &fence
			continue
		}
		if rec.Kind == "" {
			rec.Kind = contracts.CommandLogKindCommand
		}
		if rec.Kind != contracts.CommandLogKindCommand {
			return fmt.Errorf("unknown command log record kind %q", rec.Kind)
		}
		if activeFence != nil &&
			(rec.Epoch != activeFence.Epoch ||
				(rec.OwnerNode != "" && rec.OwnerNode != activeFence.OwnerNode)) {
			return fmt.Errorf("command %s does not match active owner fence", rec.CommandID)
		}
		if previous, ok := r.processed[rec.CommandID]; ok && previous.Seq != rec.Seq {
			return fmt.Errorf("duplicate command id %s at seq %d and %d", rec.CommandID, previous.Seq, rec.Seq)
		}
		r.processed[rec.CommandID] = contracts.CommandAck{
			CommandID:  rec.CommandID,
			RoomID:     rec.RoomID,
			MatchID:    rec.MatchID,
			IntentHash: commandLogIntentHash(rec),
			Accepted:   true,
			Seq:        rec.Seq,
		}
		if rec.Seq <= r.seq {
			continue
		}
		if rec.Seq != expectedSeq {
			return fmt.Errorf("command log gap for room %s: got seq %d, want %d", r.record.RoomID, rec.Seq, expectedSeq)
		}
		cmd := contracts.RoomCommand{SchemaVersion: 1, CommandID: rec.CommandID, RoomID: rec.RoomID, MatchID: rec.MatchID, GameType: r.record.GameType, Epoch: rec.Epoch, PlayerID: rec.PlayerID, Type: rec.Type, Payload: rec.Payload, SentAt: rec.CreatedAt}
		if _, err := r.definition.Engine.ApplyCommand(gamelog.WithSink(ctx, gamelog.NopSink{}), r.state, cmd); err != nil {
			return err
		}
		r.seq = rec.Seq
		expectedSeq++
	}
	if activeFence == nil ||
		activeFence.Epoch != r.record.Epoch ||
		activeFence.OwnerNode != r.host.nodeID {
		return fmt.Errorf(
			"current owner fence is missing for match %s: want %s/%d",
			r.match.MatchID,
			r.host.nodeID,
			r.record.Epoch,
		)
	}
	if r.match.LastAppliedSeq > 0 && r.seq < r.match.LastAppliedSeq {
		return fmt.Errorf("command log incomplete for match %s: restored seq %d, durable head %d", r.match.MatchID, r.seq, r.match.LastAppliedSeq)
	}
	r.record.LastAppliedSeq = r.seq
	r.match.LastAppliedSeq = r.seq
	r.metadataDirty = true
	r.operationDirty = true
	if err := r.reconcileOperationEventsLocked(ctx); err != nil {
		observability.Warn("operation reconciliation after restore deferred", "roomID", r.record.RoomID, "error", err)
	}
	if r.definition.History != nil {
		r.historyDirty = true
		if err := r.reconcileHistoryLocked(ctx); err != nil {
			if r.suppressIrrecoverableHistoryProjectionLocked(err, "history reconciliation after restore skipped") {
				// The live room can continue; history projection will resume from subsequent commands.
			} else {
				observability.Warn("history reconciliation after restore deferred", "roomID", r.record.RoomID, "error", err)
			}
		}
	}
	r.mu.Lock()
	if err := r.resetAutomationStateLocked(ctx); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("reset automation state after restore: %w", err)
	}
	r.rearmScheduledLocked(ctx, "restore")
	r.mu.Unlock()
	return nil
}

func (r *RoomActor) saveSnapshot(ctx context.Context) error {
	b, err := r.definition.Engine.EncodeState(ctx, r.state)
	if err != nil {
		return err
	}
	return r.host.store.SaveSnapshot(ctx, contracts.SnapshotRecord{RoomID: r.record.RoomID, MatchID: r.match.MatchID, Seq: r.seq, Epoch: r.record.Epoch, EngineVersion: r.record.EngineVersion, State: b, CreatedAt: time.Now().UTC()})
}

func (r *RoomActor) saveSpectatorSnapshot(ctx context.Context) error {
	if r.match.Status != contracts.MatchStatusRunning {
		return nil
	}
	provider, ok := r.definition.Engine.(game.SpectatorViewProvider)
	if !ok {
		return nil
	}
	payload, err := provider.BuildSpectatorView(ctx, r.state, r.seq)
	if err != nil {
		return err
	}
	capturedAt := time.Now().UTC()
	return r.host.store.SaveSpectatorSnapshot(ctx, contracts.SpectatorSnapshotRecord{
		RoomID:      r.record.RoomID,
		MatchID:     r.match.MatchID,
		GameType:    r.record.GameType,
		Seq:         r.seq,
		Epoch:       r.record.Epoch,
		OwnerNode:   r.host.nodeID,
		Payload:     payload,
		CapturedAt:  capturedAt,
		AvailableAt: capturedAt.Add(contracts.SpectatorDelay),
	})
}

func (r *RoomActor) syncRoomMetadataLocked(ctx context.Context) error {
	provider, ok := r.definition.Engine.(game.RoomMetadataProvider)
	if !ok {
		r.metadataDirty = false
		return nil
	}
	metadata, err := provider.RoomMetadata(ctx, r.state)
	if err != nil {
		r.metadataDirty = true
		return err
	}
	if metadata.Visibility == "" || metadata.Visibility == r.record.Visibility {
		r.metadataDirty = false
		return nil
	}
	if err := r.host.store.SetRoomVisibilityIfOwner(
		ctx,
		r.record.RoomID,
		metadata.Visibility,
		r.host.nodeID,
		r.record.Epoch,
	); err != nil {
		r.metadataDirty = true
		return err
	}
	r.record.Visibility = metadata.Visibility
	r.metadataDirty = false
	r.outboundDirty = true
	return nil
}

func (r *RoomActor) Apply(ctx context.Context, cmd contracts.RoomCommand) contracts.CommandAck {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: cmd.MatchID, Accepted: false, ErrorCode: "room_closed", Message: "room actor is closed"}
	}
	if cmd.MatchID == "" {
		cmd.MatchID = r.match.MatchID
	}
	if cmd.MatchID != r.match.MatchID {
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: cmd.MatchID, Accepted: false, ErrorCode: "stale_match", Message: "room current match changed"}
	}
	intentHash, err := commandIntentHash(cmd)
	if err != nil {
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: cmd.MatchID, Accepted: false, ErrorCode: "invalid_command", Message: err.Error()}
	}
	if prev, ok := r.processed[cmd.CommandID]; ok {
		if prev.IntentHash != "" && prev.IntentHash != intentHash {
			return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: cmd.MatchID, Accepted: false, ErrorCode: "command_id_reused", Message: store.ErrCommandIntentConflict.Error()}
		}
		return prev
	}
	if prev, err := r.host.store.GetCommandAck(ctx, cmd.RoomID, cmd.MatchID, cmd.CommandID, intentHash); err == nil {
		r.processed[cmd.CommandID] = prev
		return prev
	} else if errors.Is(err, store.ErrCommandIntentConflict) {
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: cmd.MatchID, Accepted: false, ErrorCode: "command_id_reused", Message: err.Error()}
	} else if !errors.Is(err, sql.ErrNoRows) {
		observability.Warn("command ack lookup failed", "roomID", cmd.RoomID, "commandID", cmd.CommandID, "error", err)
	}
	if cmd.Epoch != r.record.Epoch {
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "stale_epoch", Message: "room owner epoch changed"}
	}
	if r.host.coord != nil {
		if err := r.host.coord.VerifyRoomOwner(ctx, r.record.RoomID, r.host.nodeID, r.record.Epoch); err != nil {
			return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "stale_epoch", Message: "room owner lease is no longer valid"}
		}
	}
	encodedState, err := r.definition.Engine.EncodeState(ctx, r.state)
	if err != nil {
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "state_clone_error", Message: err.Error()}
	}
	stagedState, err := r.definition.Engine.DecodeState(ctx, encodedState)
	if err != nil {
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "state_clone_error", Message: err.Error()}
	}
	logSink := &commandLogSink{}
	result, err := r.definition.Engine.ApplyCommand(gamelog.WithSink(ctx, logSink), stagedState, cmd)
	if err != nil {
		ack := contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: cmd.MatchID, IntentHash: intentHash, Accepted: false, ErrorCode: "invalid_action", Message: err.Error()}
		_ = r.persistAck(ctx, ack)
		_ = r.sendError(ctx, cmd, ack)
		return ack
	}
	nextSeq := r.seq + 1
	commandTime := cmd.SentAt.UTC()
	if commandTime.IsZero() {
		commandTime = time.Now().UTC()
	}
	operations := commandOperationRecords(r.record, cmd, nextSeq, commandTime, logSink.operations)
	appendResult, err := r.host.router.AppendCommandLog(contracts.CommandLogRecord{
		RoomID:     cmd.RoomID,
		MatchID:    cmd.MatchID,
		Seq:        nextSeq,
		CommandID:  cmd.CommandID,
		Epoch:      r.record.Epoch,
		OwnerNode:  r.host.nodeID,
		PlayerID:   cmd.PlayerID,
		Type:       cmd.Type,
		Payload:    cmd.Payload,
		Operations: operations,
		CreatedAt:  commandTime,
	})
	if err != nil {
		observability.Warn("append command log failed", "roomID", cmd.RoomID, "commandID", cmd.CommandID, "seq", nextSeq, "error", err)
		switch {
		case errors.Is(err, transport.ErrCommandLogStaleEpoch):
			r.failClosedLocked()
			go r.host.unloadRoom(r.record.RoomID)
			return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "stale_epoch", Message: err.Error()}
		case errors.Is(err, transport.ErrCommandLogSequenceConflict),
			errors.Is(err, transport.ErrCommandLogFenceRequired):
			r.failClosedLocked()
			go r.host.unloadRoom(r.record.RoomID)
		}
		return contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: false, ErrorCode: "command_log_unavailable", Message: err.Error()}
	}
	if appendResult.Duplicate && appendResult.Seq < nextSeq {
		ack := contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: cmd.MatchID, IntentHash: intentHash, Accepted: true, Seq: appendResult.Seq}
		if err := r.persistAck(ctx, ack); err != nil {
			observability.Warn("duplicate command ack persistence failed", "roomID", cmd.RoomID, "commandID", cmd.CommandID, "seq", appendResult.Seq, "error", err)
		}
		return ack
	}
	if appendResult.Seq != nextSeq {
		return contracts.CommandAck{
			CommandID: cmd.CommandID,
			RoomID:    cmd.RoomID,
			MatchID:   cmd.MatchID,
			Accepted:  false,
			ErrorCode: "command_log_unavailable",
			Message:   fmt.Sprintf("command log returned seq %d, want %d", appendResult.Seq, nextSeq),
		}
	}
	r.state = stagedState
	r.seq = nextSeq
	r.record.LastAppliedSeq = r.seq
	r.match.LastAppliedSeq = r.seq
	if !result.Finished && result.Status != "" && result.Status != contracts.RoomStatusMatch {
		r.match.Status = contracts.MatchStatusRunning
	}
	if err := r.syncRoomMetadataLocked(ctx); err != nil {
		observability.Warn("room metadata projection failed", "roomID", cmd.RoomID, "commandID", cmd.CommandID, "error", err)
	}
	ack := contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, MatchID: cmd.MatchID, IntentHash: intentHash, Accepted: true, Seq: r.seq}
	if err := r.persistAck(ctx, ack); err != nil {
		observability.Warn("command ack persistence failed", "roomID", cmd.RoomID, "commandID", cmd.CommandID, "seq", r.seq, "error", err)
	}
	if err := r.host.store.AppendGameLogEvents(ctx, operations); err != nil {
		observability.Warn("operation projection failed", "roomID", cmd.RoomID, "commandID", cmd.CommandID, "error", err)
		r.operationDirty = true
	}
	if err := r.saveSpectatorSnapshot(ctx); err != nil {
		observability.Warn("spectator projection failed", "roomID", cmd.RoomID, "commandID", cmd.CommandID, "error", err)
		r.spectatorDirty = true
	} else {
		r.spectatorDirty = false
	}
	observability.Info("command applied", "nodeID", r.host.nodeID, "roomID", cmd.RoomID, "commandID", cmd.CommandID, "type", cmd.Type, "playerID", cmd.PlayerID, "seq", r.seq)
	_ = r.host.store.SaveRuntimeEvent(ctx, contracts.RuntimeEvent{
		RoomID:    cmd.RoomID,
		MatchID:   cmd.MatchID,
		TraceID:   cmd.TraceID,
		NodeID:    r.host.nodeID,
		Component: "roomhost",
		Event:     "command_applied",
		Message:   "roomhost applied command to game engine",
		Payload: map[string]any{
			"commandID": cmd.CommandID,
			"type":      cmd.Type,
			"playerID":  cmd.PlayerID,
			"seq":       r.seq,
			"epoch":     r.record.Epoch,
		},
	})
	if err := r.syncHistoryLocked(ctx, cmd); err != nil {
		observability.Warn("sync game history failed", "roomID", cmd.RoomID, "commandID", cmd.CommandID, "error", err)
		r.historyDirty = true
	}
	if r.shouldSnapshot(result, logSink.forceSnapshot) {
		if err := r.saveSnapshot(ctx); err != nil {
			observability.Warn("snapshot save failed", "roomID", cmd.RoomID, "seq", r.seq, "error", err)
			r.snapshotDirty = true
		} else {
			r.snapshotDirty = false
			observability.Info("snapshot saved", "roomID", cmd.RoomID, "seq", r.seq, "epoch", r.record.Epoch)
			_ = r.host.store.SaveRuntimeEvent(ctx, contracts.RuntimeEvent{
				RoomID:    cmd.RoomID,
				TraceID:   cmd.TraceID,
				NodeID:    r.host.nodeID,
				Component: "roomhost",
				Event:     "snapshot_saved",
				Message:   "roomhost saved room snapshot",
				Payload: map[string]any{
					"seq":   r.seq,
					"epoch": r.record.Epoch,
				},
			})
		}
	}
	if err := r.deliverEffects(ctx, cmd.TraceID, result.Effects); err != nil {
		observability.Warn("deliver game effects failed", "roomID", cmd.RoomID, "error", err)
		r.outboundDirty = true
	}
	if err := r.broadcast(ctx, cmd.TraceID); err != nil {
		observability.Warn("broadcast failed", "roomID", cmd.RoomID, "error", err)
		r.outboundDirty = true
	} else {
		r.outboundDirty = false
	}
	if result.Finished {
		r.match.Status = contracts.MatchStatusEnded
		if err := r.host.store.FinishMatch(
			ctx,
			r.record.RoomID,
			r.match.MatchID,
			contracts.MatchStatusEnded,
			"completed",
			r.host.nodeID,
			r.record.Epoch,
			r.seq,
		); err != nil {
			observability.Warn("finish match failed", "roomID", cmd.RoomID, "matchID", cmd.MatchID, "error", err)
		}
	} else if err := r.host.store.SetMatchRuntime(ctx, r.match.MatchID, r.match.Status, r.host.nodeID, r.record.Epoch, r.seq, r.record.PlayerCount); err != nil {
		observability.Warn("update match runtime failed", "roomID", cmd.RoomID, "matchID", cmd.MatchID, "error", err)
	}
	r.rearmScheduledLocked(ctx, cmd.TraceID)
	for _, req := range result.AIRequests {
		go r.runAI(r.ctx, req, cmd.TraceID)
	}
	return ack
}

func (r *RoomActor) failClosedLocked() {
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	r.stopTimerLocked()
}

func commandIntentHash(cmd contracts.RoomCommand) (string, error) {
	payload, err := json.Marshal(struct {
		RoomID   string `json:"roomID"`
		MatchID  string `json:"matchID"`
		PlayerID string `json:"playerID"`
		Type     string `json:"type"`
		Payload  any    `json:"payload"`
	}{
		RoomID:   cmd.RoomID,
		MatchID:  cmd.MatchID,
		PlayerID: cmd.PlayerID,
		Type:     cmd.Type,
		Payload:  cmd.Payload,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:]), nil
}

func commandLogIntentHash(rec contracts.CommandLogRecord) string {
	hash, _ := commandIntentHash(contracts.RoomCommand{
		RoomID:   rec.RoomID,
		MatchID:  rec.MatchID,
		PlayerID: rec.PlayerID,
		Type:     rec.Type,
		Payload:  rec.Payload,
	})
	return hash
}

func (r *RoomActor) persistAck(ctx context.Context, ack contracts.CommandAck) error {
	ack.Epoch = r.record.Epoch
	ack.OwnerNode = r.host.nodeID
	r.processed[ack.CommandID] = ack
	return r.host.store.SaveCommandAck(ctx, ack)
}

func (r *RoomActor) shouldSnapshot(result game.ApplyResult, force bool) bool {
	if result.Finished || force {
		return true
	}
	return r.seq == 1 || r.seq%snapshotEverySeq == 0
}

func (r *RoomActor) syncHistoryLocked(ctx context.Context, cmd contracts.RoomCommand) error {
	provider := r.definition.History
	if provider == nil {
		return nil
	}
	projection, err := provider.ProjectHistory(ctx, r.state)
	if err != nil || !projection.Started {
		return err
	}
	if err := r.ensureHistoryLocked(ctx, projection); err != nil {
		return err
	}
	if provider.IsRecordableCommand(cmd.Type) {
		payload, err := json.Marshal(cmd.Payload)
		if err != nil {
			return err
		}
		if err := r.host.store.AppendHistoryEvent(ctx, history.Event{
			GameID:      r.historyID,
			Seq:         r.historySeq,
			OccurredAt:  time.Now().UTC(),
			PlayerID:    cmd.PlayerID,
			CommandType: cmd.Type,
			Payload:     payload,
			StateBlob:   projection.StateBlob,
		}); err != nil {
			return err
		}
		r.historySeq++
	}
	if projection.Finished {
		if err := r.finishHistoryLocked(ctx, projection, history.EndReasonCompleted, false); err != nil {
			return err
		}
	}
	return nil
}

func (r *RoomActor) reconcileOperationEventsLocked(ctx context.Context) error {
	logs, err := r.host.router.ListCommandLog(r.match.MatchID, 0)
	if err != nil {
		return err
	}
	events := make([]contracts.GameLogEvent, 0)
	expectedSeq := int64(1)
	for _, record := range logs {
		if record.Kind == contracts.CommandLogKindOwnerFence {
			continue
		}
		if record.Seq != expectedSeq {
			return fmt.Errorf("operation projection command gap for room %s: got %d, want %d", r.record.RoomID, record.Seq, expectedSeq)
		}
		events = append(events, record.Operations...)
		expectedSeq++
	}
	if err := r.host.store.AppendGameLogEvents(ctx, events); err != nil {
		return err
	}
	r.operationDirty = false
	return nil
}

func (r *RoomActor) reconcileHistoryLocked(ctx context.Context) error {
	if r.definition.History == nil {
		r.historyDirty = false
		return nil
	}
	logs, err := r.host.router.ListCommandLog(r.match.MatchID, 0)
	if err != nil {
		return err
	}
	expectedSeq := int64(1)
	for _, record := range logs {
		if record.Kind == contracts.CommandLogKindOwnerFence {
			continue
		}
		if record.Seq != expectedSeq {
			return fmt.Errorf("history projection command gap for room %s: got %d, want %d", r.record.RoomID, record.Seq, expectedSeq)
		}
		expectedSeq++
	}
	if err := r.host.store.ResetOpenHistoryGame(ctx, r.record.RoomID, r.record.GameType); err != nil {
		return err
	}
	var randomSeed uint64
	if provider, ok := r.definition.Engine.(game.RandomSeedProvider); ok {
		randomSeed, err = provider.RandomSeed(ctx, r.state)
		if err != nil {
			return err
		}
	}
	state, err := r.definition.Engine.InitRoom(ctx, game.RoomConfig{
		RoomID:        r.record.RoomID,
		GameType:      r.record.GameType,
		OwnerUserID:   r.record.OwnerUserID,
		OwnerPlayerID: r.record.OwnerPlayerID,
		MaxPlayers:    r.record.MaxPlayers,
		RandomSeed:    randomSeed,
		Visibility:    r.record.Visibility,
	})
	if err != nil {
		return err
	}

	liveState := r.state
	r.state = state
	r.historyID = 0
	r.historySeq = 0
	succeeded := false
	defer func() {
		r.state = liveState
		if !succeeded {
			r.historyID = 0
			r.historySeq = 0
		}
	}()

	for _, record := range logs {
		if record.Kind == contracts.CommandLogKindOwnerFence {
			continue
		}
		cmd := contracts.RoomCommand{
			SchemaVersion: 1,
			CommandID:     record.CommandID,
			RoomID:        record.RoomID,
			MatchID:       record.MatchID,
			GameType:      r.record.GameType,
			Epoch:         record.Epoch,
			UserID:        record.PlayerID,
			PlayerID:      record.PlayerID,
			Type:          record.Type,
			Payload:       record.Payload,
			SentAt:        record.CreatedAt,
		}
		if _, err := r.definition.Engine.ApplyCommand(gamelog.WithSink(ctx, gamelog.NopSink{}), r.state, cmd); err != nil {
			return err
		}
		if err := r.syncHistoryLocked(ctx, cmd); err != nil {
			return err
		}
	}
	succeeded = true
	r.historyDirty = false
	return nil
}

func (r *RoomActor) suppressIrrecoverableHistoryProjectionLocked(err error, message string) bool {
	if err == nil {
		return false
	}
	if r.record.GameType == contracts.GameAcquire && strings.Contains(err.Error(), "至少需要两名玩家") {
		r.historyDirty = false
		observability.Info(message, "roomID", r.record.RoomID, "error", err)
		return true
	}
	return false
}

func (r *RoomActor) ensureHistoryLocked(ctx context.Context, projection game.HistoryProjection) error {
	if r.historyID != 0 {
		return nil
	}
	gameID, nextSeq, err := r.host.store.OpenHistoryGame(ctx, r.record.RoomID, r.record.GameType)
	if err == nil {
		r.historyID = gameID
		r.historySeq = nextSeq
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	players := historyPlayers(projection.Players, false)
	gameID, err = r.host.store.StartHistoryGame(ctx, history.Game{
		RoomID:       r.record.RoomID,
		GameType:     r.record.GameType,
		StartedAt:    projection.StartedAt,
		MaxPlayers:   projection.MaxPlayers,
		InitialState: json.RawMessage(projection.InitialState),
	}, players)
	if err != nil {
		return err
	}
	r.historyID = gameID
	r.historySeq = 0
	return nil
}

func (r *RoomActor) finishHistoryLocked(ctx context.Context, projection game.HistoryProjection, reason string, abandoned bool) error {
	if r.historyID == 0 {
		return nil
	}
	finalResult, err := json.Marshal(projection.FinalResult)
	if err != nil {
		return err
	}
	endedAt := time.Now().UTC()
	startedAt := projection.StartedAt
	if startedAt.IsZero() {
		startedAt = endedAt
	}
	var winnerUserID *int64
	if !abandoned && projection.WinnerUserID != "" {
		if parsed, err := strconv.ParseInt(projection.WinnerUserID, 10, 64); err == nil {
			winnerUserID = &parsed
		}
	}
	winnerPlayerID := projection.WinnerPlayerID
	if abandoned {
		winnerPlayerID = ""
	}
	err = r.host.store.FinishHistoryGame(ctx, r.historyID, history.FinalizeInput{
		EndedAt:         endedAt,
		DurationSeconds: int(endedAt.Sub(startedAt).Seconds()),
		WinnerUserID:    winnerUserID,
		WinnerPlayerID:  winnerPlayerID,
		EndReason:       reason,
		FinalResult:     finalResult,
		Players:         historyPlayers(projection.Players, abandoned),
	})
	if err != nil {
		return err
	}
	r.historyID = 0
	r.historySeq = 0
	return nil
}

func (r *RoomActor) closeHistoryLocked(ctx context.Context) error {
	if r.definition.History == nil {
		return nil
	}
	projection, err := r.definition.History.ProjectHistory(ctx, r.state)
	if err != nil || !projection.Started {
		return err
	}
	if err := r.ensureHistoryLocked(ctx, projection); err != nil {
		return err
	}
	if projection.Finished {
		return r.finishHistoryLocked(ctx, projection, history.EndReasonCompleted, false)
	}
	if projection.AbandonedEligible {
		return r.finishHistoryLocked(ctx, projection, history.EndReasonAbandoned, true)
	}
	err = r.host.store.DiscardHistoryGame(ctx, r.historyID)
	r.historyID = 0
	r.historySeq = 0
	return err
}

func historyPlayers(players []game.HistoryPlayer, abandoned bool) []history.Player {
	out := make([]history.Player, 0, len(players))
	abandonedRank := len(players)
	for _, player := range players {
		var userID *int64
		if player.UserID != "" {
			if parsed, err := strconv.ParseInt(player.UserID, 10, 64); err == nil {
				userID = &parsed
			}
		}
		finalRank := player.FinalRank
		isWinner := player.IsWinner
		if abandoned {
			rank := abandonedRank
			finalRank = &rank
			isWinner = false
		}
		out = append(out, history.Player{
			UserID:      userID,
			PlayerID:    player.PlayerID,
			SeatIndex:   player.SeatIndex,
			IsAI:        player.IsAI,
			FinalScore:  player.FinalScore,
			FinalMoney:  player.FinalMoney,
			FinalStocks: player.FinalStocks,
			FinalRank:   finalRank,
			IsWinner:    isWinner,
		})
	}
	return out
}

func (r *RoomActor) stopTimerLocked() {
	r.timerGen++
	r.timerKey = ""
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
}

func (r *RoomActor) rearmScheduledLocked(ctx context.Context, traceID string) {
	r.stopTimerLocked()
	if r.definition.Scheduler == nil {
		return
	}
	pending, err := r.definition.Scheduler.PendingCommands(ctx, r.state)
	if err != nil {
		observability.Warn("read pending game commands failed", "roomID", r.record.RoomID, "error", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].ExecuteAt.Before(pending[j].ExecuteAt)
	})
	next := pending[0]
	if next.Key == "" || next.Type == "" || next.PlayerID == "" {
		return
	}
	r.timerGen++
	generation := r.timerGen
	r.timerKey = next.Key
	delay := time.Until(next.ExecuteAt)
	if delay < 0 {
		delay = 0
	}
	r.timer = time.AfterFunc(delay, func() {
		r.mu.Lock()
		if r.closed || r.timerGen != generation || r.timerKey != next.Key {
			r.mu.Unlock()
			return
		}
		r.timer = nil
		r.timerKey = ""
		r.mu.Unlock()
		cmd := contracts.RoomCommand{
			SchemaVersion: 1,
			CommandID:     uuid.NewString(),
			TraceID:       traceID,
			RoomID:        r.record.RoomID,
			MatchID:       r.match.MatchID,
			GameType:      r.record.GameType,
			Epoch:         r.record.Epoch,
			UserID:        next.PlayerID,
			PlayerID:      next.PlayerID,
			Type:          next.Type,
			Payload:       next.Payload,
			SentAt:        time.Now().UTC(),
		}
		_ = r.Apply(r.ctx, cmd)
	})
	observability.Info("game command scheduled", "roomID", r.record.RoomID, "key", next.Key, "type", next.Type, "playerID", next.PlayerID, "executeAt", next.ExecuteAt)
}

func (r *RoomActor) deliverEffects(ctx context.Context, traceID string, effects []game.DeliveryEffect) error {
	if len(effects) == 0 {
		return nil
	}
	presences, err := r.host.store.ListPresence(ctx, r.record.RoomID)
	if err != nil {
		return err
	}
	playerProfiles, err := r.loadPlayerProfiles(ctx)
	if err != nil {
		observability.Warn("load player profiles for effects failed", "roomID", r.record.RoomID, "error", err)
	}
	for _, effect := range effects {
		for _, presence := range presences {
			if !presence.Online {
				continue
			}
			if effect.Audience == game.AudiencePlayer && effect.TargetPlayerID != presence.PlayerID {
				continue
			}
			payload := effect.Payload
			switch typed := payload.(type) {
			case contracts.ErrorMessage:
				typed.SchemaVersion = contracts.RealtimeSchemaVersion
				typed.Type = contracts.MessageTypeError
				typed.RoomID = r.record.RoomID
				typed.MatchID = r.match.MatchID
				typed.Seq = r.seq
				payload = typed
			default:
				view, viewErr := r.definition.Engine.BuildPlayerView(ctx, r.state, game.ViewRequest{
					RoomID:         r.record.RoomID,
					MatchID:        r.match.MatchID,
					GameType:       r.record.GameType,
					PlayerID:       presence.PlayerID,
					Visibility:     r.record.Visibility,
					MaxPlayers:     r.record.MaxPlayers,
					Seq:            r.seq,
					PlayerProfiles: playerProfiles,
				})
				if viewErr != nil {
					return viewErr
				}
				payload = realtimePlayerView(
					view,
					r.record.GameType,
					r.record.RoomID,
					r.match.MatchID,
					r.seq,
					[]map[string]any{realtimeEffect(effect.Payload)},
				)
			}
			out := contracts.OutboundMessage{
				SchemaVersion:  contracts.RealtimeSchemaVersion,
				MessageID:      uuid.NewString(),
				TraceID:        traceID,
				RoomID:         r.record.RoomID,
				MatchID:        r.match.MatchID,
				Seq:            r.seq,
				Epoch:          r.record.Epoch,
				OwnerNode:      r.host.nodeID,
				TargetUserID:   presence.UserID,
				TargetPlayerID: presence.PlayerID,
				TargetConn:     presence.ConnectionRef,
				Reliable:       effect.Reliable,
				CloseAfter:     effect.CloseAfter,
				Payload:        payload,
				CreatedAt:      time.Now().UTC(),
			}
			if effect.Reliable {
				if err := r.host.store.SaveOutboundMessage(ctx, out); err != nil {
					return err
				}
			}
			if err := r.host.router.Deliver(out); err != nil {
				return err
			}
		}
	}
	return nil
}

func realtimeEffect(payload any) map[string]any {
	result := map[string]any{"type": "effect"}
	if value, ok := payload.(map[string]any); ok {
		if effectType, ok := value["type"].(string); ok && effectType != "" {
			result["type"] = effectType
		}
		if message, ok := value["message"].(string); ok && message != "" {
			result["value"] = message
		}
	}
	return result
}

func (r *RoomActor) runAI(ctx context.Context, req game.AIRequest, traceID string) {
	delay := req.Delay
	if delay <= 0 {
		delay = aiThinkDelay()
	}
	observability.Info("ai decision scheduled", "roomID", req.RoomID, "playerID", req.PlayerID, "delayMs", delay.Milliseconds())
	_ = r.host.store.SaveRuntimeEvent(ctx, contracts.RuntimeEvent{
		RoomID:    req.RoomID,
		MatchID:   r.match.MatchID,
		TraceID:   traceID,
		NodeID:    r.host.nodeID,
		Component: "roomhost",
		Event:     "ai_decision_scheduled",
		Message:   "roomhost scheduled ai decision",
		Payload: map[string]any{
			"playerID": req.PlayerID,
			"delayMs":  delay.Milliseconds(),
		},
	})
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return
	case <-timer.C:
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = aiDecisionTimeout
	}
	aiCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	aiCmd, err := r.decideAI(aiCtx, req)
	if err != nil {
		observability.Warn("ai decision failed", "roomID", req.RoomID, "playerID", req.PlayerID, "error", err)
		r.recoverAutomation(r.ctx, traceID, "decision_failed")
		return
	}
	aiCmd.SchemaVersion = 1
	aiCmd.CommandID = uuid.NewString()
	aiCmd.TraceID = traceID
	aiCmd.RoomID = req.RoomID
	aiCmd.MatchID = r.match.MatchID
	aiCmd.GameType = r.record.GameType
	aiCmd.Epoch = r.record.Epoch
	aiCmd.UserID = req.PlayerID
	aiCmd.PlayerID = req.PlayerID
	aiCmd.ConnectionRef = contracts.ConnectionRef{}
	aiCmd.SentAt = time.Now().UTC()
	observability.Info("ai decision dispatch", "roomID", req.RoomID, "playerID", req.PlayerID, "type", aiCmd.Type, "delayMs", delay.Milliseconds())
	_ = r.host.store.SaveRuntimeEvent(ctx, contracts.RuntimeEvent{
		RoomID:    req.RoomID,
		MatchID:   r.match.MatchID,
		TraceID:   traceID,
		NodeID:    r.host.nodeID,
		Component: "roomhost",
		Event:     "ai_decision_dispatch",
		Message:   "roomhost dispatched ai decision",
		Payload: map[string]any{
			"commandID": aiCmd.CommandID,
			"playerID":  req.PlayerID,
			"type":      aiCmd.Type,
			"delayMs":   delay.Milliseconds(),
		},
	})
	ack := r.Apply(ctx, aiCmd)
	if !ack.Accepted {
		observability.Warn(
			"ai command rejected",
			"roomID", req.RoomID,
			"playerID", req.PlayerID,
			"type", aiCmd.Type,
			"errorCode", ack.ErrorCode,
			"message", ack.Message,
		)
		r.recoverAutomation(r.ctx, traceID, "command_rejected")
	}
}

func (r *RoomActor) decideAI(ctx context.Context, req game.AIRequest) (contracts.RoomCommand, error) {
	if r.definition.AI != nil {
		cmd, err := r.definition.AI.Decide(ctx, req)
		if err != nil {
			if req.FallbackCommand != nil {
				return *req.FallbackCommand, nil
			}
			return contracts.RoomCommand{}, err
		}
		return cmd, nil
	}
	if req.FallbackCommand != nil {
		return *req.FallbackCommand, nil
	}
	return contracts.RoomCommand{}, errors.New("game has no ai planner")
}

func (r *RoomActor) resetAutomationStateLocked(ctx context.Context) error {
	resetter, ok := r.definition.Engine.(game.AutomationStateResetter)
	if !ok {
		return nil
	}
	return resetter.ResetAutomationState(ctx, r.state)
}

func (r *RoomActor) recoverAutomation(ctx context.Context, traceID, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if err := r.resetAutomationStateLocked(ctx); err != nil {
		observability.Warn("reset automation state failed", "roomID", r.record.RoomID, "reason", reason, "error", err)
		return
	}
	observability.Info("automation state reset", "roomID", r.record.RoomID, "reason", reason)
	r.rearmScheduledLocked(ctx, traceID)
}

func (r *RoomActor) broadcast(ctx context.Context, traceID string) error {
	presences, err := r.host.store.ListPresence(ctx, r.record.RoomID)
	if err != nil {
		return err
	}
	var outboundErr error
	playerProfiles, err := r.loadPlayerProfiles(ctx)
	if err != nil {
		observability.Warn("load player profiles for broadcast failed", "roomID", r.record.RoomID, "error", err)
	}
	if err := r.syncRosterLocked(ctx, presences); err != nil {
		observability.Warn("sync match roster failed", "roomID", r.record.RoomID, "error", err)
		outboundErr = errors.Join(outboundErr, err)
	}
	if err := r.syncProgressLocked(ctx); err != nil {
		observability.Warn("sync room progress failed", "roomID", r.record.RoomID, "error", err)
		outboundErr = errors.Join(outboundErr, err)
	}
	for _, p := range presences {
		if !p.Online {
			continue
		}
		payload, err := r.definition.Engine.BuildPlayerView(ctx, r.state, game.ViewRequest{
			RoomID:         r.record.RoomID,
			MatchID:        r.match.MatchID,
			GameType:       r.record.GameType,
			PlayerID:       p.PlayerID,
			Visibility:     r.record.Visibility,
			MaxPlayers:     r.record.MaxPlayers,
			Seq:            r.seq,
			PlayerProfiles: playerProfiles,
		})
		if err != nil {
			observability.Warn("build player view failed", "roomID", r.record.RoomID, "playerID", p.PlayerID, "error", err)
			outboundErr = errors.Join(outboundErr, err)
			continue
		}
		payload = realtimePlayerView(
			payload,
			r.record.GameType,
			r.record.RoomID,
			r.match.MatchID,
			r.seq,
			nil,
		)
		out := contracts.OutboundMessage{
			SchemaVersion:  contracts.RealtimeSchemaVersion,
			MessageID:      uuid.NewString(),
			TraceID:        traceID,
			RoomID:         r.record.RoomID,
			MatchID:        r.match.MatchID,
			Seq:            r.seq,
			Epoch:          r.record.Epoch,
			OwnerNode:      r.host.nodeID,
			TargetUserID:   p.UserID,
			TargetPlayerID: p.PlayerID,
			TargetConn:     p.ConnectionRef,
			Reliable:       true,
			Payload:        payload,
			CreatedAt:      time.Now().UTC(),
		}
		if err := r.host.store.SaveOutboundMessage(ctx, out); err != nil {
			observability.Warn("save outbound failed", "roomID", r.record.RoomID, "sessionID", p.SessionID, "error", err)
			outboundErr = errors.Join(outboundErr, err)
			continue
		}
		if err := r.host.router.Deliver(out); err != nil {
			observability.Warn("deliver failed", "roomID", r.record.RoomID, "sessionID", p.SessionID, "error", err)
			outboundErr = errors.Join(outboundErr, err)
		} else {
			observability.Info("broadcast queued", "nodeID", r.host.nodeID, "roomID", r.record.RoomID, "sessionID", p.SessionID, "playerID", p.PlayerID, "gatewayNode", p.GatewayNode, "messageID", out.MessageID, "seq", out.Seq)
			_ = r.host.store.SaveRuntimeEvent(ctx, contracts.RuntimeEvent{
				RoomID:    r.record.RoomID,
				MatchID:   r.match.MatchID,
				TraceID:   traceID,
				NodeID:    r.host.nodeID,
				Component: "roomhost",
				Event:     "broadcast_queued",
				Message:   "roomhost queued outbound message to gateway",
				Payload: map[string]any{
					"sessionID":   p.SessionID,
					"playerID":    p.PlayerID,
					"gatewayNode": p.GatewayNode,
					"messageID":   out.MessageID,
					"seq":         out.Seq,
				},
			})
		}
	}
	if err := r.host.store.SetRoom(ctx, r.record); err != nil {
		outboundErr = errors.Join(outboundErr, err)
	}
	return outboundErr
}

func (r *RoomActor) loadPlayerProfiles(ctx context.Context) (map[string]game.PlayerProfile, error) {
	if r.match.MatchID == "" {
		return map[string]game.PlayerProfile{}, nil
	}
	players, err := r.host.store.ListMatchPlayers(ctx, r.match.MatchID)
	if err != nil {
		return nil, err
	}
	profiles := make(map[string]game.PlayerProfile, len(players))
	for _, player := range players {
		if player.PlayerID == "" {
			continue
		}
		displayName := player.DisplayName
		if displayName == "" {
			displayName = player.Name
		}
		profiles[player.PlayerID] = game.PlayerProfile{
			UserID:      player.UserID,
			DisplayName: displayName,
			Avatar:      player.Avatar,
		}
	}
	return profiles, nil
}

func (r *RoomActor) syncRosterLocked(ctx context.Context, presences []contracts.PresenceRecord) error {
	provider, ok := r.definition.Engine.(game.RosterProvider)
	if !ok {
		players := make(map[string]struct{}, len(presences))
		for _, presence := range presences {
			if presence.PlayerID != "" {
				players[presence.PlayerID] = struct{}{}
			}
		}
		r.record.PlayerCount = len(players)
		return nil
	}
	roster, err := provider.Roster(ctx, r.state)
	if err != nil {
		return err
	}
	players := make([]contracts.MatchPlayerRecord, 0, len(roster))
	now := time.Now().UTC()
	presenceUserIDs := make(map[string]string, len(presences))
	for _, presence := range presences {
		if presence.PlayerID != "" && presence.UserID != "" {
			presenceUserIDs[presence.PlayerID] = presence.UserID
		}
	}
	for _, player := range roster {
		userID := player.UserID
		if !player.IsAI && presenceUserIDs[player.PlayerID] != "" {
			userID = presenceUserIDs[player.PlayerID]
		}
		players = append(players, contracts.MatchPlayerRecord{
			UserID:      userID,
			PlayerID:    player.PlayerID,
			DisplayName: player.DisplayName,
			Online:      player.Online,
			IsAI:        player.IsAI,
			SeatIndex:   player.SeatIndex,
			Ready:       player.Ready,
			JoinedAt:    now,
			LastSeen:    now,
		})
	}
	if err := r.host.store.SyncMatchRoster(ctx, r.match.MatchID, players); err != nil {
		return err
	}
	r.record.PlayerCount = len(roster)
	return nil
}

func (r *RoomActor) syncProgressLocked(ctx context.Context) error {
	provider, ok := r.definition.Engine.(game.ProgressProvider)
	if !ok {
		return nil
	}
	progress, err := provider.Progress(ctx, r.state)
	if err != nil {
		return err
	}
	r.record.ProgressCurrent = progress.Current
	r.record.ProgressTotal = progress.Total
	return nil
}

func (r *RoomActor) syncRoster(ctx context.Context) error {
	presences, err := r.host.store.ListPresence(ctx, r.record.RoomID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	if err := r.syncRosterLocked(ctx, presences); err != nil {
		r.mu.Unlock()
		return err
	}
	if err := r.syncProgressLocked(ctx); err != nil {
		r.mu.Unlock()
		return err
	}
	record := r.record
	r.mu.Unlock()
	return r.host.store.SetRoom(ctx, record)
}

func (r *RoomActor) sendError(ctx context.Context, cmd contracts.RoomCommand, ack contracts.CommandAck) error {
	out := contracts.OutboundMessage{
		SchemaVersion: contracts.RealtimeSchemaVersion,
		MessageID:     uuid.NewString(),
		TraceID:       cmd.TraceID,
		RoomID:        cmd.RoomID,
		MatchID:       cmd.MatchID,
		Epoch:         r.record.Epoch,
		OwnerNode:     r.host.nodeID,
		TargetConn:    cmd.ConnectionRef,
		Reliable:      false,
		Payload: contracts.ErrorMessage{
			SchemaVersion: contracts.RealtimeSchemaVersion,
			Type:          contracts.MessageTypeError,
			RoomID:        cmd.RoomID,
			MatchID:       cmd.MatchID,
			Seq:           r.seq,
			CommandID:     cmd.CommandID,
			Code:          ack.ErrorCode,
			Message:       ack.Message,
		},
		CreatedAt: time.Now().UTC(),
	}
	return r.host.router.Deliver(out)
}

func realtimePlayerView(
	payload any,
	gameType,
	roomID,
	matchID string,
	seq int64,
	effects []map[string]any,
) any {
	view, ok := payload.(map[string]any)
	if !ok {
		encoded, err := json.Marshal(payload)
		if err != nil || json.Unmarshal(encoded, &view) != nil {
			return payload
		}
	}
	messageType, _ := view["type"].(string)
	if messageType == contracts.MessageTypeMatchSync {
		result := make(map[string]any, len(view)+4)
		for key, value := range view {
			result[key] = value
		}
		result["schemaVersion"] = contracts.RealtimeSchemaVersion
		result["roomID"] = roomID
		result["matchID"] = matchID
		result["seq"] = seq
		return result
	}
	delete(view, "type")
	delete(view, "roomID")
	delete(view, "matchID")
	delete(view, "seq")
	result := map[string]any{
		"schemaVersion": contracts.RealtimeSchemaVersion,
		"type":          contracts.MessageTypeRoomSync,
		"roomID":        roomID,
		"matchID":       matchID,
		"seq":           seq,
		"view":          map[string]any{gameType: view},
	}
	if len(effects) > 0 {
		result["effects"] = effects
	}
	return result
}
