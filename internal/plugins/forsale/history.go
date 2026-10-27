package forsale

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) IsRecordableCommand(commandType string) bool {
	switch commandType {
	case CommandBid,
		CommandPass,
		CommandLockSale,
		CommandRevealSale,
		CommandNextRound,
		CommandStartSelling,
		CommandTurnTimeout,
		CommandPhaseTimeout:
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
	winnerIDs := []string{}
	for _, playerID := range typed.PlayerOrder {
		player := typed.Roster[playerID]
		if player == nil {
			continue
		}
		var finalScore, finalMoney, finalRank *int
		isWinner := false
		if result, ok := typed.Result[playerID]; ok {
			score := result.Total
			money := result.Coins
			rank := result.Rank
			finalScore = &score
			finalMoney = &money
			finalRank = &rank
			isWinner = result.Winner
			if isWinner {
				winnerIDs = append(winnerIDs, playerID)
			}
		}
		players = append(players, game.HistoryPlayer{
			UserID:      player.UserID,
			PlayerID:    playerID,
			DisplayName: player.DisplayName,
			SeatIndex:   player.SeatIndex,
			IsAI:        player.AI,
			FinalScore:  finalScore,
			FinalMoney:  finalMoney,
			FinalRank:   finalRank,
			IsWinner:    isWinner,
		})
	}
	winnerPlayerID := ""
	winnerUserID := ""
	if len(winnerIDs) == 1 {
		winnerPlayerID = winnerIDs[0]
		if player := typed.Roster[winnerPlayerID]; player != nil {
			winnerUserID = player.UserID
		}
	}
	return game.HistoryProjection{
		Started:  true,
		Finished: finished,
		AbandonedEligible: typed.TotalRounds > 0 &&
			len(typed.Purchases) >= typed.TotalRounds,
		StartedAt:           typed.GameStartTime,
		MaxPlayers:          len(typed.PlayerOrder),
		ReplaySchemaVersion: ReplaySchemaVersion,
		ReplayFrame:         replayFrame(typed),
		InitialState:        stateBlob,
		StateBlob:           stateBlob,
		FinalResult:         cloneResults(typed.Result),
		WinnerUserID:        winnerUserID,
		WinnerPlayerID:      winnerPlayerID,
		Players:             players,
	}, nil
}

func (p *Plugin) DecodeReplayState(
	_ context.Context,
	initialState,
	stateSnapshot,
	stateBlob []byte,
	_ []game.HistoryPlayer,
) (game.State, error) {
	data := stateBlob
	if len(data) == 0 {
		data = stateSnapshot
	}
	if len(data) == 0 {
		data = initialState
	}
	if len(data) == 0 {
		return nil, errors.New("for sale replay state is unavailable")
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	ensureCollections(&state)
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
		"roomData":    roomDataView(state, playerViews(state, nil)),
		"playersData": playerDataView(state, "", true),
		"result":      cloneResults(state.Result),
	}
}
