package onitama

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) IsRecordableCommand(commandType string) bool {
	switch commandType {
	case CommandMovePiece, CommandPass, CommandResign, CommandTimeout:
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
	for _, playerID := range typed.PlayerOrder {
		player := typed.Roster[playerID]
		if player == nil {
			continue
		}
		score := pieceCount(typed, player.Side)
		var rank *int
		if finished {
			value := 2
			if typed.WinnerID == playerID {
				value = 1
			}
			rank = &value
		}
		players = append(players, game.HistoryPlayer{
			UserID:      player.UserID,
			PlayerID:    playerID,
			DisplayName: player.DisplayName,
			SeatIndex:   player.SeatIndex,
			IsAI:        player.AI,
			FinalScore:  &score,
			FinalRank:   rank,
			IsWinner:    finished && typed.WinnerID == playerID,
			GameRole:    string(player.Side),
		})
	}
	winnerID := ""
	if finished {
		winnerID = typed.WinnerID
	}
	return game.HistoryProjection{
		Started:             true,
		Finished:            finished,
		AbandonedEligible:   len(typed.Log) > 0,
		StartedAt:           typed.GameStartTime,
		MaxPlayers:          typed.MaxPlayers,
		ReplaySchemaVersion: ReplaySchemaVersion,
		ReplayFrame:         replayFrame(typed),
		InitialState:        stateBlob,
		StateBlob:           stateBlob,
		FinalResult:         replayResult(typed),
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
		return nil, errors.New("onitama replay state is unavailable")
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	ensureState(&state)
	if len(state.PlayerOrder) == 0 &&
		len(players) == defaultMaxPlayers {
		for _, historyPlayer := range players {
			side := Side(historyPlayer.GameRole)
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
				GameReady:   true,
				Side:        side,
			}
			switch side {
			case SideBlue:
				state.BluePlayerID = historyPlayer.PlayerID
			case SideRed:
				state.RedPlayerID = historyPlayer.PlayerID
			}
		}
		sortPlayerOrder(&state)
	}
	if err := validateState(&state); err != nil {
		return nil, err
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
		"roomData": roomDataView(
			state,
			playerViews(state, nil),
			false,
		),
		"playersData": replayPlayerData(state),
		"result":      replayResult(state),
	}
}

func replayResult(state *State) any {
	if state.Status != StatusEnd {
		return nil
	}
	return resultView(state)
}
