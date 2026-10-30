package forsale

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) BuildPlayerView(
	_ context.Context,
	state game.State,
	request game.ViewRequest,
) (any, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	if typed.Roster[request.PlayerID] == nil {
		return nil, errors.New("player is not in room")
	}
	players := playerViews(typed, request.PlayerProfiles)
	if typed.Status == StatusMatch {
		return map[string]any{
			"type":        contracts.MessageTypeMatchSync,
			"roomID":      typed.RoomID,
			"matchID":     request.MatchID,
			"ownerID":     typed.OwnerID,
			"status":      typed.Status,
			"playerID":    request.PlayerID,
			"visibility":  typed.Visibility,
			"maxPlayers":  typed.MaxPlayers,
			"players":     players,
			"playerOrder": append([]string(nil), typed.PlayerOrder...),
		}, nil
	}
	return roomView(
		typed,
		request.PlayerID,
		request.MatchID,
		request.Seq,
		players,
		false,
	), nil
}

func (p *Plugin) BuildSpectatorView(
	_ context.Context,
	state game.State,
	seq int64,
) (any, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	return roomView(
		typed,
		"",
		"",
		seq,
		playerViews(typed, nil),
		false,
	), nil
}

func roomView(
	state *State,
	viewerID,
	matchID string,
	seq int64,
	players map[string]any,
	replay bool,
) map[string]any {
	result := map[string]PlayerResult{}
	if state.Status == StatusEnd {
		result = cloneResults(state.Result)
	}
	return map[string]any{
		"type":       contracts.MessageTypeRoomSync,
		"roomID":     state.RoomID,
		"matchID":    matchID,
		"seq":        seq,
		"playerId":   viewerID,
		"ownerID":    state.OwnerID,
		"visibility": state.Visibility,
		"playerData": playerDataView(state, viewerID, replay),
		"roomData":   roomDataView(state, players),
		"result":     result,
	}
}

func roomDataView(state *State, players map[string]any) map[string]any {
	data := map[string]any{
		"gameStatus":     state.Status,
		"round":          state.Round,
		"totalRounds":    state.TotalRounds,
		"propertyMarket": append([]int(nil), state.PropertyMarket...),
		"chequeMarket":   append([]int(nil), state.ChequeMarket...),
		"highestBid":     state.HighestBid,
		"currentPlayer":  state.CurrentPlayer,
		"starterPlayer":  state.StarterPlayer,
		"lockedCount":    lockedPlayerCount(state),
		"purchases":      cloneRoundRecords(state.Purchases),
		"sales":          cloneRoundRecords(state.Sales),
		"actionLog":      append([]Action(nil), state.ActionLog...),
		"players":        players,
		"playerOrder":    append([]string(nil), state.PlayerOrder...),
	}
	if state.LastAction != nil {
		action := *state.LastAction
		data["lastAction"] = action
	}
	if !state.TransitionDeadline.IsZero() {
		data["transitionDeadline"] = deadlineValue(state.TransitionDeadline)
		data["transitionTimeoutMs"] = state.TransitionTimeout.Milliseconds()
	}
	return data
}

func playerDataView(
	state *State,
	viewerID string,
	replay bool,
) map[string]any {
	result := make(map[string]any, len(state.PlayerOrder))
	revealLocked := state.Status == StatusSellReveal ||
		state.Status == StatusEnd
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		value := map[string]any{
			"seatIndex":     player.SeatIndex,
			"bid":           player.Bid,
			"passed":        player.Passed,
			"propertyCount": len(player.Properties),
			"chequeCount":   len(player.Cheques),
			"locked":        player.LockedProperty != 0,
		}
		private := replay || playerID == viewerID
		if private {
			value["coins"] = player.Coins
			value["properties"] = append([]int(nil), player.Properties...)
			value["cheques"] = append([]int(nil), player.Cheques...)
		}
		if player.LockedProperty != 0 &&
			((playerID == viewerID && !replay) || revealLocked) {
			value["lockedProperty"] = player.LockedProperty
		}
		if player.WonProperty != 0 {
			value["wonProperty"] = player.WonProperty
			value["roundCost"] = player.RoundCost
		}
		if playerID == viewerID && !player.ActionDeadline.IsZero() {
			value["actionDeadline"] = deadlineValue(player.ActionDeadline)
			value["actionTimeoutMs"] = playerActionTimeout.Milliseconds()
		}
		result[playerID] = value
	}
	return result
}

func playerViews(
	state *State,
	profiles map[string]game.PlayerProfile,
) map[string]any {
	result := make(map[string]any, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		userID := player.UserID
		displayName := player.DisplayName
		avatar := ""
		if profile, ok := profiles[playerID]; ok {
			userID = profile.UserID
			displayName = profile.DisplayName
			avatar = profile.Avatar
		}
		if displayName == "" {
			if player.AI {
				displayName = playerID
				if name, ok := game.AIName(playerID, aiNames); ok {
					displayName = name
				}
			} else {
				displayName = fmt.Sprintf("玩家 %s", playerID)
			}
		}
		result[playerID] = map[string]any{
			"userID":      userID,
			"playerID":    playerID,
			"displayName": displayName,
			"avatar":      avatar,
			"seatIndex":   player.SeatIndex,
			"online":      player.Online,
			"ai":          player.AI,
			"ready":       player.Ready,
		}
	}
	return result
}

func cloneResults(values map[string]PlayerResult) map[string]PlayerResult {
	result := make(map[string]PlayerResult, len(values))
	for playerID, value := range values {
		result[playerID] = value
	}
	return result
}

func deadlineValue(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
