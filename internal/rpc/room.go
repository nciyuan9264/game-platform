package rpc

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	platform "github.com/nciyuan9264/game-platform-client/kitex_gen/pam/game/platform/v1"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/store"
	platformcontrol "github.com/nciyuan9264/game-platform/kitex_gen/pam/platform/control/v1"
)

func (h *Handler) CreateRoom(ctx context.Context, req *platform.CreateRoomReq) (*platform.RoomResp, error) {
	user, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.GetRequestId()) == "" {
		return nil, platformError("invalid_request", errors.New("requestID is required"))
	}
	gameType, err := h.validGameTypeForCreate(ctx, req.GetGameType())
	if err != nil {
		return nil, err
	}
	visibility, err := validVisibility(req.GetVisibility())
	if err != nil {
		return nil, err
	}
	if h.rooms == nil {
		return nil, platformError("control_unavailable", errors.New("control plane is unavailable"))
	}
	turnTimeoutSeconds, err := createRoomTurnTimeoutSeconds(gameType, req)
	if err != nil {
		return nil, err
	}
	var room contracts.RoomRecord
	var createErr error
	if creator, ok := h.rooms.(interface {
		CreateRoomWithSettings(
			context.Context,
			string,
			string,
			string,
			string,
			int,
			string,
			int,
		) (contracts.RoomRecord, error)
	}); ok {
		room, createErr = creator.CreateRoomWithSettings(
			ctx,
			gameType,
			user.UserID,
			user.UserID,
			req.GetRequestId(),
			int(req.GetMaxPlayers()),
			visibility,
			turnTimeoutSeconds,
		)
	} else {
		room, createErr = h.rooms.CreateRoomWithVisibility(
			ctx,
			gameType,
			user.UserID,
			user.UserID,
			req.GetRequestId(),
			int(req.GetMaxPlayers()),
			visibility,
		)
	}
	if createErr != nil {
		return nil, mapRoomError(createErr)
	}
	room.Visibility = visibility
	info, err := h.roomInfo(ctx, room)
	if err != nil {
		return nil, err
	}
	h.resolveRoomProfiles(ctx, []*platform.RoomInfo{info})
	return &platform.RoomResp{StatusCode: http.StatusOK, Message: "ok", Data: info}, nil
}

func createRoomTurnTimeoutSeconds(gameType string, req *platform.CreateRoomReq) (int, error) {
	turnTimeoutSeconds := int(req.GetTurnTimeoutSeconds())
	if gameType == contracts.GameFugitive {
		if req.IsSetTurnTimeoutSeconds() &&
			turnTimeoutSeconds != 120 {
			return 0, platformError(
				"invalid_request",
				errors.New("fugitive turnTimeoutSeconds is fixed at 120"),
			)
		}
		return 120, nil
	}
	if gameType == contracts.GameWarChest {
		if req.IsSetTurnTimeoutSeconds() &&
			turnTimeoutSeconds != 90 {
			return 0, platformError(
				"invalid_request",
				errors.New("warchest turnTimeoutSeconds is fixed at 90"),
			)
		}
		return 90, nil
	}
	if gameType == contracts.GameQuoridor {
		if req.IsSetTurnTimeoutSeconds() &&
			turnTimeoutSeconds != 60 {
			return 0, platformError(
				"invalid_request",
				errors.New("quoridor turnTimeoutSeconds is fixed at 60"),
			)
		}
		return 60, nil
	}
	if gameType == contracts.GameHex {
		if req.IsSetTurnTimeoutSeconds() &&
			turnTimeoutSeconds != 60 {
			return 0, platformError(
				"invalid_request",
				errors.New("hex turnTimeoutSeconds is fixed at 60"),
			)
		}
		return 60, nil
	}
	if gameType == contracts.GameLoveLetter {
		if req.IsSetTurnTimeoutSeconds() &&
			turnTimeoutSeconds != 60 {
			return 0, platformError(
				"invalid_request",
				errors.New("loveletter turnTimeoutSeconds is fixed at 60"),
			)
		}
		return 60, nil
	}
	if gameType == contracts.GameSixNimmt {
		if req.IsSetTurnTimeoutSeconds() &&
			turnTimeoutSeconds != 60 {
			return 0, platformError(
				"invalid_request",
				errors.New("sixnimmt turnTimeoutSeconds is fixed at 60"),
			)
		}
		return 60, nil
	}
	if gameType == contracts.GameForSale {
		if req.IsSetTurnTimeoutSeconds() &&
			turnTimeoutSeconds != 60 {
			return 0, platformError(
				"invalid_request",
				errors.New("forsale turnTimeoutSeconds is fixed at 60"),
			)
		}
		return 60, nil
	}
	if gameType == contracts.GameOnitama {
		if req.IsSetTurnTimeoutSeconds() &&
			turnTimeoutSeconds != 60 {
			return 0, platformError(
				"invalid_request",
				errors.New("onitama turnTimeoutSeconds is fixed at 60"),
			)
		}
		return 60, nil
	}
	if gameType == contracts.GameHive {
		if req.IsSetTurnTimeoutSeconds() &&
			turnTimeoutSeconds != 120 {
			return 0, platformError(
				"invalid_request",
				errors.New("hive turnTimeoutSeconds is fixed at 120"),
			)
		}
		return 120, nil
	}
	if gameType == contracts.GameCantStop {
		if req.IsSetTurnTimeoutSeconds() &&
			turnTimeoutSeconds != 60 {
			return 0, platformError(
				"invalid_request",
				errors.New("cantstop turnTimeoutSeconds is fixed at 60"),
			)
		}
		return 60, nil
	}
	if turnTimeoutSeconds != 0 &&
		turnTimeoutSeconds != 60 &&
		turnTimeoutSeconds != 90 {
		return 0, platformError(
			"invalid_request",
			errors.New("turnTimeoutSeconds must be 0, 60, or 90"),
		)
	}
	return turnTimeoutSeconds, nil
}

