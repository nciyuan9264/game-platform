package sixnimmt

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) IsRecordableCommand(commandType string) bool {
	switch commandType {
	case CommandLockCard, CommandUnlockCard, CommandChooseRow,
		CommandNextRound, CommandTimeout, CommandRoundTransition:
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
		score := player.TotalPenalty
		var rank *int
		if finished && typed.Result != nil {
			value := typed.Result.FinalRanks[playerID]
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
				containsString(typed.Result.WinnerIDs, playerID),
		})
	}
	winnerID := ""
	if finished {
		winnerID = firstWinnerBySeat(typed)
	}
	return game.HistoryProjection{
		Started:             true,
		Finished:            finished,
		AbandonedEligible:   typed.RoundNo > 1 || typed.TurnNo > 1,
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

func firstWinnerBySeat(state *State) string {
	if state.Result == nil {
		return ""
	}
	winners := append([]string(nil), state.Result.WinnerIDs...)
	sort.SliceStable(winners, func(i, j int) bool {
		return state.Roster[winners[i]].SeatIndex <
			state.Roster[winners[j]].SeatIndex
	})
	return firstString(winners)
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
		return nil, errors.New("sixnimmt replay state is unavailable")
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
				Hand:        []int{},
				Taken:       []int{},
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
	privateData := make(map[string]any, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		privateData[playerID] = privateDataView(state, playerID)
	}
	return map[string]any{
		"roomData": roomDataView(
			state,
			playerViews(state, nil),
		),
		"playersData": publicPlayerData(state),
		"privateData": privateData,
		"unusedCards": append([]int(nil), state.UnusedCards...),
		"result":      cloneResult(state.Result),
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
