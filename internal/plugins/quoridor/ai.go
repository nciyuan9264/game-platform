package quoridor

import (
	"context"
	"errors"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const searchWidth = 12

type scoredCandidate struct {
	action candidate
	score  int
}

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
	action, ok := bestAction(
		ctx,
		state,
		sideForPlayer(state, request.PlayerID),
	)
	if !ok {
		return contracts.RoomCommand{}, errors.New("AI found no legal action")
	}
	if action.Kind == "move" {
		return contracts.RoomCommand{
			Type:     CommandMovePawn,
			PlayerID: request.PlayerID,
			Payload:  map[string]any{"to": action.To},
		}, nil
	}
	return contracts.RoomCommand{
		Type:     CommandPlaceWall,
		PlayerID: request.PlayerID,
		Payload:  map[string]any{"wall": action.Wall},
	}, nil
}

func bestAction(
	ctx context.Context,
	state *State,
	side Side,
) (candidate, bool) {
	fallback, ok := fallbackMove(state, side)
	if !ok {
		return candidate{}, false
	}
	candidates := allCandidates(state, side)
	if len(candidates) == 0 {
		return candidate{}, false
	}
	ordered := prescoreCandidates(ctx, state, side, candidates)
	if len(ordered) == 0 {
		return fallback, true
	}
	if len(ordered) > searchWidth {
		ordered = ordered[:searchWidth]
	}
	best := ordered[0]
	found := false
	for _, current := range ordered {
		if ctx.Err() != nil {
			break
		}
		next := cloneState(state)
		if !applyCandidate(next, side, current.action) {
			continue
		}
		score := evaluateState(next, side)
		if next.Status != StatusEnd {
			opponent := other(side)
			replies := prescoreCandidates(
				ctx,
				next,
				opponent,
				allCandidates(next, opponent),
			)
			if len(replies) > searchWidth {
				replies = replies[:searchWidth]
			}
			if len(replies) > 0 {
				worst := 1 << 30
				for _, reply := range replies {
					if ctx.Err() != nil {
						break
					}
					afterReply := cloneState(next)
					if !applyCandidate(
						afterReply,
						opponent,
						reply.action,
					) {
						continue
					}
					value := evaluateState(afterReply, side)
					if value < worst {
						worst = value
					}
				}
				if worst != 1<<30 {
					score = worst
				}
			}
		}
		if !found || score > best.score ||
			(score == best.score &&
				candidateLess(current.action, best.action)) {
			best = scoredCandidate{action: current.action, score: score}
			found = true
		}
	}
	if !found {
		return fallback, true
	}
	return best.action, true
}

func prescoreCandidates(
	ctx context.Context,
	state *State,
	side Side,
	candidates []candidate,
) []scoredCandidate {
	result := make([]scoredCandidate, 0, len(candidates))
	opponentThreat := hasImmediateWin(state, other(side))
	for _, action := range candidates {
		if ctx.Err() != nil {
			break
		}
		next := cloneState(state)
		if !applyCandidate(next, side, action) {
			continue
		}
		score := evaluateState(next, side)
		if opponentThreat {
			if hasImmediateWin(next, other(side)) {
				score -= 10_000
			} else {
				score += 10_000
			}
		}
		result = append(result, scoredCandidate{
			action: action,
			score:  score,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].score != result[j].score {
			return result[i].score > result[j].score
		}
		return candidateLess(result[i].action, result[j].action)
	})
	return result
}

func hasImmediateWin(state *State, side Side) bool {
	if state.Status != StatusPlaying {
		return false
	}
	for _, move := range legalMoves(state, side) {
		if move.Row == targetRow(side) {
			return true
		}
	}
	return false
}

func evaluateState(state *State, perspective Side) int {
	if state.Status == StatusEnd {
		if state.WinnerSide == perspective {
			return 1_000_000 - len(state.Log)
		}
		return -1_000_000 + len(state.Log)
	}
	opponent := other(perspective)
	selfPath := shortestPath(state, perspective)
	opponentPath := shortestPath(state, opponent)
	if len(selfPath) == 0 {
		return -900_000
	}
	if len(opponentPath) == 0 {
		return 900_000
	}
	selfDistance := len(selfPath) - 1
	opponentDistance := len(opponentPath) - 1
	score := (opponentDistance - selfDistance) * 110
	score += (state.WallStock[perspective] -
		state.WallStock[opponent]) * 9
	selfPawn := state.Pawns[perspective]
	opponentPawn := state.Pawns[opponent]
	score -= abs(selfPawn.Column-4) * 2
	score += abs(opponentPawn.Column - 4)
	if state.LastAction != nil &&
		state.LastAction.Kind == "move" &&
		state.LastAction.Side == perspective &&
		state.LastAction.From != nil &&
		state.LastAction.To != nil {
		before := abs(state.LastAction.From.Row - targetRow(perspective))
		after := abs(state.LastAction.To.Row - targetRow(perspective))
		score += (before - after) * 18
	}
	return score
}

func fallbackMove(state *State, side Side) (candidate, bool) {
	moves := legalMoves(state, side)
	if len(moves) == 0 {
		return candidate{}, false
	}
	sort.SliceStable(moves, func(i, j int) bool {
		leftPath := shortestPathFrom(state, side, moves[i])
		rightPath := shortestPathFrom(state, side, moves[j])
		leftDistance, rightDistance := 1<<30, 1<<30
		if len(leftPath) > 0 {
			leftDistance = len(leftPath) - 1
		}
		if len(rightPath) > 0 {
			rightDistance = len(rightPath) - 1
		}
		if leftDistance != rightDistance {
			return leftDistance < rightDistance
		}
		if moves[i].Row != moves[j].Row {
			return moves[i].Row < moves[j].Row
		}
		return moves[i].Column < moves[j].Column
	})
	return candidate{Kind: "move", To: moves[0]}, true
}

func candidateLess(left, right candidate) bool {
	values := []candidate{left, right}
	sortCandidates(values)
	return values[0] == left && left != right
}
