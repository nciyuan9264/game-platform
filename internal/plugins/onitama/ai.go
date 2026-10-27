package onitama

import (
	"context"
	"errors"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const searchInfinity = 1_000_000_000

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
		return contracts.RoomCommand{}, errors.New(
			"AI is not the active player",
		)
	}
	side := sideForPlayer(state, request.PlayerID)
	action, ok := bestAction(ctx, state, side)
	if !ok {
		return contracts.RoomCommand{}, errors.New(
			"AI found no legal action",
		)
	}
	if action.Kind == "pass" {
		return contracts.RoomCommand{
			Type:     CommandPass,
			PlayerID: request.PlayerID,
			Payload: map[string]any{
				"cardID": action.CardID,
			},
		}, nil
	}
	return contracts.RoomCommand{
		Type:     CommandMovePiece,
		PlayerID: request.PlayerID,
		Payload: map[string]any{
			"cardID":  action.CardID,
			"pieceID": action.PieceID,
			"to":      action.To,
		},
	}, nil
}

func bestAction(
	ctx context.Context,
	state *State,
	side Side,
) (candidate, bool) {
	actions := allCandidates(state, side)
	if len(actions) == 0 {
		return candidate{}, false
	}
	best := actions[0]
	bestScore := -searchInfinity
	for _, action := range actions {
		next := cloneState(state)
		if !applyCandidate(next, side, action, true) {
			continue
		}
		score := evaluate(next, side)
		if score > bestScore {
			best = action
			bestScore = score
		}
	}
	for depth := 1; depth <= 4; depth++ {
		action, _, complete := searchRoot(
			ctx,
			state,
			side,
			depth,
		)
		if !complete {
			break
		}
		best = action
	}
	return best, true
}

func searchRoot(
	ctx context.Context,
	state *State,
	root Side,
	depth int,
) (candidate, int, bool) {
	actions := allCandidates(state, root)
	if len(actions) == 0 {
		return candidate{}, 0, false
	}
	best := actions[0]
	bestScore := -searchInfinity
	alpha := -searchInfinity
	for _, action := range actions {
		if ctx.Err() != nil {
			return candidate{}, 0, false
		}
		next := cloneState(state)
		if !applyCandidate(next, root, action, true) {
			continue
		}
		score, complete := minimax(
			ctx,
			next,
			root,
			depth-1,
			alpha,
			searchInfinity,
		)
		if !complete {
			return candidate{}, 0, false
		}
		if score > bestScore {
			best = action
			bestScore = score
		}
		if score > alpha {
			alpha = score
		}
	}
	return best, bestScore, true
}

func minimax(
	ctx context.Context,
	state *State,
	root Side,
	depth,
	alpha,
	beta int,
) (int, bool) {
	if ctx.Err() != nil {
		return 0, false
	}
	if depth == 0 || state.Status == StatusEnd {
		return evaluate(state, root), true
	}
	current := sideForPlayer(state, state.CurrentPlayer)
	actions := allCandidates(state, current)
	if len(actions) == 0 {
		return evaluate(state, root), true
	}
	if current == root {
		value := -searchInfinity
		for _, action := range actions {
			next := cloneState(state)
			if !applyCandidate(next, current, action, true) {
				continue
			}
			score, complete := minimax(
				ctx,
				next,
				root,
				depth-1,
				alpha,
				beta,
			)
			if !complete {
				return 0, false
			}
			if score > value {
				value = score
			}
			if value > alpha {
				alpha = value
			}
			if alpha >= beta {
				break
			}
		}
		return value, true
	}
	value := searchInfinity
	for _, action := range actions {
		next := cloneState(state)
		if !applyCandidate(next, current, action, true) {
			continue
		}
		score, complete := minimax(
			ctx,
			next,
			root,
			depth-1,
			alpha,
			beta,
		)
		if !complete {
			return 0, false
		}
		if score < value {
			value = score
		}
		if value < beta {
			beta = value
		}
		if alpha >= beta {
			break
		}
	}
	return value, true
}

func evaluate(state *State, root Side) int {
	if state.Status == StatusEnd {
		if state.WinnerSide == root {
			return 10_000_000 - len(state.Log)
		}
		return -10_000_000 + len(state.Log)
	}
	opponent := other(root)
	score := (pieceCount(state, root) - pieceCount(state, opponent)) * 900
	score += (masterDistance(state, opponent) - masterDistance(state, root)) * 80
	score += (mobility(state, root) - mobility(state, opponent)) * 12
	score += (centerControl(state, root) - centerControl(state, opponent)) * 20
	score += (winningMoves(state, root) - winningMoves(state, opponent)) * 5000
	return score
}

func masterDistance(state *State, side Side) int {
	master := state.Pieces[string(side)+"-master"]
	if master == nil {
		return 20
	}
	target := templeFor(side)
	return absolute(master.Cell.Row-target.Row) +
		absolute(master.Cell.Column-target.Column)
}

func mobility(state *State, side Side) int {
	copyState := cloneState(state)
	copyState.Status = StatusPlaying
	return len(legalMoves(copyState, side))
}

func centerControl(state *State, side Side) int {
	score := 0
	for _, piece := range state.Pieces {
		if piece == nil || piece.Side != side {
			continue
		}
		score += 4 - absolute(piece.Cell.Row-2) -
			absolute(piece.Cell.Column-2)
	}
	return score
}

func winningMoves(state *State, side Side) int {
	copyState := cloneState(state)
	copyState.Status = StatusPlaying
	total := 0
	for _, move := range legalMoves(copyState, side) {
		if move.Winning {
			total++
		}
	}
	return total
}

func absolute(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
