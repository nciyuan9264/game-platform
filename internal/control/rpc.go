package control

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nciyuan9264/game-platform-server/internal/authclient"
	"github.com/nciyuan9264/game-platform-server/internal/contracts"
	"github.com/nciyuan9264/game-platform-server/internal/rpcmeta"
	"github.com/nciyuan9264/game-platform-server/internal/store"
	platformcontrol "github.com/nciyuan9264/game-platform-server/kitex_gen/pam/platform/control/v1"
)

type RPCHandler struct {
	server *Server
}

func NewRPCHandler(server *Server) *RPCHandler {
	return &RPCHandler{server: server}
}

func (h *RPCHandler) CreateRoom(ctx context.Context, req *platformcontrol.CreateRoomReq) (*platformcontrol.CreateRoomResp, error) {
	if h == nil || h.server == nil || req == nil {
		return nil, controlErr("invalid_request", errors.New("request is required"))
	}
	identity, err := h.callerIdentity(ctx, false)
	if err != nil {
		return nil, controlErr("forbidden", err)
	}
	ownerUserID := identity.UserID
	if hasRole(identity.Roles, "admin") &&
		strings.HasPrefix(req.GetOwnerUserId(), "lt-") &&
		strings.HasPrefix(req.GetRequestId(), "lt-") {
		ownerUserID = req.GetOwnerUserId()
	}
	ownerIdentity := identity
	ownerIdentity.UserID = ownerUserID
	room, err := h.server.createRoomRecord(ctx, createRoomRequest{
		GameType:      req.GetGameType(),
		OwnerUserID:   ownerUserID,
		OwnerPlayerID: ownerIdentity.UserID,
		MaxPlayers:    int(req.GetMaxPlayers()),
		Visibility:    req.GetVisibility(),
		RequestID:     req.GetRequestId(),
	})
	if err != nil {
		code := "create_room_failed"
		switch {
		case errors.Is(err, errNoRoomHost):
			code = "no_roomhost"
		case errors.Is(err, store.ErrRoomQuotaExceeded):
			code = "too_many_rooms"
		case errors.Is(err, store.ErrIdempotencyConflict):
			code = "idempotency_conflict"
		case errors.Is(err, errRoomCreationInProgress):
			code = "creation_in_progress"
		}
		return nil, controlErr(code, err)
	}
	return &platformcontrol.CreateRoomResp{Room: toRPCRoom(room)}, nil
}

func (h *RPCHandler) StartMatch(ctx context.Context, req *platformcontrol.StartMatchReq) (*platformcontrol.StartMatchResp, error) {
	if h == nil || h.server == nil || req == nil || req.GetRoomId() == "" {
		return nil, controlErr("invalid_request", errors.New("room_id is required"))
	}
	identity, err := h.callerIdentity(ctx, false)
	if err != nil {
		return nil, controlErr("forbidden", err)
	}
	room, err := h.server.store.GetRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, controlErr("store_error", err)
	}
	actorIsAdmin := false
	for _, role := range identity.Roles {
		if role == "admin" {
			actorIsAdmin = true
			break
		}
	}
	if identity.UserID != room.OwnerUserID && !actorIsAdmin {
		return nil, controlErr("forbidden", errors.New("only the room owner or an administrator can start a match"))
	}
	room, match, err := h.server.startNextMatch(ctx, room.RoomID, req.GetRequestId())
	if err != nil {
		code := "start_match_failed"
		switch {
		case errors.Is(err, store.ErrMatchActive):
			code = "match_active"
		case errors.Is(err, store.ErrRoomNotOpen):
			code = "room_not_open"
		}
		return nil, controlErr(code, err)
	}
	return &platformcontrol.StartMatchResp{
		Success: true,
		Room:    toRPCRoom(room),
		MatchId: match.MatchID,
		RoundNo: int32(match.RoundNo),
	}, nil
}

func (h *RPCHandler) CloseRoom(ctx context.Context, req *platformcontrol.RoomActionReq) (*platformcontrol.RoomActionResp, error) {
	return h.finishRoom(ctx, req, contracts.RoomStatusClosed, false)
}

func (h *RPCHandler) InterruptRoom(ctx context.Context, req *platformcontrol.RoomActionReq) (*platformcontrol.RoomActionResp, error) {
	return h.finishRoom(ctx, req, contracts.RoomStatusClosed, true)
}

func (h *RPCHandler) DrainRoomHost(ctx context.Context, req *platformcontrol.DrainRoomHostReq) (*platformcontrol.DrainRoomHostResp, error) {
	if h == nil || h.server == nil || req == nil || req.GetNodeId() == "" {
		return nil, controlErr("invalid_request", errors.New("node_id is required"))
	}
	identity, err := h.callerIdentity(ctx, true)
	if err != nil {
		return nil, controlErr("forbidden", err)
	}
	rooms, err := h.server.store.ListRooms(ctx, "")
	if err != nil {
		return nil, controlErr("store_error", err)
	}
	affected := 0
	for _, room := range rooms {
		if room.OwnerNode != req.GetNodeId() || contracts.IsTerminalRoomStatus(room.Status) {
			continue
		}
		_, err := h.finishRoomAs(ctx, &platformcontrol.RoomActionReq{
			RoomId:      room.RoomID,
			ActorUserId: identity.UserID,
			Reason:      req.Reason,
		}, contracts.RoomStatusClosed, identity)
		if err != nil {
			return nil, err
		}
		affected++
	}
	return &platformcontrol.DrainRoomHostResp{Success: true, AffectedRooms: int32(affected)}, nil
}

