package splendor

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/splendor/domain"
	"github.com/nciyuan9264/game-platform/internal/splendor/rules"
)

func (p *Plugin) IsRecordableCommand(commandType string) bool {
	switch commandType {
	case "game_get_gem", "game_buy_card", "game_preserve_card", "turn_timeout":
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
		typed.RoomStatus != domain.RoomStatusMatch &&
		typed.RoomStatus != domain.RoomStatusWaiting
	if !started {
		return game.HistoryProjection{}, nil
	}
	initialState, err := json.Marshal(typed)
	if err != nil {
		return game.HistoryProjection{}, err
	}
	result := rules.RecomputeDerivedState(domain.NewRoom(typed))
	stateBlob, err := rules.EncodeStateSnapshot(typed, result)
	if err != nil {
		return game.HistoryProjection{}, err
	}
	scores := scoreItems(typed)
	finished := typed.RoomStatus == domain.RoomStatusEnd
	winnerPlayerID := ""
	if finished && len(scores) > 0 {
		winnerPlayerID = scores[0].playerID
	}
	players := make([]game.HistoryPlayer, 0, len(scores))
	rank := 0
	previousScore, previousCards := -1, -1
	for index, score := range scores {
		if index == 0 || score.score != previousScore || score.cards != previousCards {
			rank = index + 1
			previousScore, previousCards = score.score, score.cards
		}
		finalScore, finalRank := score.score, rank
		roster := typed.Roster[score.playerID]
		players = append(players, game.HistoryPlayer{
			PlayerID:   score.playerID,
			SeatIndex:  index,
			IsAI:       roster != nil && roster.AI,
			FinalScore: &finalScore,
			FinalRank:  &finalRank,
			IsWinner:   finished && score.playerID == winnerPlayerID,
		})
	}
	return game.HistoryProjection{
		Started:             true,
		Finished:            finished,
		AbandonedEligible:   maxScore(typed)*2 >= 15,
		StartedAt:           typed.GameStartTime,
		MaxPlayers:          typed.MaxPlayers,
		ReplaySchemaVersion: ReplaySchemaVersion,
		ReplayFrame:         splendorReplayFrame(typed, result),
		InitialState:        initialState,
		StateBlob:           stateBlob,
		FinalResult:         result,
		WinnerPlayerID:      winnerPlayerID,
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
	var state *domain.GameState
	if len(stateBlob) > 0 {
		if decoded, _, ok := rules.DecodeStateSnapshot(stateBlob); ok {
			state = decoded
		}
	}
	if state == nil && len(stateSnapshot) > 0 {
		if decoded, _, ok := rules.DecodeStateSnapshot(stateSnapshot); ok {
			state = decoded
		}
	}
	if state == nil && len(initialState) > 0 {
		state = &domain.GameState{}
		if err := json.Unmarshal(initialState, state); err != nil {
			return nil, err
		}
	}
	if state == nil {
		return nil, errors.New("splendor replay state is unavailable")
	}
	ensureState(state)
	if len(state.PlayerOrder) == 0 {
		for _, player := range players {
			state.PlayerOrder = append(state.PlayerOrder, player.PlayerID)
			state.Roster[player.PlayerID] = &domain.Player{
				UserID: player.UserID, PlayerID: player.PlayerID,
				AI: player.IsAI, SeatIndex: player.SeatIndex, Ready: true,
			}
		}
	}
	return state, nil
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
	result := rules.RecomputeDerivedState(domain.NewRoom(typed))
	snapshot := splendorReplayFrame(typed, result)
	snapshot["seq"] = seq
	snapshot["totalEvents"] = total
	if commandType != "" {
		payloadJSON, _ := json.Marshal(payload)
		snapshot["currentEvent"] = map[string]any{
			"seq": seq, "playerID": playerID, "cmdType": commandType,
			"payloadJSON": string(payloadJSON),
		}
	}
	return snapshot, nil
}

func splendorReplayFrame(typed *domain.GameState, result any) map[string]any {
	return map[string]any{
		"roomData": map[string]any{
			"normalCards":   typed.NormalCards,
			"nobleCards":    typed.NobleCards,
			"deckCounts":    deckCounts(typed),
			"gems":          typed.Gems,
			"currentPlayer": typed.CurrentPlayer,
			"gameStatus":    typed.RoomStatus,
			"lastData":      typed.LastData,
			"players":       playerViews(typed, nil),
		},
		"playersData": typed.Players,
		"result":      result,
	}
}

type scoreItem struct {
	playerID string
	score    int
	cards    int
}

func scoreItems(state *domain.GameState) []scoreItem {
	result := make([]scoreItem, 0, len(state.Players))
	for playerID, player := range state.Players {
		if player == nil {
			continue
		}
		result = append(result, scoreItem{
			playerID: playerID, score: player.Score, cards: len(player.NormalCard),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].score != result[j].score {
			return result[i].score > result[j].score
		}
		if result[i].cards != result[j].cards {
			return result[i].cards < result[j].cards
		}
		return result[i].playerID < result[j].playerID
	})
	return result
}
