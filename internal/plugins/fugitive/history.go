package fugitive

import (
	"context"
	"encoding/json"

	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) IsRecordableCommand(commandType string) bool {
	switch commandType {
	case CommandDrawCard,
		CommandPlayHideout,
		CommandFinishOpening,
		CommandLayLow,
		CommandSearchHideouts,
		CommandTimeout:
		return true
	default:
		return false
	}
}

func (p *Plugin) ProjectHistory(
	_ context.Context,
	state game.State,
) (game.HistoryProjection, error) {
	typed, err := asState(state)
	if err != nil {
		return game.HistoryProjection{}, err
	}
	started := !typed.GameStartTime.IsZero() &&
		typed.Status != StatusMatch &&
		typed.Status != StatusWaiting
	if !started {
		return game.HistoryProjection{}, nil
	}
	stateBlob, err := json.Marshal(typed)
	if err != nil {
		return game.HistoryProjection{}, err
	}
	finished := typed.Status == StatusEnd
	players := make([]game.HistoryPlayer, 0, len(typed.PlayerOrder))
	revealed := len(typed.Route) - hiddenHideoutCount(typed) - 1
	if revealed < 0 {
		revealed = 0
	}
	for _, playerID := range typed.PlayerOrder {
		player := typed.Roster[playerID]
		if player == nil {
			continue
		}
		score := revealed
		if player.Role == RoleFugitive {
			score = lastHideoutNumber(typed)
			if finished && typed.WinnerRole == RoleFugitive {
				score = 42
			}
		}
		finalScore := score
		var finalRank *int
		if finished {
			rank := 2
			if typed.WinnerID == playerID {
				rank = 1
			}
			finalRank = &rank
		}
		players = append(players, game.HistoryPlayer{
			UserID:      player.UserID,
			PlayerID:    playerID,
			DisplayName: player.DisplayName,
			SeatIndex:   player.SeatIndex,
			IsAI:        player.AI,
			FinalScore:  &finalScore,
			FinalRank:   finalRank,
			IsWinner:    finished && typed.WinnerID == playerID,
			GameRole:    string(player.Role),
		})
	}
	return game.HistoryProjection{
		Started:             true,
		Finished:            finished,
		AbandonedEligible:   typed.RoundNo >= 3 || typed.Status == StatusManhunt,
		StartedAt:           typed.GameStartTime,
		MaxPlayers:          typed.MaxPlayers,
		ReplaySchemaVersion: ReplaySchemaVersion,
		ReplayFrame:         historyFrame(typed),
		InitialState:        stateBlob,
		StateBlob:           stateBlob,
		FinalResult: map[string]any{
			"winnerID":   typed.WinnerID,
			"winnerRole": typed.WinnerRole,
			"reason":     typed.EndReason,
			"searches":   typed.Searches,
		},
		WinnerPlayerID: typed.WinnerID,
		Players:        players,
	}, nil
}

func historyFrame(state *State) map[string]any {
	route := make([]map[string]any, 0, len(state.Route))
	for index, hideout := range state.Route {
		entry := map[string]any{
			"index":       index,
			"revealed":    hideout.Revealed,
			"sprintCount": len(hideout.Sprints),
		}
		if hideout.Revealed || state.Status == StatusEnd {
			entry["card"] = hideout.Card
			entry["sprints"] = hideout.Sprints
		}
		route = append(route, entry)
	}
	return map[string]any{
		"roomData": map[string]any{
			"gameStatus":         state.Status,
			"roundNo":            state.RoundNo,
			"highestRevealed":    highestRevealed(state),
			"hiddenHideoutCount": hiddenHideoutCount(state),
			"route":              route,
		},
		"playersData": historyPlayerData(state),
		"result": map[string]any{
			"winnerID":   state.WinnerID,
			"winnerRole": state.WinnerRole,
			"reason":     state.EndReason,
			"searches":   state.Searches,
		},
	}
}

func historyPlayerData(state *State) map[string]any {
	players := make(map[string]any, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		players[playerID] = map[string]any{
			"role":      player.Role,
			"handCount": len(player.Hand),
			"seatIndex": player.SeatIndex,
			"ai":        player.AI,
		}
	}
	return players
}