func (h *RPCHandler) finishRoom(ctx context.Context, req *platformcontrol.RoomActionReq, status string, internalOnly bool) (*platformcontrol.RoomActionResp, error) {
	if h == nil || h.server == nil || req == nil || req.GetRoomId() == "" {
		return nil, controlErr("invalid_request", errors.New("room_id is required"))
	}
	identity, err := h.callerIdentity(ctx, internalOnly)
	if err != nil {
		return nil, controlErr("forbidden", err)
	}
	return h.finishRoomAs(ctx, req, status, identity)
}

func (h *RPCHandler) finishRoomAs(ctx context.Context, req *platformcontrol.RoomActionReq, status string, identity contracts.UserIdentity) (*platformcontrol.RoomActionResp, error) {
	room, err := h.server.store.GetRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, controlErr("store_error", err)
	}
	if contracts.IsTerminalRoomStatus(room.Status) {
		return &platformcontrol.RoomActionResp{Success: true, Room: toRPCRoom(room)}, nil
	}
	actorIsAdmin := false
	actorIsSystem := false
	for _, role := range identity.Roles {
		if role == "admin" {
			actorIsAdmin = true
		}
		if role == "system" {
			actorIsSystem = true
		}
	}
	if identity.UserID != room.OwnerUserID && !actorIsAdmin && !actorIsSystem {
		return nil, controlErr("forbidden", errors.New("only the room owner or an internal controller can close this room"))
	}
	actorUserID := identity.UserID
	if h.server.roomCommands != nil {
		cmd := contracts.RoomCommand{
			SchemaVersion: 1,
			CommandID:     uuid.NewString(),
			TraceID:       uuid.NewString(),
			RoomID:        room.RoomID,
			GameType:      room.GameType,
			Epoch:         room.Epoch,
			UserID:        actorUserID,
			PlayerID:      actorUserID,
			Type:          contracts.RuntimeCommandRoomClose,
			Payload: map[string]any{
				"status": status,
				"reason": req.GetReason(),
			},
			SentAt: time.Now().UTC(),
		}
		ack, sendErr := h.server.roomCommands.SendRoomCommand(ctx, room.OwnerNode, cmd, 3*time.Second)
		if sendErr != nil {
			return nil, controlErr("runtime_error", sendErr)
		}
		if !ack.Accepted {
			return nil, controlErr("runtime_error", errors.New(ack.Message))
		}
	}
	room, err = h.server.store.UpdateRoom(ctx, req.GetRoomId(), func(record *contracts.RoomRecord) error {
		record.Status = status
		record.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		return nil, controlErr("store_error", err)
	}
	return &platformcontrol.RoomActionResp{Success: true, Room: toRPCRoom(room)}, nil
}

func (h *RPCHandler) callerIdentity(ctx context.Context, internalOnly bool) (contracts.UserIdentity, error) {
	if userID, roles, ok := rpcmeta.UserIdentityFromContext(ctx); ok {
		if !internalOnly || hasRole(roles, "admin") {
			return contracts.UserIdentity{UserID: userID, Roles: roles}, nil
		}
		return contracts.UserIdentity{}, errors.New("administrator role is required")
	}
	token, ok := rpcmeta.UserTokenFromContext(ctx)
	if !ok || h.server.adminAuth == nil {
		return contracts.UserIdentity{}, errors.New("verified user token is required")
	}
	result, err := h.server.adminAuth.VerifyToken(ctx, authclient.VerifyTokenRequest{
		AccessToken:      token,
		RequiredAudience: "game-platform",
	})
	if err != nil {
		return contracts.UserIdentity{}, err
	}
	if !result.Valid || result.User.UserID == "" {
		return contracts.UserIdentity{}, errors.New("invalid user token")
	}
	if internalOnly && !hasRole(result.User.Roles, "admin") {
		return contracts.UserIdentity{}, errors.New("administrator role is required")
	}
	return result.User, nil
}

func hasRole(roles []string, required string) bool {
	for _, role := range roles {
		if role == required {
			return true
		}
	}
	return false
}

func toRPCRoom(room contracts.RoomRecord) *platformcontrol.RoomRecord {
	out := &platformcontrol.RoomRecord{
		RoomId:        room.RoomID,
		GameType:      room.GameType,
		OwnerUserId:   room.OwnerUserID,
		OwnerPlayerId: room.OwnerPlayerID,
		OwnerNode:     room.OwnerNode,
		Epoch:         room.Epoch,
		Status:        room.Status,
		MaxPlayers:    int32(room.MaxPlayers),
		PlayerCount:   int32(room.PlayerCount),
		EngineVersion: room.EngineVersion,
		NextRoundNo:   int32(room.NextRoundNo),
		Visibility:    &room.Visibility,
		CreatedAtUnix: room.CreatedAt.Unix(),
		UpdatedAtUnix: room.UpdatedAt.Unix(),
	}
	if !room.LeaseExpiresAt.IsZero() {
		value := room.LeaseExpiresAt.Unix()
		out.LeaseExpiresAtUnix = &value
	}
	if room.CurrentMatchID != "" {
		out.CurrentMatchId = &room.CurrentMatchID
	}
	return out
}

func controlErr(code string, err error) *platformcontrol.ControlError {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return &platformcontrol.ControlError{Code: code, Message: message}
}
