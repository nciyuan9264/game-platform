package cantstop

import (
	"context"
	"errors"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) Decide(
	_ context.Context,
	request game.AIRequest,
) (contracts.RoomCommand, error) {
	state, err := asState(request.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	if state.CurrentPlayer != request.PlayerID {
		return contracts.RoomCommand{}, errors.New("AI is not active")
	}
	switch state.Status {
	case StatusRoll:
		return contracts.RoomCommand{
			Type:     CommandRollDice,
			PlayerID: request.PlayerID,
		}, nil
	case StatusChoose:
		option, err := bestAdvance(state)
		if err != nil {
			return contracts.RoomCommand{}, err
		}
		return contracts.RoomCommand{
			Type:     CommandAdvance,
			PlayerID: request.PlayerID,
			Payload:  map[string]any{"actionID": option.ActionID},
		}, nil
	case StatusDecide:
		if shouldBank(state) {
			return contracts.RoomCommand{
				Type:     CommandBank,
				PlayerID: request.PlayerID,
			}, nil
		}
		return contracts.RoomCommand{
			Type:     CommandRollDice,
			PlayerID: request.PlayerID,
		}, nil
	default:
		return contracts.RoomCommand{}, errors.New("AI has no action now")
	}
}

func bestAdvance(state *State) (PairingOption, error) {
	if len(state.PairingOptions) == 0 {
		return PairingOption{}, errors.New("no legal action")
	}
	type candidate struct {
		option PairingOption
		score  float64
	}
	candidates := make([]candidate, 0, len(state.PairingOptions))
	for _, option := range state.PairingOptions {
		candidates = append(candidates, candidate{
			option: option,
			score:  advanceScore(state, option),
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].option.ActionID < candidates[j].option.ActionID
	})
	return candidates[0].option, nil
}

func advanceScore(state *State, option PairingOption) float64 {
	player := state.Roster[state.CurrentPlayer]
	score := float64(option.StepCount) * 10
	for column, steps := range option.Advances {
		height := columnHeight(column)
		before := 0
		if player != nil {
			before = player.Saved[column]
		}
		if current := state.Runners[column]; current > before {
			before = current
			score += 1.5
		}
		after := option.RunnerPositions[column]
		score += float64(after-before) * float64(14-height) / 3
		score += float64(after) / float64(height) * 4
		if after >= height {
			score += 18
		}
		if state.ClaimedBy[column] != "" {
			score -= 100
		}
		score += float64(steps)
	}
	score -= bustRiskForRunners(state, option.RunnerPositions) * 4
	return score
}

func shouldBank(state *State) bool {
	gain := turnGain(state)
	if gain <= 0 {
		return false
	}
	for column, position := range state.Runners {
		if position >= columnHeight(column) {
			return true
		}
	}
	risk := bustRisk(state)
	if gain >= 5 {
		return true
	}
	if len(state.Runners) >= targetColumns && risk >= 0.28 {
		return true
	}
	if state.RollNumber >= 4 && gain >= 3 {
		return true
	}
	return risk >= 0.42 && gain >= 2
}

func bustRiskForRunners(state *State, runners map[int]int) float64 {
	copyState := cloneState(state)
	copyState.Runners = cloneProgress(runners)
	return bustRisk(copyState)
}

func timeoutFallback(
	state *State,
	playerID string,
) (contracts.RoomCommand, error) {
	if state.CurrentPlayer != playerID {
		return contracts.RoomCommand{}, errors.New("timeout is stale")
	}
	switch state.Status {
	case StatusRoll:
		return contracts.RoomCommand{
			Type:     CommandRollDice,
			PlayerID: playerID,
		}, nil
	case StatusChoose:
		option, err := bestAdvance(state)
		if err != nil {
			return contracts.RoomCommand{}, err
		}
		return contracts.RoomCommand{
			Type:     CommandAdvance,
			PlayerID: playerID,
			Payload:  map[string]any{"actionID": option.ActionID},
		}, nil
	case StatusDecide:
		return contracts.RoomCommand{
			Type:     CommandBank,
			PlayerID: playerID,
		}, nil
	default:
		return contracts.RoomCommand{}, errors.New("timeout is not available")
	}
}
