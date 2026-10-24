package rpc

import (
	"context"
	"strconv"

	platform "github.com/nciyuan9264/game-platform-client/kitex_gen/pam/game/platform/v1"
	"github.com/nciyuan9264/game-platform/internal/history"
	"github.com/nciyuan9264/game-platform/internal/playerprofile"
)

func (h *Handler) resolveRoomDisplayNames(ctx context.Context, rooms []*platform.RoomInfo) {
	players := make([]playerprofile.Player, 0)
	for _, room := range rooms {
		if room == nil {
			continue
		}
		for _, player := range room.GetPlayers() {
			if player == nil {
				continue
			}
			players = append(players, playerprofile.Player{
				UserID:      player.GetUserID(),
				PlayerID:    player.GetPlayerID(),
				DisplayName: player.GetDisplayName(),
				IsAI:        player.GetAi(),
			})
		}
	}
	displayNames := h.profiles.Resolve(ctx, players)
	for _, room := range rooms {
		if room == nil {
			continue
		}
		for _, player := range room.GetPlayers() {
			if player == nil {
				continue
			}
			player.DisplayName = displayNames[player.GetPlayerID()]
		}
	}
}

func (h *Handler) resolveHistoryPlayers(ctx context.Context, players []history.Player) {
	subjects := make([]playerprofile.Player, 0, len(players))
	for _, player := range players {
		userID := ""
		if player.UserID != nil {
			userID = strconv.FormatInt(*player.UserID, 10)
		}
		subjects = append(subjects, playerprofile.Player{
			UserID:      userID,
			PlayerID:    player.PlayerID,
			DisplayName: player.DisplayName,
			IsAI:        player.IsAI,
		})
	}
	displayNames := h.profiles.Resolve(ctx, subjects)
	for index := range players {
		players[index].DisplayName = displayNames[players[index].PlayerID]
	}
}

func (h *Handler) resolveHistoryGames(ctx context.Context, games []history.Game) {
	subjects := make([]playerprofile.Player, 0)
	for _, gameRecord := range games {
		for _, player := range gameRecord.Players {
			userID := ""
			if player.UserID != nil {
				userID = strconv.FormatInt(*player.UserID, 10)
			}
			subjects = append(subjects, playerprofile.Player{
				UserID:      userID,
				PlayerID:    player.PlayerID,
				DisplayName: player.DisplayName,
				IsAI:        player.IsAI,
			})
		}
	}
	displayNames := h.profiles.Resolve(ctx, subjects)
	for gameIndex := range games {
		for playerIndex := range games[gameIndex].Players {
			player := &games[gameIndex].Players[playerIndex]
			player.DisplayName = displayNames[player.PlayerID]
		}
	}
}

func (h *Handler) resolveLeaderboard(ctx context.Context, entries []history.LeaderboardEntry) {
	subjects := make([]playerprofile.Player, 0, len(entries))
	for _, entry := range entries {
		subjects = append(subjects, playerprofile.Player{
			UserID:      strconv.FormatInt(entry.UserID, 10),
			PlayerID:    entry.PlayerID,
			DisplayName: entry.DisplayName,
		})
	}
	displayNames := h.profiles.Resolve(ctx, subjects)
	for index := range entries {
		entries[index].DisplayName = displayNames[entries[index].PlayerID]
	}
}

func historyDisplayNames(players []history.Player) map[string]string {
	displayNames := make(map[string]string, len(players))
	for _, player := range players {
		displayNames[player.PlayerID] = player.DisplayName
	}
	return displayNames
}