func (h *Handler) ListRooms(ctx context.Context, req *platform.ListRoomsReq) (*platform.RoomListResp, error) {
	user, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, platformError("invalid_request", errors.New("request is required"))
	}
	gameType, err := h.validGameType(ctx, req.GetGameType())
	if err != nil {
		return nil, err
	}
	rooms, err := h.store.ListRooms(ctx, gameType)
	if err != nil {
		return nil, platformError("store_error", err)
	}
	result := make([]*platform.RoomInfo, 0, len(rooms))
	for _, room := range rooms {
		if contracts.IsTerminalRoomStatus(room.Status) {
			continue
		}
		info, err := h.roomInfo(ctx, room)
		if err != nil {
			return nil, err
		}
		if info.Visibility == contracts.VisibilityPrivate && !roomVisibleTo(info, user.UserID) {
			continue
		}
		result = append(result, info)
	}
	h.resolveRoomProfiles(ctx, result)
	return &platform.RoomListResp{
		StatusCode: http.StatusOK,
		Message:    "ok",
		Data:       &platform.RoomListData{Rooms: result},
	}, nil
}

func (h *Handler) GetRoom(ctx context.Context, req *platform.GetRoomReq) (*platform.RoomResp, error) {
	if _, err := requireIdentity(ctx); err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.GetRoomId()) == "" {
		return nil, platformError("invalid_request", errors.New("roomID is required"))
	}
	gameType, err := h.validGameType(ctx, req.GetGameType())
	if err != nil {
		return nil, err
	}
	room, err := h.store.GetRoom(ctx, req.GetRoomId())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, platformError("not_found", errors.New("room not found"))
	}
	if err != nil {
		return nil, platformError("store_error", err)
	}
	if room.GameType != gameType || contracts.IsTerminalRoomStatus(room.Status) {
		return nil, platformError("not_found", errors.New("room not found"))
	}
	info, err := h.roomInfo(ctx, room)
	if err != nil {
		return nil, err
	}
	h.resolveRoomProfiles(ctx, []*platform.RoomInfo{info})
	return &platform.RoomResp{StatusCode: http.StatusOK, Message: "ok", Data: info}, nil
}

func (h *Handler) CloseRoom(ctx context.Context, req *platform.CloseRoomReq) (*platform.RoomResp, error) {
	user, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || req.GetRoomId() == "" || req.GetRequestId() == "" {
		return nil, platformError("invalid_request", errors.New("roomID and requestID are required"))
	}
	gameType, err := h.validGameType(ctx, req.GetGameType())
	if err != nil {
		return nil, err
	}
	room, err := h.authorizedRoom(ctx, req.GetRoomId(), gameType, user)
	if err != nil {
		return nil, err
	}
	if h.rooms == nil {
		return nil, platformError("control_unavailable", errors.New("control plane is unavailable"))
	}
	room, err = h.rooms.CloseRoom(ctx, room.RoomID, user.UserID, user.Roles, "user_closed")
	if err != nil {
		return nil, mapRoomError(err)
	}
	info, err := h.roomInfo(ctx, room)
	if err != nil {
		return nil, err
	}
	h.resolveRoomProfiles(ctx, []*platform.RoomInfo{info})
	return &platform.RoomResp{StatusCode: http.StatusOK, Message: "ok", Data: info}, nil
}

