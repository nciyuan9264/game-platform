package yinsh

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const aiSearchBudget = 900 * time.Millisecond

type moveCandidate struct {
	From  string
	Move  MoveOption
	Score int
}

func (p *Plugin) Decide(ctx context.Context, request game.AIRequest) (contracts.RoomCommand, error) {
	state, err := asState(request.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	if state.CurrentPlayer != request.PlayerID {
		return contracts.RoomCommand{}, errors.New("AI is not the active player")
	}
	switch state.Status {
	case StatusPlacement:
		pointID := bestPlacement(state, request.PlayerID)
		if pointID == "" {
			return contracts.RoomCommand{}, errors.New("AI found no placement")
		}
		return contracts.RoomCommand{
			Type:     CommandPlaceRing,
			PlayerID: request.PlayerID,
			Payload:  map[string]any{"pointID": pointID},
		}, nil
	case StatusMove:
		candidate, ok := bestMove(ctx, state, request.PlayerID)
		if !ok {
			return contracts.RoomCommand{}, errors.New("AI found no legal move")
		}
		return contracts.RoomCommand{
			Type:     CommandMoveRing,
			PlayerID: request.PlayerID,
			Payload: map[string]any{
				"from": candidate.From,
				"to":   candidate.Move.To,
			},
		}, nil
	case StatusRemoveRow:
		row := bestRow(state, request.PlayerID)
		if row.ID == "" {
			return contracts.RoomCommand{}, errors.New("AI found no removable row")
		}
		return contracts.RoomCommand{
			Type:     CommandRemoveRow,
			PlayerID: request.PlayerID,
			Payload:  map[string]any{"rowID": row.ID},
		}, nil
	case StatusRemoveRing:
		pointID := bestRingToRemove(state, request.PlayerID)
		if pointID == "" {
			return contracts.RoomCommand{}, errors.New("AI found no removable ring")
		}
		return contracts.RoomCommand{
			Type:     CommandRemoveRing,
			PlayerID: request.PlayerID,
			Payload:  map[string]any{"pointID": pointID},
		}, nil
	default:
		return contracts.RoomCommand{}, errors.New("AI has no action in current phase")
	}
}

func bestPlacement(state *State, playerID string) string {
	side := sideForPlayer(state, playerID)
	opponent := other(side)
	bestID := ""
	bestScore := -1 << 30
	for _, pointID := range emptyPointIDs(state) {
		score := (5-pointDistance(pointID))*18 + placementSpread(state, side, pointID)*5
		score += placementPressure(state, opponent, pointID) * 3
		if score > bestScore || score == bestScore && pointID < bestID {
			bestID = pointID
			bestScore = score
		}
	}
	return bestID
}

func placementSpread(state *State, side Side, pointID string) int {
	point := pointByID[pointID]
	total := 0
	for existingID, owner := range state.Rings {
		if owner != side {
			continue
		}
		existing := pointByID[existingID]
		total += max3(
			abs(point.Q-existing.Q),
			abs(point.R-existing.R),
			abs(point.Q+point.R-existing.Q-existing.R),
		)
	}
	return total
}

func placementPressure(state *State, side Side, pointID string) int {
	point := pointByID[pointID]
	score := 0
	for existingID, owner := range state.Rings {
		if owner != side {
			continue
		}
		existing := pointByID[existingID]
		distance := max3(
			abs(point.Q-existing.Q),
			abs(point.R-existing.R),
			abs(point.Q+point.R-existing.Q-existing.R),
		)
		if distance <= 2 {
			score++
		}
	}
	return score
}

func bestMove(ctx context.Context, state *State, playerID string) (moveCandidate, bool) {
	deadline := time.Now().Add(aiSearchBudget)
	side := sideForPlayer(state, playerID)
	candidates := generateMoveCandidates(state, playerID)
	if len(candidates) == 0 {
		return moveCandidate{}, false
	}
	best := candidates[0]
	best.Score = -1 << 30
	for index := range candidates {
		if ctx.Err() != nil || time.Now().After(deadline) {
			break
		}
		candidate := &candidates[index]
		next := cloneState(state)
		if err := moveRing(next, playerID, candidate.From, candidate.Move.To); err != nil {
			continue
		}
		score := evaluateState(next, side)
		if next.Status == StatusMove && next.CurrentPlayer != "" {
			score -= bestOpponentReply(ctx, next, side, deadline)
		}
		candidate.Score = score
		if score > best.Score || score == best.Score && moveLess(*candidate, best) {
			best = *candidate
		}
	}
	return best, true
}

func bestOpponentReply(ctx context.Context, state *State, perspective Side, deadline time.Time) int {
	opponentID := state.CurrentPlayer
	candidates := generateMoveCandidates(state, opponentID)
	best := -1 << 30
	for index, candidate := range candidates {
		if index >= 96 || ctx.Err() != nil || time.Now().After(deadline) {
			break
		}
		next := cloneState(state)
		if moveRing(next, opponentID, candidate.From, candidate.Move.To) != nil {
			continue
		}
		score := evaluateState(next, other(perspective))
		if score > best {
			best = score
		}
	}
	if best == -1<<30 {
		return 0
	}
	return best / 2
}

func generateMoveCandidates(state *State, playerID string) []moveCandidate {
	moves := allLegalMoves(state, playerID)
	fromIDs := make([]string, 0, len(moves))
	for from := range moves {
		fromIDs = append(fromIDs, from)
	}
	sort.Strings(fromIDs)
	result := []moveCandidate{}
	for _, from := range fromIDs {
		for _, option := range moves[from] {
			result = append(result, moveCandidate{From: from, Move: option})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		leftImmediate := len(result[i].Move.Flips)
		rightImmediate := len(result[j].Move.Flips)
		if leftImmediate != rightImmediate {
			return leftImmediate > rightImmediate
		}
		return moveLess(result[i], result[j])
	})
	return result
}

func moveLess(left, right moveCandidate) bool {
	if left.From != right.From {
		return left.From < right.From
	}
	return left.Move.To < right.Move.To
}

func evaluateState(state *State, perspective Side) int {
	if state.Status == StatusEnd {
		if state.WinnerID == "" {
			return 0
		}
		if sideForPlayer(state, state.WinnerID) == perspective {
			return 1_000_000
		}
		return -1_000_000
	}
	opponent := other(perspective)
	score := (state.RingsRemoved[perspective] - state.RingsRemoved[opponent]) * 100_000
	score += (len(rowsFor(state, perspective)) - len(rowsFor(state, opponent))) * 12_000
	score += (linePotential(state, perspective) - linePotential(state, opponent)) * 160
	score += (markerCount(state, perspective) - markerCount(state, opponent)) * 8
	score += (sideMobility(state, perspective) - sideMobility(state, opponent)) * 3
	score += ringPositionScore(state, perspective) - ringPositionScore(state, opponent)
	return score
}

func linePotential(state *State, side Side) int {
	total := 0
	for _, start := range boardPoints {
		for _, direction := range directions[:3] {
			count := 0
			valid := true
			for offset := 0; offset < 5; offset++ {
				point, ok := pointByCoord[[2]int{
					start.Q + offset*direction[0],
					start.R + offset*direction[1],
				}]
				if !ok || state.Rings[point.ID] != "" {
					valid = false
					break
				}
				if state.Markers[point.ID] == side {
					count++
				} else if state.Markers[point.ID] != "" {
					valid = false
					break
				}
			}
			if valid {
				total += count * count
			}
		}
	}
	return total
}

func markerCount(state *State, side Side) int {
	total := 0
	for _, owner := range state.Markers {
		if owner == side {
			total++
		}
	}
	return total
}

func sideMobility(state *State, side Side) int {
	playerID := playerForSide(state, side)
	total := 0
	for _, options := range allLegalMoves(state, playerID) {
		total += len(options)
	}
	return total
}

func ringPositionScore(state *State, side Side) int {
	score := 0
	for pointID, owner := range state.Rings {
		if owner == side {
			score += (5 - pointDistance(pointID)) * 4
		}
	}
	return score
}

func bestRow(state *State, playerID string) Row {
	side := sideForPlayer(state, playerID)
	rows := rowsFor(state, side)
	if len(rows) == 0 {
		return Row{}
	}
	best := rows[0]
	bestScore := -1 << 30
	for _, row := range rows {
		next := cloneState(state)
		for _, pointID := range row.Points {
			delete(next.Markers, pointID)
		}
		score := evaluateState(next, side)
		if score > bestScore || score == bestScore && row.ID < best.ID {
			best = row
			bestScore = score
		}
	}
	return best
}

func bestRingToRemove(state *State, playerID string) string {
	side := sideForPlayer(state, playerID)
	rings := ringIDs(state, side)
	bestID := ""
	bestCost := 1 << 30
	for _, pointID := range rings {
		cost := len(legalMoves(state, pointID))*10 + (5-pointDistance(pointID))*3
		if cost < bestCost || cost == bestCost && pointID < bestID {
			bestID = pointID
			bestCost = cost
		}
	}
	return bestID
}
