package hex

import (
	"errors"
	"sort"
	"time"
)

var neighborOffsets = []Cell{
	{Row: -1, Column: 0},
	{Row: -1, Column: 1},
	{Row: 0, Column: -1},
	{Row: 0, Column: 1},
	{Row: 1, Column: -1},
	{Row: 1, Column: 0},
}

func freshBoard() []Stone {
	return make([]Stone, boardSize*boardSize)
}

func position(cell Cell) int {
	return cell.Row*boardSize + cell.Column
}

func cellAt(index int) Cell {
	return Cell{Row: index / boardSize, Column: index % boardSize}
}

func inside(cell Cell) bool {
	return cell.Row >= 0 && cell.Row < boardSize &&
		cell.Column >= 0 && cell.Column < boardSize
}

func neighbors(cell Cell) []Cell {
	result := make([]Cell, 0, 6)
	for _, offset := range neighborOffsets {
		next := Cell{
			Row:    cell.Row + offset.Row,
			Column: cell.Column + offset.Column,
		}
		if inside(next) {
			result = append(result, next)
		}
	}
	return result
}

func connection(board []Stone, side Side) []Cell {
	if len(board) != boardSize*boardSize {
		return nil
	}
	stone := stoneFor(side)
	queue := make([]Cell, 0, boardSize*boardSize)
	previous := map[int]int{}
	for offset := 0; offset < boardSize; offset++ {
		cell := Cell{Row: 0, Column: offset}
		if side == SideTeal {
			cell = Cell{Row: offset, Column: 0}
		}
		index := position(cell)
		if board[index] == stone {
			queue = append(queue, cell)
			previous[index] = -1
		}
	}
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		reached := current.Row == boardSize-1
		if side == SideTeal {
			reached = current.Column == boardSize-1
		}
		if reached {
			path := []Cell{current}
			for index := previous[position(current)]; index >= 0; index = previous[index] {
				current = cellAt(index)
				path = append(path, current)
			}
			reverseCells(path)
			return path
		}
		for _, next := range neighbors(current) {
			index := position(next)
			if board[index] != stone {
				continue
			}
			if _, seen := previous[index]; seen {
				continue
			}
			previous[index] = position(current)
			queue = append(queue, next)
		}
	}
	return []Cell{}
}

func legalCells(board []Stone) []Cell {
	result := []Cell{}
	for index, stone := range board {
		if stone == StoneEmpty {
			result = append(result, cellAt(index))
		}
	}
	return result
}

func canSwap(state *State, playerID string) bool {
	return state.Status == StatusPlaying &&
		state.CurrentPlayer == playerID &&
		playerID == state.TealPlayerID &&
		len(state.Log) == 1 &&
		state.Log[0].Kind == "place" &&
		state.WinnerID == ""
}

func placeStone(
	state *State,
	playerID string,
	cell Cell,
	automatic bool,
) error {
	if state.Status != StatusPlaying {
		return errors.New("not in playing phase")
	}
	if state.CurrentPlayer != playerID {
		return errors.New("not the active player")
	}
	if !inside(cell) || len(state.Board) != boardSize*boardSize {
		return errors.New("cell is out of bounds")
	}
	index := position(cell)
	if state.Board[index] != StoneEmpty {
		return errors.New("cell is already occupied")
	}
	side := sideForPlayer(state, playerID)
	state.Board[index] = stoneFor(side)
	actionCell := cell
	appendAction(state, Action{
		Kind:      "place",
		PlayerID:  playerID,
		Side:      side,
		Cell:      &actionCell,
		Automatic: automatic,
	})
	path := connection(state.Board, side)
	if len(path) > 0 {
		state.WinningPath = path
		finish(
			state,
			playerID,
			side,
			sideName(side)+"连通两岸，赢得对局。",
		)
		return nil
	}
	state.CurrentPlayer = playerForSide(state, other(side))
	return nil
}

func swapSides(state *State, playerID string, automatic bool) error {
	if !canSwap(state, playerID) {
		return errors.New("swap is not available")
	}
	actionSide := sideForPlayer(state, playerID)
	appendAction(state, Action{
		Kind:      "swap",
		PlayerID:  playerID,
		Side:      actionSide,
		Automatic: automatic,
	})
	state.RedPlayerID, state.TealPlayerID =
		state.TealPlayerID, state.RedPlayerID
	state.Roster[state.RedPlayerID].Side = SideRed
	state.Roster[state.TealPlayerID].Side = SideTeal
	state.CurrentPlayer = state.TealPlayerID
	return nil
}

func resign(state *State, playerID string) error {
	if state.Status != StatusPlaying {
		return errors.New("game is not active")
	}
	player := state.Roster[playerID]
	if player == nil ||
		(player.Side != SideRed && player.Side != SideTeal) {
		return errors.New("player not found")
	}
	appendAction(state, Action{
		Kind:     "resign",
		PlayerID: playerID,
		Side:     player.Side,
	})
	winnerSide := other(player.Side)
	state.WinningPath = []Cell{}
	finish(
		state,
		playerForSide(state, winnerSide),
		winnerSide,
		sideName(player.Side)+"主动认输，对局结束。",
	)
	return nil
}

func finish(state *State, winnerID string, side Side, reason string) {
	state.Status = StatusEnd
	state.WinnerID = winnerID
	state.WinnerSide = side
	state.EndReason = reason
	state.CurrentPlayer = ""
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
}

func appendAction(state *State, action Action) {
	action.ID = len(state.Log) + 1
	value := cloneAction(action)
	state.Log = append(state.Log, value)
	state.LastAction = &value
}

func sideForPlayer(state *State, playerID string) Side {
	if player := state.Roster[playerID]; player != nil {
		return player.Side
	}
	if playerID == state.RedPlayerID {
		return SideRed
	}
	if playerID == state.TealPlayerID {
		return SideTeal
	}
	return ""
}

func playerForSide(state *State, side Side) string {
	if side == SideRed {
		return state.RedPlayerID
	}
	if side == SideTeal {
		return state.TealPlayerID
	}
	return ""
}

func sideName(side Side) string {
	if side == SideRed {
		return "陶红"
	}
	return "湖绿"
}

func stoneCounts(board []Stone) (red, teal int) {
	for _, stone := range board {
		switch stone {
		case StoneRed:
			red++
		case StoneTeal:
			teal++
		}
	}
	return red, teal
}

func sortedCells(values []Cell) []Cell {
	result := append([]Cell(nil), values...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Row != result[j].Row {
			return result[i].Row < result[j].Row
		}
		return result[i].Column < result[j].Column
	})
	return result
}

func reverseCells(values []Cell) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
