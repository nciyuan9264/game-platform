package yinsh

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) IsRecordableCommand(commandType string) bool {
	switch commandType {
	case CommandPlaceRing,
		CommandMoveRing,
		CommandRemoveRow,
		CommandRemoveRing,
		CommandResign,
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
	for _, playerID := range typed.PlayerOrder {
		player := typed.Roster[playerID]
		if player == nil {
			continue
		}
		score := typed.RingsRemoved[player.Side]
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
		AbandonedEligible:   len(typed.Markers) >= 5 || typed.RingsRemoved[SideWhite]+typed.RingsRemoved[SideBlack] > 0,
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
		return nil, errors.New("yinsh replay state is unavailable")
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	ensureState(&state)
	if len(state.PlayerOrder) == 0 {
		for _, historyPlayer := range players {
			side := SideWhite
			if historyPlayer.SeatIndex != 0 {
				side = SideBlack
			}
			state.PlayerOrder = append(state.PlayerOrder, historyPlayer.PlayerID)
			state.Roster[historyPlayer.PlayerID] = &Player{
				UserID:      historyPlayer.UserID,
				PlayerID:    historyPlayer.PlayerID,
				DisplayName: historyPlayer.DisplayName,
				SeatIndex:   historyPlayer.SeatIndex,
				AI:          historyPlayer.IsAI,
				Ready:       true,
				Side:        side,
			}
		}
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
	var lastAction any
	if state.LastAction != nil {
		lastAction = actionView(*state.LastAction)
	}
	activity := make([]any, 0, len(state.Log))
	for _, action := range state.Log {
		activity = append(activity, actionView(action))
	}
	candidateRows := []Row{}
	if state.Status == StatusRemoveRow {
		candidateRows = rowsFor(state, sideForPlayer(state, state.ResolverPlayer))
	}
	legal := map[string][]MoveOption{}
	if state.Status == StatusMove {
		legal = allLegalMoves(state, state.CurrentPlayer)
	}
	return map[string]any{
		"rings":          state.Rings,
		"markers":        state.Markers,
		"currentPlayer":  state.CurrentPlayer,
		"moverPlayer":    optionalString(state.MoverPlayer),
		"resolverPlayer": optionalString(state.ResolverPlayer),
		"gameStatus":     state.Status,
		"placedRings":    state.PlacedRings,
		"markerPool":     markerPool(state),
		"legalMoves":     legal,
		"candidateRows":  candidateRows,
		"lastAction":     lastAction,
		"turnDeadline":   deadlineValue(state.TurnDeadline),
		"turnTimeoutMs":  state.TurnTimeout.Milliseconds(),
		"players":        playerViews(state, nil),
		"playerOrder":    state.PlayerOrder,
		"roundNo":        state.RoundNo,
		"activity":       activity,
	}
}

func replayPlayerData(state *State) map[string]any {
	result := map[string]any{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		result[playerID] = map[string]any{
			"side":         player.Side,
			"ringsRemoved": state.RingsRemoved[player.Side],
		}
	}
	return result
}
