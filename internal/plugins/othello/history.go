package othello

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) IsRecordableCommand(commandType string) bool {
	switch commandType {
	case CommandPlaceDisc, CommandPass, CommandResign, CommandTimeout:
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
	black, white, _ := counts(typed.Board)
	players := make([]game.HistoryPlayer, 0, len(typed.PlayerOrder))
	for _, playerID := range typed.PlayerOrder {
		player := typed.Roster[playerID]
		if player == nil {
			continue
		}
		score := black
		if player.Side == SideWhite {
			score = white
		}
		var rank *int
		if finished {
			value := 1
			if typed.WinnerID != "" && typed.WinnerID != playerID {
				value = 2
			}
			rank = &value
		}
		finalScore := score
		players = append(players, game.HistoryPlayer{
			UserID:      player.UserID,
			PlayerID:    playerID,
			DisplayName: player.DisplayName,
			SeatIndex:   player.SeatIndex,
			IsAI:        player.AI,
			FinalScore:  &finalScore,
			FinalRank:   rank,
			IsWinner:    finished && typed.WinnerID == playerID,
		})
	}
	winnerID := ""
	if finished {
		winnerID = typed.WinnerID
	}
	return game.HistoryProjection{
		Started:             true,
		Finished:            finished,
		AbandonedEligible:   hasPlayedMove(typed),
		StartedAt:           typed.GameStartTime,
		MaxPlayers:          typed.MaxPlayers,
		ReplaySchemaVersion: ReplaySchemaVersion,
		ReplayFrame:         replayFrame(typed),
		InitialState:        stateBlob,
		StateBlob:           stateBlob,
		FinalResult:         resultView(typed),
		WinnerPlayerID:      winnerID,
		Players:             players,
	}, nil
}

func (p *Plugin) DecodeReplayState(
	_ context.Context,
	initialState,
	stateSnapshot,
	stateBlob []byte,
	players []game.HistoryPlayer,
) (game.State, error) {
	data := stateBlob
	if len(data) == 0 {
		data = stateSnapshot
	}
	if len(data) == 0 {
		data = initialState
	}
	if len(data) == 0 {
		return nil, errors.New("othello replay state is unavailable")
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	ensureState(&state)
	if len(state.PlayerOrder) == 0 {
		for _, historyPlayer := range players {
			state.PlayerOrder = append(
				state.PlayerOrder,
				historyPlayer.PlayerID,
			)
			state.Roster[historyPlayer.PlayerID] = &Player{
				UserID:      historyPlayer.UserID,
				PlayerID:    historyPlayer.PlayerID,
				DisplayName: historyPlayer.DisplayName,
				SeatIndex:   historyPlayer.SeatIndex,
				AI:          historyPlayer.IsAI,
				Ready:       true,
			}
		}
		sortPlayerOrder(&state)
		assignSides(&state)
	}
	return &state, nil
}

func (p *Plugin) BuildReplaySnapshot(
	_ context.Context,
	state game.State,
	seq,
	total int,
	commandType,
	playerID string,
	payload any,
) (any, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	frame := replayFrame(typed)
	frame["seq"] = seq
	frame["totalEvents"] = total
	if commandType != "" {
		payloadJSON, _ := json.Marshal(payload)
		frame["currentEvent"] = map[string]any{
			"seq":         seq,
			"playerID":    playerID,
			"cmdType":     commandType,
			"payloadJSON": string(payloadJSON),
		}
	}
	return frame, nil
}

func replayFrame(state *State) map[string]any {
	return map[string]any{
		"roomData":    replayRoomData(state),
		"playersData": replayPlayerData(state),
		"result":      resultView(state),
	}
}

func replayRoomData(state *State) map[string]any {
	legal := []Move{}
	if state.Status == StatusPlaying || state.Status == StatusPass {
		legal = legalMoves(
			state.Board,
			sideForPlayer(state, state.CurrentPlayer),
		)
	}
	var lastAction any
	if state.LastAction != nil {
		lastAction = cloneAction(*state.LastAction)
	}
	return map[string]any{
		"board":         boardView(state.Board),
		"currentPlayer": state.CurrentPlayer,
		"currentSide":   currentSide(state),
		"blackPlayerID": state.BlackPlayerID,
		"whitePlayerID": state.WhitePlayerID,
		"roundNo":       state.RoundNo,
		"gameStatus":    state.Status,
		"legalMoves":    legal,
		"moveLog":       state.Log,
		"lastData":      lastAction,
		"turnDeadline":  deadlineValue(state.TurnDeadline),
		"turnTimeoutMs": state.TurnTimeout.Milliseconds(),
		"players":       playerViews(state, nil),
		"playerOrder":   state.PlayerOrder,
	}
}

func replayPlayerData(state *State) map[string]any {
	black, white, _ := counts(state.Board)
	result := map[string]any{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		count := black
		if player.Side == SideWhite {
			count = white
		}
		result[playerID] = map[string]any{
			"side":      player.Side,
			"discCount": count,
		}
	}
	return result
}

func hasPlayedMove(state *State) bool {
	for _, action := range state.Log {
		if action.Kind == "move" {
			return true
		}
	}
	return false
}
