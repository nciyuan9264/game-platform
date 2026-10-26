package warchest

import (
	"context"
	"encoding/json"

	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) IsRecordableCommand(commandType string) bool {
	switch commandType {
	case CommandPlayAction, CommandResign, CommandTimeout:
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
		score := controlledLocations(typed, player.Side)
		var rank *int
		if finished {
			value := 2
			if playerID == typed.WinnerID {
				value = 1
			}
			rank = &value
		}
		finalScore := score
		players = append(players, game.HistoryPlayer{
			UserID:      player.UserID,
			PlayerID:    player.PlayerID,
			DisplayName: player.DisplayName,
			SeatIndex:   player.SeatIndex,
			IsAI:        player.AI,
			FinalScore:  &finalScore,
			FinalRank:   rank,
			IsWinner:    finished && playerID == typed.WinnerID,
			GameRole:    string(player.Side),
		})
	}
	frame := omniscientReplayFrame(typed)
	return game.HistoryProjection{
		Started:             true,
		Finished:            finished,
		AbandonedEligible:   typed.ActionCounter > 0,
		StartedAt:           typed.GameStartTime,
		MaxPlayers:          typed.MaxPlayers,
		ReplaySchemaVersion: ReplaySchemaVersion,
		ReplayFrame:         frame,
		InitialState:        stateBlob,
		StateBlob:           stateBlob,
		FinalResult:         resultView(typed),
		WinnerPlayerID:      typed.WinnerID,
		RoomDisplayName:     typed.DisplayName,
		Players:             players,
	}, nil
}

func omniscientReplayFrame(state *State) map[string]any {
	players := playerViews(state, nil)
	log := projectedLog(state, "", true)
	var last any
	if len(log) > 0 {
		last = log[len(log)-1]
	}
	return map[string]any{
		"roomData": replayRoomData(
			state,
			players,
			[]map[string]any{},
			log,
			last,
		),
		"playersData": projectedPlayerData(state, "", true),
		"privateData": privateData(state, "", true),
		"result":      resultView(state),
	}
}
