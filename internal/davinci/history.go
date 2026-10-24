package davinci

import (
	"context"
	"encoding/json"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const davinciHistorySchemaVersion = 1

func (p *Plugin) IsRecordableCommand(commandType string) bool {
	switch commandType {
	case CommandGetCard, CommandGuessCard, CommandSetCard, CommandTurnTimeout:
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
		typed.Status != contracts.RoomStatusMatch &&
		typed.Status != StatusWaiting
	if !started {
		return game.HistoryProjection{}, nil
	}

	stateBlob, err := json.Marshal(typed)
	if err != nil {
		return game.HistoryProjection{}, err
	}
	finished := typed.Status == StatusEnd
	winnerPlayerID := ""
	if finished {
		winnerPlayerID = typed.WinnerID
		if winnerPlayerID == "" {
			winnerPlayerID = p.engine.findWinner(typed)
		}
	}

	players := make([]game.HistoryPlayer, 0, len(typed.PlayerSeq))
	for _, playerID := range typed.PlayerSeq {
		player := typed.Players[playerID]
		if player == nil {
			continue
		}
		unrevealed := 0
		for _, card := range player.Cards {
			if !card.IsRevealed {
				unrevealed++
			}
		}
		finalScore := unrevealed
		var finalRank *int
		if finished {
			rank := 2
			if playerID == winnerPlayerID {
				rank = 1
			}
			finalRank = &rank
		}
		players = append(players, game.HistoryPlayer{
			PlayerID:   playerID,
			SeatIndex:  player.SeatIndex,
			IsAI:       player.AI,
			FinalScore: &finalScore,
			FinalRank:  finalRank,
			IsWinner:   finished && playerID == winnerPlayerID,
		})
	}

	return game.HistoryProjection{
		Started:             true,
		Finished:            finished,
		AbandonedEligible:   davinciAbandonedEligible(typed),
		StartedAt:           typed.GameStartTime,
		MaxPlayers:          typed.MaxPlayers,
		ReplaySchemaVersion: davinciHistorySchemaVersion,
		ReplayFrame:         davinciHistoryFrame(typed, winnerPlayerID),
		InitialState:        stateBlob,
		StateBlob:           stateBlob,
		FinalResult: map[string]any{
			"status":   typed.Status,
			"winnerID": winnerPlayerID,
		},
		WinnerPlayerID: winnerPlayerID,
		Players:        players,
	}, nil
}

func davinciAbandonedEligible(state *State) bool {
	total, revealed := 0, 0
	for _, player := range state.Players {
		if player == nil {
			continue
		}
		for _, card := range player.Cards {
			total++
			if card.IsRevealed {
				revealed++
			}
		}
	}
	return total > 0 && revealed*2 >= total
}

func davinciHistoryFrame(state *State, winnerPlayerID string) map[string]any {
	players := make(map[string]any, len(state.Players))
	for playerID, player := range state.Players {
		if player == nil {
			continue
		}
		players[playerID] = map[string]any{
			"playerID":  playerID,
			"online":    player.Online,
			"ready":     player.Ready,
			"ai":        player.AI,
			"seatIndex": player.SeatIndex,
		}
	}
	return map[string]any{
		"roomData": map[string]any{
			"currentPlayer": state.CurrentPlayer,
			"gameStatus":    state.Status,
			"players":       players,
			"lastData":      state.LastAction,
			"turnDeadline":  turnDeadlineValue(state.TurnDeadline),
			"turnTimeoutMs": state.TurnTimeout.Milliseconds(),
		},
		"playersData": state.Players,
		"result": map[string]any{
			"winnerID": winnerPlayerID,
		},
	}
}
