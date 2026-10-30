package cantstop

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) IsRecordableCommand(commandType string) bool {
	switch commandType {
	case CommandRollDice, CommandAdvance, CommandBank,
		CommandNextTurn, CommandTimeout, CommandTransition:
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
	ranks := finalRanks(typed)
	players := make([]game.HistoryPlayer, 0, len(typed.PlayerOrder))
	for _, playerID := range typed.PlayerOrder {
		player := typed.Roster[playerID]
		if player == nil {
			continue
		}
		score := claimedColumnCount(typed, playerID)
		var rank *int
		if finished {
			value := ranks[playerID]
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
			IsWinner: finished && typed.Result != nil &&
				typed.Result.WinnerID == playerID,
		})
	}
	winnerID := ""
	if finished && typed.Result != nil {
		winnerID = typed.Result.WinnerID
	}
	return game.HistoryProjection{
		Started:             true,
		Finished:            finished,
		AbandonedEligible:   typed.TotalRolls > 0 || len(typed.Log) > 0,
		StartedAt:           typed.GameStartTime,
		MaxPlayers:          typed.MaxPlayers,
		ReplaySchemaVersion: ReplaySchemaVersion,
		ReplayFrame:         replayFrame(typed),
		InitialState:        stateBlob,
		StateBlob:           stateBlob,
		FinalResult:         cloneResult(typed.Result),
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
		return nil, errors.New("can't stop replay state is unavailable")
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
				GameReady:   true,
				Saved:       map[int]int{},
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
		),
		"playersData": publicPlayerData(state),
		"result":      cloneResult(state.Result),
	}
}

func finalRanks(state *State) map[string]int {
	type candidate struct {
		playerID string
		claimed  int
		saved    int
		seat     int
	}
	values := make([]candidate, 0, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		values = append(values, candidate{
			playerID: playerID,
			claimed:  claimedColumnCount(state, playerID),
			saved:    savedStepCount(player),
			seat:     player.SeatIndex,
		})
	}
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].claimed != values[j].claimed {
			return values[i].claimed > values[j].claimed
		}
		if values[i].saved != values[j].saved {
			return values[i].saved > values[j].saved
		}
		return values[i].seat < values[j].seat
	})
	result := map[string]int{}
	for index, value := range values {
		result[value.playerID] = index + 1
	}
	return result
}
