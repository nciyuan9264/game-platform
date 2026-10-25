package controlclient

import (
	"context"
	"time"

	kitexclient "github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/transmeta"
	kitextransport "github.com/cloudwego/kitex/transport"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/rpcmeta"
	"github.com/nciyuan9264/game-platform/internal/servicediscovery"
	platformcontrol "github.com/nciyuan9264/game-platform/kitex_gen/pam/platform/control/v1"
	platformcontrolservice "github.com/nciyuan9264/game-platform/kitex_gen/pam/platform/control/v1/platformcontrolservice"
)

type Client struct {
	rpc platformcontrolservice.Client
}

func New(addr string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	options := []kitexclient.Option{
		kitexclient.WithRPCTimeout(timeout),
		kitexclient.WithConnectTimeout(timeout),
		kitexclient.WithTransportProtocol(kitextransport.TTHeader),
		kitexclient.WithMetaHandler(transmeta.ClientTTHeaderHandler),
		kitexclient.WithMiddleware(rpcmeta.ClientMiddleware),
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
	psm, err := servicediscovery.ServiceName("CONTROL_RPC_PSM", "game.platform.control")
	if err != nil {
		return nil, err
	}
	rpc, err := platformcontrolservice.NewClient(psm, options...)
	if err != nil {
		return nil, err
	}
	return &Client{rpc: rpc}, nil
}

func (c *Client) CreateRoom(ctx context.Context, gameType, ownerUserID, ownerPlayerID, requestID string, maxPlayers int) (contracts.RoomRecord, error) {
	return c.CreateRoomWithVisibility(
		ctx,
		gameType,
		ownerUserID,
		ownerPlayerID,
		requestID,
		maxPlayers,
		contracts.VisibilityPublic,
	)
}

func (c *Client) CreateRoomWithVisibility(
	ctx context.Context,
	gameType,
	ownerUserID,
	ownerPlayerID,
	requestID string,
	maxPlayers int,
	visibility string,
) (contracts.RoomRecord, error) {
	return c.CreateRoomWithSettings(
		ctx,
		gameType,
		ownerUserID,
		ownerPlayerID,
		requestID,
		maxPlayers,
		visibility,
		"",
		0,
	)
}

func (c *Client) CreateRoomWithSettings(
	ctx context.Context,
	gameType,
	ownerUserID,
	ownerPlayerID,
	requestID string,
	maxPlayers int,
	visibility,
	displayName string,
	turnTimeoutSeconds int,
) (contracts.RoomRecord, error) {
	req := &platformcontrol.CreateRoomReq{
		GameType:      gameType,
		OwnerUserId:   ownerUserID,
		OwnerPlayerId: ownerPlayerID,
	}
	if requestID != "" {
		req.RequestId = &requestID
	}
	if maxPlayers > 0 {
		value := int32(maxPlayers)
		req.MaxPlayers = &value
	}
	if visibility != "" {
		req.Visibility = &visibility
	}
	if displayName != "" {
		req.DisplayName = &displayName
	}
	value := int32(turnTimeoutSeconds)
	req.TurnTimeoutSeconds = &value
	resp, err := c.rpc.CreateRoom(ctx, req)
	if err != nil {
		return contracts.RoomRecord{}, err
	}
	return fromRPCRoom(resp.GetRoom()), nil
}

func (c *Client) StartMatch(ctx context.Context, roomID, actorUserID string, actorRoles []string, requestID string) (contracts.RoomRecord, string, int, error) {
	req := &platformcontrol.StartMatchReq{
		RoomId:      roomID,
		ActorUserId: actorUserID,
	}
	if len(actorRoles) > 0 {
		req.ActorRoles = append([]string(nil), actorRoles...)
	}
	if requestID != "" {
		req.RequestId = &requestID
	}
	resp, err := c.rpc.StartMatch(ctx, req)
	if err != nil {
		return contracts.RoomRecord{}, "", 0, err
	}
	return fromRPCRoom(resp.GetRoom()), resp.GetMatchId(), int(resp.GetRoundNo()), nil
}

func (c *Client) CloseRoom(ctx context.Context, roomID, actorUserID string, actorRoles []string, reason string) (contracts.RoomRecord, error) {
	resp, err := c.rpc.CloseRoom(ctx, roomActionRequest(roomID, actorUserID, actorRoles, reason))
	if err != nil {
		return contracts.RoomRecord{}, err
	}
	return fromRPCRoom(resp.GetRoom()), nil
}

func (c *Client) InterruptRoom(ctx context.Context, roomID, actorUserID string, actorRoles []string, reason string) (contracts.RoomRecord, error) {
	resp, err := c.rpc.InterruptRoom(ctx, roomActionRequest(roomID, actorUserID, actorRoles, reason))
	if err != nil {
		return contracts.RoomRecord{}, err
	}
	return fromRPCRoom(resp.GetRoom()), nil
}

func roomActionRequest(roomID, actorUserID string, actorRoles []string, reason string) *platformcontrol.RoomActionReq {
	req := &platformcontrol.RoomActionReq{RoomId: roomID, ActorUserId: actorUserID}
	if len(actorRoles) > 0 {
		req.ActorRoles = append([]string(nil), actorRoles...)
	}
	if reason != "" {
		req.Reason = &reason
	}
	return req
}

func fromRPCRoom(room *platformcontrol.RoomRecord) contracts.RoomRecord {
	if room == nil {
		return contracts.RoomRecord{}
	}
	out := contracts.RoomRecord{
		RoomID:             room.GetRoomId(),
		GameType:           room.GetGameType(),
		OwnerUserID:        room.GetOwnerUserId(),
		OwnerPlayerID:      room.GetOwnerPlayerId(),
		OwnerNode:          room.GetOwnerNode(),
		Epoch:              room.GetEpoch(),
		Status:             room.GetStatus(),
		MaxPlayers:         int(room.GetMaxPlayers()),
		PlayerCount:        int(room.GetPlayerCount()),
		EngineVersion:      room.GetEngineVersion(),
		CurrentMatchID:     room.GetCurrentMatchId(),
		NextRoundNo:        int(room.GetNextRoundNo()),
		Visibility:         room.GetVisibility(),
		DisplayName:        room.GetDisplayName(),
		TurnTimeoutSeconds: int(room.GetTurnTimeoutSeconds()),
		CreatedAt:          time.Unix(room.GetCreatedAtUnix(), 0).UTC(),
		UpdatedAt:          time.Unix(room.GetUpdatedAtUnix(), 0).UTC(),
	}
	if room.IsSetLeaseExpiresAtUnix() {
		out.LeaseExpiresAt = time.Unix(room.GetLeaseExpiresAtUnix(), 0).UTC()
	}
	return out
}
