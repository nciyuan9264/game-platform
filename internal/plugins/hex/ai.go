package hex

import (
	"container/heap"
	"context"
	"errors"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const aiSearchWidth = 18

type scoredCell struct {
	cell  Cell
	score int
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
	if canSwap(state, request.PlayerID) && shouldSwap(state) {
		return contracts.RoomCommand{
			Type:     CommandSwap,
			PlayerID: request.PlayerID,
		}, nil
	}
	cell, ok := bestCell(ctx, state, sideForPlayer(state, request.PlayerID))
	if !ok {
		return contracts.RoomCommand{}, errors.New("AI found no legal move")
	}
	return contracts.RoomCommand{
		Type:     CommandPlaceStone,
		PlayerID: request.PlayerID,
		Payload:  map[string]any{"cell": cell},
	}, nil
}

func shouldSwap(state *State) bool {
	if len(state.Log) != 1 || state.Log[0].Cell == nil {
		return false
	}
	center := boardSize / 2
	cell := *state.Log[0].Cell
	return abs(cell.Row-center)+abs(cell.Column-center) <= boardSize/2
}

func bestCell(ctx context.Context, state *State, side Side) (Cell, bool) {
	legal := sortedCells(legalCells(state.Board))
	if len(legal) == 0 {
		return Cell{}, false
	}
	for _, cell := range legal {
		board := append([]Stone(nil), state.Board...)
		board[position(cell)] = stoneFor(side)
		if len(connection(board, side)) > 0 {
			return cell, true
		}
	}
	opponent := other(side)
	for _, cell := range legal {
		board := append([]Stone(nil), state.Board...)
		board[position(cell)] = stoneFor(opponent)
		if len(connection(board, opponent)) > 0 {
			return cell, true
		}
	}
	scored := make([]scoredCell, 0, len(legal))
	for _, cell := range legal {
		if ctx.Err() != nil {
			break
		}
		board := append([]Stone(nil), state.Board...)
		board[position(cell)] = stoneFor(side)
		scored = append(scored, scoredCell{
			cell:  cell,
			score: evaluateBoard(board, side, cell),
		})
	}
	if len(scored) == 0 {
		return legal[0], true
	}
	sortScored(scored)
	if len(scored) > aiSearchWidth {
		scored = scored[:aiSearchWidth]
	}
	best := scoredCell{score: -(1 << 30)}
	evaluated := false
	for _, candidate := range scored {
		if ctx.Err() != nil {
			break
		}
		evaluated = true
		board := append([]Stone(nil), state.Board...)
		board[position(candidate.cell)] = stoneFor(side)
		worst := candidate.score
		replies := topReplies(ctx, board, opponent)
		if len(replies) > 0 {
			worst = 1 << 30
			for _, reply := range replies {
				after := append([]Stone(nil), board...)
				after[position(reply.cell)] = stoneFor(opponent)
				score := evaluatePosition(after, side)
				if score < worst {
					worst = score
				}
			}
		}
		if worst > best.score ||
			(worst == best.score && cellLess(candidate.cell, best.cell)) {
			best = scoredCell{cell: candidate.cell, score: worst}
		}
	}
	if !evaluated {
		return scored[0].cell, true
	}
	return best.cell, true
}

func topReplies(ctx context.Context, board []Stone, side Side) []scoredCell {
	result := []scoredCell{}
	for _, cell := range legalCells(board) {
		if ctx.Err() != nil {
			break
		}
		next := append([]Stone(nil), board...)
		next[position(cell)] = stoneFor(side)
		result = append(result, scoredCell{
			cell:  cell,
			score: evaluateBoard(next, side, cell),
		})
	}
	sortScored(result)
	if len(result) > aiSearchWidth {
		result = result[:aiSearchWidth]
	}
	return result
}

func evaluateBoard(board []Stone, side Side, cell Cell) int {
	score := evaluatePosition(board, side)
	center := boardSize / 2
	score -= (abs(cell.Row-center) + abs(cell.Column-center)) * 2
	for _, neighbor := range neighbors(cell) {
		switch board[position(neighbor)] {
		case stoneFor(side):
			score += 9
		case stoneFor(other(side)):
			score += 5
		}
	}
	return score
}

func evaluatePosition(board []Stone, side Side) int {
	if len(connection(board, side)) > 0 {
		return 1_000_000
	}
	if len(connection(board, other(side))) > 0 {
		return -1_000_000
	}
	selfDistance := connectionDistance(board, side)
	opponentDistance := connectionDistance(board, other(side))
	return (opponentDistance-selfDistance)*120 +
		(connectedPairs(board, side)-connectedPairs(board, other(side)))*4
}

func connectedPairs(board []Stone, side Side) int {
	stone := stoneFor(side)
	total := 0
	for index, value := range board {
		if value != stone {
			continue
		}
		for _, neighbor := range neighbors(cellAt(index)) {
			if position(neighbor) > index && board[position(neighbor)] == stone {
				total++
			}
		}
	}
	return total
}

type distanceNode struct {
	index    int
	distance int
}

type distanceQueue []distanceNode

func (q distanceQueue) Len() int { return len(q) }
func (q distanceQueue) Less(i, j int) bool {
	if q[i].distance != q[j].distance {
		return q[i].distance < q[j].distance
	}
	return q[i].index < q[j].index
}
func (q distanceQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *distanceQueue) Push(value any) {
	*q = append(*q, value.(distanceNode))
}
func (q *distanceQueue) Pop() any {
	old := *q
	value := old[len(old)-1]
	*q = old[:len(old)-1]
	return value
}

func connectionDistance(board []Stone, side Side) int {
	const infinity = 1 << 20
	distances := make([]int, boardSize*boardSize)
	for index := range distances {
		distances[index] = infinity
	}
	queue := &distanceQueue{}
	heap.Init(queue)
	for offset := 0; offset < boardSize; offset++ {
		cell := Cell{Row: 0, Column: offset}
		if side == SideTeal {
			cell = Cell{Row: offset, Column: 0}
		}
		index := position(cell)
		cost := stoneCost(board[index], side)
		if cost >= infinity {
			continue
		}
		distances[index] = cost
		heap.Push(queue, distanceNode{index: index, distance: cost})
	}
	for queue.Len() > 0 {
		node := heap.Pop(queue).(distanceNode)
		if node.distance != distances[node.index] {
			continue
		}
		cell := cellAt(node.index)
		if side == SideRed && cell.Row == boardSize-1 ||
			side == SideTeal && cell.Column == boardSize-1 {
			return node.distance
		}
		for _, neighbor := range neighbors(cell) {
			index := position(neighbor)
			cost := stoneCost(board[index], side)
			if cost >= infinity || node.distance+cost >= distances[index] {
				continue
			}
			distances[index] = node.distance + cost
			heap.Push(queue, distanceNode{
				index: index, distance: distances[index],
			})
		}
	}
	return infinity
}

func stoneCost(stone Stone, side Side) int {
	if stone == stoneFor(other(side)) {
		return 1 << 20
	}
	if stone == stoneFor(side) {
		return 0
	}
	return 1
}

func sortScored(values []scoredCell) {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].score != values[j].score {
			return values[i].score > values[j].score
		}
		return cellLess(values[i].cell, values[j].cell)
	})
}

func cellLess(left, right Cell) bool {
	return left.Row < right.Row ||
		left.Row == right.Row && left.Column < right.Column
}