func (h *Handler) StartNextMatch(ctx context.Context, req *platform.StartNextMatchReq) (*platform.StartNextMatchResp, error) {
	user, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || req.GetRoomId() == "" || req.GetRequestId() == "" {
		return nil, platformError("invalid_request", errors.New("roomID and requestID are required"))
	}
	gameType, err := h.validGameType(ctx, req.GetGameType())
	if err != nil {
		return nil, err
	}
	room, err := h.authorizedRoom(ctx, req.GetRoomId(), gameType, user)
	if err != nil {
		return nil, err
	}
	if h.rooms == nil {
		return nil, platformError("control_unavailable", errors.New("control plane is unavailable"))
	}
	room, matchID, roundNo, err := h.rooms.StartMatch(
		ctx,
		room.RoomID,
		user.UserID,
		user.Roles,
		req.GetRequestId(),
	)
	if err != nil {
		return nil, mapRoomError(err)
	}
	info, err := h.roomInfo(ctx, room)
	if err != nil {
		return nil, err
	}
	h.resolveRoomProfiles(ctx, []*platform.RoomInfo{info})
	return &platform.StartNextMatchResp{
		StatusCode: http.StatusOK,
		Message:    "ok",
		Data: &platform.StartNextMatchData{
			Room:    info,
			MatchID: matchID,
			RoundNo: int32(roundNo),
		},
	}, nil
}

func (h *Handler) validGameType(ctx context.Context, value string) (string, error) {
	value = strings.TrimSpace(value)
	if h.gameCatalog == nil {
		return "", platformError("store_error", errors.New("game catalog is unavailable"))
	}
	if _, err := h.gameCatalog.GetGameCatalogEntry(ctx, value); err != nil {
		if errors.Is(err, store.ErrGameCatalogNotFound) {
			return "", platformError("invalid_game_type", errors.New("gameType is not registered"))
		}
		return "", platformError("store_error", err)
	}
	return value, nil
}

func (h *Handler) validGameTypeForCreate(ctx context.Context, value string) (string, error) {
	value, err := h.validGameType(ctx, value)
	if err != nil {
		return "", err
	}
	entry, err := h.gameCatalog.GetGameCatalogEntry(ctx, value)
	if err != nil {
		return "", platformError("store_error", err)
	}
	if !entry.EnabledForNewRooms {
		return "", platformError("game_disabled", errors.New("game is disabled for new rooms"))
	}
	return value, nil
}

func validVisibility(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return contracts.VisibilityPublic, nil
	}
	if value != contracts.VisibilityPublic && value != contracts.VisibilityPrivate {
		return "", platformError("invalid_visibility", errors.New("visibility must be public or private"))
	}
	return value, nil
}

func requireIdentity(ctx context.Context) (identity, error) {
	value, ok := authenticatedIdentity(ctx)
	if !ok {
		return identity{}, platformError("unauthorized", errors.New("authenticated user identity is required"))
	}
	return value, nil
}

func (h *Handler) authorizedRoom(ctx context.Context, roomID, gameType string, user identity) (contracts.RoomRecord, error) {
	room, err := h.store.GetRoom(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && room.GameType != gameType) {
		return contracts.RoomRecord{}, platformError("not_found", errors.New("room not found"))
	}
	if err != nil {
		return contracts.RoomRecord{}, platformError("store_error", err)
	}
	if room.OwnerUserID != user.UserID && !hasRole(user.Roles, "admin") {
		return contracts.RoomRecord{}, platformError("forbidden", errors.New("only the room owner or an administrator may perform this action"))
	}
	return room, nil
}

func mapRoomError(err error) error {
	var controlErr *platformcontrol.ControlError
	if errors.As(err, &controlErr) {
		return platformError(controlErr.GetCode(), errors.New(controlErr.GetMessage()))
	}
	switch {
	case errors.Is(err, store.ErrRoomQuotaExceeded):
		return platformError("too_many_rooms", err)
	case errors.Is(err, store.ErrIdempotencyConflict):
		return platformError("idempotency_conflict", err)
	case errors.Is(err, store.ErrMatchActive):
		return platformError("match_active", err)
	case errors.Is(err, store.ErrRoomNotOpen):
		return platformError("room_not_open", err)
	default:
		return platformError("control_unavailable", err)
	}
}
