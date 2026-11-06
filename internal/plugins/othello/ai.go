package othello

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

var positionWeights = [64]int{
	120, -35, 20, 5, 5, 20, -35, 120,
	-35, -60, -5, -5, -5, -5, -60, -35,
	20, -5, 15, 3, 3, 15, -5, 20,
	5, -5, 3, 3, 3, 3, -5, 5,
	5, -5, 3, 3, 3, 3, -5, 5,
	20, -5, 15, 3, 3, 15, -5, 20,
	-35, -60, -5, -5, -5, -5, -60, -35,
	120, -35, 20, 5, 5, 20, -35, 120,
}

func (p *Plugin) Decide(
	ctx context.Context,
	request game.AIRequest,
) (contracts.RoomCommand, error) {
	state, err := asState(request.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	if state.CurrentPlayer != request.PlayerID {
		return contracts.RoomCommand{}, errors.New("AI is not the active player")
	}
	switch state.Status {
	case StatusPass:
		return contracts.RoomCommand{
			Type:     CommandPass,
			PlayerID: request.PlayerID,
		}, nil
	case StatusPlaying:
		player := state.Roster[request.PlayerID]
		if player == nil || !player.AI {
			return contracts.RoomCommand{}, errors.New("AI player is unavailable")
		}
		side := sideForPlayer(state, request.PlayerID)
		var move Move
		var ok bool
		switch player.AIDifficulty {
		case game.AIDifficultyEasy:
			move, ok = bestEasyMove(state, request.PlayerID, side)
		case game.AIDifficultyHard:
			move, ok = bestHardMove(ctx, state, side)
		default:
			move, ok = bestMediumMove(ctx, state, side)
		}
		if !ok {
			return contracts.RoomCommand{}, errors.New("AI found no legal move")
		}
		return contracts.RoomCommand{
			Type:     CommandPlaceDisc,
			PlayerID: request.PlayerID,
			Payload:  map[string]any{"index": move.Index},
		}, nil
	default:
		return contracts.RoomCommand{}, errors.New("AI has no action in current phase")
	}
}

func bestEasyMove(state *State, playerID string, side Side) (Move, bool) {
	moves := sortedMoves(legalMoves(state.Board, side))
	if len(moves) == 0 {
		return Move{}, false
	}
	hasher := fnv.New64a()
	_, _ = fmt.Fprintf(
		hasher,
		"%d:%d:%d:%s",
		state.RandomSeed,
		state.RoundNo,
		len(state.Log),
		playerID,
	)
	return moves[int(hasher.Sum64()%uint64(len(moves)))], true
}

func bestMediumMove(ctx context.Context, state *State, side Side) (Move, bool) {
	moves := sortedMoves(legalMoves(state.Board, side))
	if len(moves) == 0 {
		return Move{}, false
	}
	type scoredMove struct {
		move  Move
		score int
	}
	scored := make([]scoredMove, 0, len(moves))
	for _, move := range moves {
		if ctx.Err() != nil {
			break
		}
		next, err := applyMove(state.Board, move, side)
		if err != nil {
			continue
		}
		scored = append(scored, scoredMove{
			move:  move,
			score: evaluateBoard(next, side, move),
		})
	}
	if len(scored) == 0 {
		return moves[0], true
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].move.Index < scored[j].move.Index
	})
	return scored[0].move, true
}

func bestHardMove(ctx context.Context, state *State, side Side) (Move, bool) {
	if state.Analysis != nil &&
		state.Analysis.Status == analysisStatusReady &&
		state.Analysis.PositionKey == positionKey(state) &&
		state.Analysis.BestMove != nil {
		move := moveAt(state.Board, *state.Analysis.BestMove, side)
		if len(move.Flips) > 0 {
			return move, true
		}
	}
	return bestMediumMove(ctx, state, side)
}

func evaluateBoard(board []Disc, side Side, move Move) int {
	opponent := other(side)
	black, white, empty := counts(board)
	selfCount, opponentCount := black, white
	if side == SideWhite {
		selfCount, opponentCount = white, black
	}
	score := positionWeights[move.Index] * 20
	score += len(move.Flips) * 3
	score += (len(legalMoves(board, side)) - len(legalMoves(board, opponent))) * 18
	score += (frontierCount(board, opponent) - frontierCount(board, side)) * 8
	score += (stableEdgeCount(board, side) - stableEdgeCount(board, opponent)) * 30
	if empty <= 12 {
		score += (selfCount - opponentCount) * 45
		if empty%2 == 1 {
			score += 10
		}
	}
	if len(legalMoves(board, opponent)) == 0 {
		if len(legalMoves(board, side)) == 0 {
			if selfCount > opponentCount {
				score += 1_000_000
			} else if selfCount < opponentCount {
				score -= 1_000_000
			}
		} else {
			score += 220
		}
	}
	return score
}

func frontierCount(board []Disc, side Side) int {
	disc := discFor(side)
	total := 0
	for index, value := range board {
		if value != disc {
			continue
		}
		row, column := index/8, index%8
		for _, direction := range directions {
			nextRow, nextColumn := row+direction[0], column+direction[1]
			if inside(nextRow, nextColumn) &&
				board[position(nextRow, nextColumn)] == DiscEmpty {
				total++
				break
			}
		}
	}
	return total
}

func stableEdgeCount(board []Disc, side Side) int {
	disc := discFor(side)
	total := 0
	for _, corner := range []struct {
		index int
		stepA int
		stepB int
	}{
		{0, 1, 8},
		{7, -1, 8},
		{56, 1, -8},
		{63, -1, -8},
	} {
		if board[corner.index] != disc {
			continue
		}
		total++
		for _, step := range []int{corner.stepA, corner.stepB} {
			for index := corner.index + step; index >= 0 && index < 64; index += step {
				if board[index] != disc {
					break
				}
				total++
				if step == 1 || step == -1 {
					if index/8 != corner.index/8 {
						break
					}
				}
			}
		}
	}
	return total
}
