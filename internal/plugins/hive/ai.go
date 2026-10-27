package hive

import (
	"context"
	"errors"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) Decide(
	ctx context.Context,
	request game.AIRequest,
) (contracts.RoomCommand, error) {
	state, err := asState(request.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	if state.Status != StatusPlaying ||
		state.CurrentPlayer != request.PlayerID {
		return contracts.RoomCommand{}, errors.New("AI is not the active player")
	}
	option, ok := bestAction(ctx, state)
	if !ok {
		return contracts.RoomCommand{}, errors.New("AI found no legal action")
	}
	commandType := CommandMovePiece
	var payload any = map[string]any{
		"pieceID": option.PieceID,
		"to":      option.To,
	}
	if option.Kind == "place" {
		commandType = CommandPlacePiece
	}
	return contracts.RoomCommand{
		Type:     commandType,
		PlayerID: request.PlayerID,
		Payload:  payload,
	}, nil
}

func bestAction(ctx context.Context, state *State) (candidate, bool) {
	options := legalActions(state)
	if len(options) == 0 {
		return candidate{}, false
	}
	best := options[0]
	bestScore := -1 << 30
	found := false
	activeSide := state.CurrentSide
	for _, option := range options {
		if ctx.Err() != nil {
			break
		}
		next := cloneState(state)
		if !applyCandidate(next, option, true) {
			continue
		}
		score := evaluateAction(next, option, activeSide)
		if !found || score > bestScore {
			best = option
			bestScore = score
			found = true
		}
	}
	if !found {
		return options[0], true
	}
	return best, true
}

func evaluateAction(state *State, option candidate, side Side) int {
	rival := other(side)
	if state.Status == StatusEnd {
		if state.Draw {
			return 1
		}
		if state.WinnerSide == side {
			return 100_000
		}
		return -100_000
	}
	score := queenPressure(state, rival)*13 -
		queenPressure(state, side)*17
	if queenPressure(state, side) == 5 {
		score -= 90
	}
	piece := piecesByID[option.PieceID]
	if enemyQueen, placed := queenLocation(state, rival); placed {
		score -= distance(option.To, enemyQueen) * 2
	}
	if option.Kind == "place" {
		score += 3
		if piece.Kind == KindQueen {
			if state.Turns[side] >= 2 {
				score += 20
			} else {
				score += 4
			}
		}
	}
	if piece.Kind == KindBeetle &&
		stackHeight(state.Board, option.To) > 1 {
		score += 5
	}
	if enemyQueen, placed := queenLocation(state, rival); placed {
		top, ok := topPiece(state.Board, enemyQueen)
		if ok && (top.Side != rival || top.Kind != KindQueen) {
			score += 9
		}
	}
	return score
}
