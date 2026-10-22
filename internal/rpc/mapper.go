package rpc

import (
	"context"
	"database/sql"
	"errors"
	"time"

	platform "github.com/nciyuan9264/game-platform-client/kitex_gen/pam/game/platform/v1"
	"github.com/nciyuan9264/game-platform-server/internal/contracts"
	"github.com/nciyuan9264/game-platform-server/internal/history"
)

func (h *Handler) roomInfo(ctx context.Context, room contracts.RoomRecord) (*platform.RoomInfo, error) {
	visibility, err := h.store.RoomVisibility(ctx, room.RoomID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, platformError("store_error", err)
	}
	if visibility == "" {
		visibility = contracts.VisibilityPublic
	}
	room.Visibility = visibility

	matchStatus := room.CurrentMatchStatus
	players := []*platform.RoomPlayer{}
	if room.CurrentMatchID != "" {
		match, matchErr := h.store.GetMatch(ctx, room.CurrentMatchID)
		if matchErr != nil && !errors.Is(matchErr, sql.ErrNoRows) {
			return nil, platformError("store_error", matchErr)
		}
		if matchErr == nil {
			matchStatus = match.Status
			players = make([]*platform.RoomPlayer, 0, len(match.Players))
			for _, player := range match.Players {
				displayName := player.DisplayName
				if displayName == "" {
					displayName = player.Name
				}
				if displayName == "" {
					displayName = player.PlayerID
				}
				mapped := &platform.RoomPlayer{
					PlayerID:    player.PlayerID,
					DisplayName: displayName,
					SeatIndex:   int32(player.SeatIndex),
					Online:      player.Online,
					Ai:          player.IsAI,
					Ready:       player.Ready,
				}
				if !player.IsAI && player.UserID != "" {
					mapped.UserID = stringPointer(player.UserID)
				}
				if player.Avatar != "" {
					mapped.Avatar = stringPointer(player.Avatar)
				}
				players = append(players, mapped)
			}
		}
	}
	status := publicRoomStatus(room.Status, matchStatus)
	return &platform.RoomInfo{
		RoomID:             room.RoomID,
		GameType:           platform.GameType(room.GameType),
		Status:             status,
		Visibility:         platform.Visibility(visibility),
		OwnerUserID:        room.OwnerUserID,
		OwnerPlayerID:      room.OwnerPlayerID,
		MaxPlayers:         int32(room.MaxPlayers),
		PlayerCount:        int32(len(players)),
		CurrentMatchID:     room.CurrentMatchID,
		CurrentMatchStatus: matchStatus,
		NextRoundNo:        int32(room.NextRoundNo),
		CreatedAt:          platform.ISODateTime(formatTime(room.CreatedAt)),
		UpdatedAt:          platform.ISODateTime(formatTime(room.UpdatedAt)),
		Players:            players,
		Metrics:            roomMetrics(room),
	}, nil
}

func roomMetrics(room contracts.RoomRecord) *platform.RoomMetrics {
	current := int32(room.ProgressCurrent)
	total := int32(room.ProgressTotal)
	switch room.GameType {
	case contracts.GameAcquire:
		empty := total - current
		if empty < 0 {
			empty = 0
		}
		return &platform.RoomMetrics{Acquire: &platform.AcquireRoomMetrics{
			EmptyTileCount: empty, ProgressCurrent: current, ProgressTotal: total,
		}}
	case contracts.GameDaVinci:
		remaining := total - current
		if remaining < 0 {
			remaining = 0
		}
		return &platform.RoomMetrics{Davinci: &platform.DavinciRoomMetrics{
			BoardCardCount: remaining, ProgressCurrent: current, ProgressTotal: total,
		}}
	default:
		return &platform.RoomMetrics{Splendor: &platform.SplendorRoomMetrics{
			MaxScore: current, ProgressCurrent: current, ProgressTotal: total,
		}}
	}
}

func publicRoomStatus(roomStatus, matchStatus string) string {
	if contracts.IsTerminalRoomStatus(roomStatus) {
		return "end"
	}
	switch matchStatus {
	case contracts.MatchStatusRunning, contracts.MatchStatusEnding:
		return "playing"
	case contracts.MatchStatusEnded, contracts.MatchStatusAborted:
		return "end"
	default:
		return "match"
	}
}

func roomVisibleTo(room *platform.RoomInfo, userID string) bool {
	if room.OwnerUserID == userID {
		return true
	}
	for _, player := range room.Players {
		if player != nil && player.GetUserID() == userID {
			return true
		}
	}
	return false
}

func mapHistoryGame(value history.Game) *platform.HistoryGame {
	mapped := &platform.HistoryGame{
		Id:              value.ID,
		RoomID:          value.RoomID,
		GameType:        platform.GameType(value.GameType),
		StartedAt:       platform.ISODateTime(formatTime(value.StartedAt)),
		DurationSeconds: int32(value.DurationSeconds),
		WinnerUserID:    value.WinnerUserID,
		MaxPlayers:      int32(value.MaxPlayers),
		CreatedAt:       platform.ISODateTime(formatTime(value.CreatedAt)),
		UpdatedAt:       platform.ISODateTime(formatTime(value.UpdatedAt)),
	}
	if value.EndedAt != nil {
		endedAt := platform.ISODateTime(formatTime(*value.EndedAt))
		mapped.EndedAt = &endedAt
	}
	if value.WinnerPlayerID != "" {
		mapped.WinnerPlayerID = stringPointer(value.WinnerPlayerID)
	}
	if value.EndReason != "" {
		mapped.EndReason = stringPointer(value.EndReason)
	}
	if value.Players != nil {
		mapped.Players = mapHistoryPlayers(value.Players)
	}
	return mapped
}

func mapHistoryPlayers(values []history.Player) []*platform.HistoryGamePlayer {
	result := make([]*platform.HistoryGamePlayer, 0, len(values))
	for _, value := range values {
		mapped := &platform.HistoryGamePlayer{
			Id:          value.ID,
			GameID:      value.GameID,
			UserID:      value.UserID,
			PlayerID:    value.PlayerID,
			SeatIndex:   int32(value.SeatIndex),
			IsAI:        value.IsAI,
			FinalStocks: int32(value.FinalStocks),
			IsWinner:    value.IsWinner,
			CreatedAt:   platform.ISODateTime(formatTime(value.CreatedAt)),
		}
		mapped.FinalScore = int32Pointer(value.FinalScore)
		mapped.FinalMoney = int32Pointer(value.FinalMoney)
		mapped.FinalRank = int32Pointer(value.FinalRank)
		result = append(result, mapped)
	}
	return result
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func stringPointer(value string) *string {
	return &value
}

func int32Pointer(value *int) *int32 {
	if value == nil {
		return nil
	}
	result := int32(*value)
	return &result
}
