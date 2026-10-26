package quoridor

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	wallReasonOverlap    = "overlap"
	wallReasonCrossing   = "crossing"
	wallReasonBlocksPath = "blocks_path"
	wallReasonNoStock    = "no_stock"
)

var cardinalDirections = []Cell{
	{Row: -1, Column: 0},
	{Row: 0, Column: -1},
	{Row: 0, Column: 1},
	{Row: 1, Column: 0},
}

func freshPawns() map[Side]Cell {
	return map[Side]Cell{
		SideSouth: {Row: 8, Column: 4},
		SideNorth: {Row: 0, Column: 4},
	}
}

func freshWallStock() map[Side]int {
	return map[Side]int{
		SideSouth: 10,
		SideNorth: 10,
	}
}

func inBoard(cell Cell) bool {
	return cell.Row >= 0 && cell.Row < 9 &&
		cell.Column >= 0 && cell.Column < 9
}

func validWallSpec(wall WallSpec) bool {
	return wall.Row >= 0 && wall.Row < 8 &&
		wall.Column >= 0 && wall.Column < 8 &&
		(wall.Orientation == OrientationHorizontal ||
			wall.Orientation == OrientationVertical)
}

func sameCell(left, right Cell) bool {
	return left.Row == right.Row && left.Column == right.Column
}

func addCell(left, right Cell) Cell {
	return Cell{
		Row:    left.Row + right.Row,
		Column: left.Column + right.Column,
	}
}

func edgeOpen(walls []Wall, from, to Cell) bool {
	if !inBoard(from) || !inBoard(to) {
		return false
	}
	if abs(from.Row-to.Row)+abs(from.Column-to.Column) != 1 {
		return false
	}
	if from.Row != to.Row {
		row := min(from.Row, to.Row)
		for _, wall := range walls {
			if wall.Orientation == OrientationHorizontal &&
				wall.Row == row &&
				(wall.Column == from.Column ||
					wall.Column+1 == from.Column) {
				return false
			}
		}
		return true
	}
	column := min(from.Column, to.Column)
	for _, wall := range walls {
		if wall.Orientation == OrientationVertical &&
			wall.Column == column &&
			(wall.Row == from.Row || wall.Row+1 == from.Row) {
			return false
		}
	}
	return true
}

func shortestPath(state *State, side Side) []Cell {
	return shortestPathFrom(state, side, state.Pawns[side])
}

func shortestPathFrom(state *State, side Side, start Cell) []Cell {
	if !inBoard(start) {
		return nil
	}
	queue := []Cell{start}
	seen := map[int]bool{cellID(start): true}
	previous := map[int]Cell{}
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		if current.Row == targetRow(side) {
			path := []Cell{current}
			for !sameCell(current, start) {
				value, ok := previous[cellID(current)]
				if !ok {
					return nil
				}
				current = value
				path = append(path, current)
			}
			reverseCells(path)
			return path
		}
		for _, direction := range orderedPathDirections(side) {
			next := addCell(current, direction)
			if !edgeOpen(state.Walls, current, next) ||
				seen[cellID(next)] {
				continue
			}
			seen[cellID(next)] = true
			previous[cellID(next)] = current
			queue = append(queue, next)
		}
	}
	return nil
}

func orderedPathDirections(side Side) []Cell {
	if side == SideSouth {
		return []Cell{
			{Row: -1, Column: 0},
			{Row: 0, Column: -1},
			{Row: 0, Column: 1},
			{Row: 1, Column: 0},
		}
	}
	return []Cell{
		{Row: 1, Column: 0},
		{Row: 0, Column: -1},
		{Row: 0, Column: 1},
		{Row: -1, Column: 0},
	}
}

func legalMoves(state *State, side Side) []Cell {
	if state.Status != StatusPlaying {
		return []Cell{}
	}
	self, selfOK := state.Pawns[side]
	opponent, opponentOK := state.Pawns[other(side)]
	if !selfOK || !opponentOK {
		return []Cell{}
	}
	result := []Cell{}
	add := func(cell Cell) {
		for _, current := range result {
			if sameCell(current, cell) {
				return
			}
		}
		result = append(result, cell)
	}
	for _, direction := range cardinalDirections {
		next := addCell(self, direction)
		if !edgeOpen(state.Walls, self, next) {
			continue
		}
		if !sameCell(next, opponent) {
			add(next)
			continue
		}
		behind := addCell(opponent, direction)
		if edgeOpen(state.Walls, opponent, behind) {
			add(behind)
			continue
		}
		lateral := []Cell{{Row: -1}, {Row: 1}}
		if direction.Row != 0 {
			lateral = []Cell{{Column: -1}, {Column: 1}}
		}
		for _, offset := range lateral {
			target := addCell(opponent, offset)
			if edgeOpen(state.Walls, opponent, target) {
				add(target)
			}
		}
	}
	sortCells(result)
	return result
}

func wallCheck(state *State, side Side, wall WallSpec) (bool, string) {
	if state.Status != StatusPlaying {
		return false, "not_playing"
	}
	if state.WallStock[side] <= 0 {
		return false, wallReasonNoStock
	}
	if !validWallSpec(wall) {
		return false, "out_of_bounds"
	}
	for _, existing := range state.Walls {
		if existing.Orientation != wall.Orientation &&
			existing.Row == wall.Row &&
			existing.Column == wall.Column {
			return false, wallReasonCrossing
		}
		if existing.Orientation != wall.Orientation {
			continue
		}
		if wall.Orientation == OrientationHorizontal &&
			existing.Row == wall.Row &&
			abs(existing.Column-wall.Column) < 2 {
			return false, wallReasonOverlap
		}
		if wall.Orientation == OrientationVertical &&
			existing.Column == wall.Column &&
			abs(existing.Row-wall.Row) < 2 {
			return false, wallReasonOverlap
		}
	}
	next := cloneState(state)
	next.Walls = append(next.Walls, Wall{
		Row:         wall.Row,
		Column:      wall.Column,
		Orientation: wall.Orientation,
		Side:        side,
	})
	if len(shortestPath(next, SideSouth)) == 0 ||
		len(shortestPath(next, SideNorth)) == 0 {
		return false, wallReasonBlocksPath
	}
	return true, ""
}

func wallOptions(state *State, side Side) []WallOption {
	result := make([]WallOption, 0, 128)
	for row := 0; row < 8; row++ {
		for column := 0; column < 8; column++ {
			for _, orientation := range []Orientation{
				OrientationHorizontal,
				OrientationVertical,
			} {
				wall := WallSpec{
					Row:         row,
					Column:      column,
					Orientation: orientation,
				}
				legal, reason := wallCheck(state, side, wall)
				result = append(result, WallOption{
					Row:         row,
					Column:      column,
					Orientation: orientation,
					Legal:       legal,
					ReasonCode:  reason,
				})
			}
		}
	}
	return result
}

func movePawn(
	state *State,
	playerID string,
	to Cell,
	automatic bool,
) error {
	if state.Status != StatusPlaying {
		return errors.New("not in playing phase")
	}
	if state.CurrentPlayer != playerID {
		return errors.New("not the active player")
	}
	side := sideForPlayer(state, playerID)
	legal := false
	for _, option := range legalMoves(state, side) {
		if sameCell(option, to) {
			legal = true
			break
		}
	}
	if !legal {
		return errors.New("illegal pawn move")
	}
	from := state.Pawns[side]
	state.Pawns[side] = to
	appendAction(state, Action{
		Kind:      "move",
		PlayerID:  playerID,
		Side:      side,
		From:      &from,
		To:        &to,
		Automatic: automatic,
	})
	if to.Row == targetRow(side) {
		finish(state, playerID, side, sideName(side)+"率先抵达对边，赢得对局。")
		return nil
	}
	state.CurrentPlayer = playerForSide(state, other(side))
	return nil
}

func placeWall(
	state *State,
	playerID string,
	wall WallSpec,
	automatic bool,
) error {
	if state.Status != StatusPlaying {
		return errors.New("not in playing phase")
	}
	if state.CurrentPlayer != playerID {
		return errors.New("not the active player")
	}
	side := sideForPlayer(state, playerID)
	legal, reason := wallCheck(state, side, wall)
	if !legal {
		return fmt.Errorf("illegal wall placement: %s", reason)
	}
	state.Walls = append(state.Walls, Wall{
		Row:         wall.Row,
		Column:      wall.Column,
		Orientation: wall.Orientation,
		Side:        side,
	})
	state.WallStock[side]--
	appendAction(state, Action{
		Kind:      "wall",
		PlayerID:  playerID,
		Side:      side,
		Wall:      &wall,
		Automatic: automatic,
	})
	state.CurrentPlayer = playerForSide(state, other(side))
	return nil
}

func resign(state *State, playerID string) error {
	if state.Status != StatusPlaying {
		return errors.New("game is not active")
	}
	player := state.Roster[playerID]
	if player == nil ||
		(player.Side != SideSouth && player.Side != SideNorth) {
		return errors.New("player not found")
	}
	appendAction(state, Action{
		Kind:     "resign",
		PlayerID: playerID,
		Side:     player.Side,
	})
	winnerSide := other(player.Side)
	finish(
		state,
		playerForSide(state, winnerSide),
		winnerSide,
		sideName(player.Side)+"主动认输，对局结束。",
	)
	return nil
}

func finish(state *State, winnerID string, winnerSide Side, reason string) {
	state.Status = StatusEnd
	state.WinnerID = winnerID
	state.WinnerSide = winnerSide
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
	if playerID == state.SouthPlayerID {
		return SideSouth
	}
	if playerID == state.NorthPlayerID {
		return SideNorth
	}
	return ""
}

func playerForSide(state *State, side Side) string {
	if side == SideSouth {
		return state.SouthPlayerID
	}
	if side == SideNorth {
		return state.NorthPlayerID
	}
	return ""
}

func sideName(side Side) string {
	if side == SideSouth {
		return "南方"
	}
	return "北方"
}

func allCandidates(state *State, side Side) []candidate {
	result := make([]candidate, 0, 131)
	for _, move := range legalMoves(state, side) {
		result = append(result, candidate{Kind: "move", To: move})
	}
	if state.WallStock[side] > 0 {
		for _, option := range wallOptions(state, side) {
			if option.Legal {
				result = append(result, candidate{
					Kind: "wall",
					Wall: WallSpec{
						Row:         option.Row,
						Column:      option.Column,
						Orientation: option.Orientation,
					},
				})
			}
		}
	}
	sortCandidates(result)
	return result
}

func applyCandidate(state *State, side Side, action candidate) bool {
	playerID := playerForSide(state, side)
	if action.Kind == "move" {
		return movePawn(state, playerID, action.To, true) == nil
	}
	return placeWall(state, playerID, action.Wall, true) == nil
}

func sortCells(cells []Cell) {
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Row != cells[j].Row {
			return cells[i].Row < cells[j].Row
		}
		return cells[i].Column < cells[j].Column
	})
}

func sortCandidates(values []candidate) {
	sort.Slice(values, func(i, j int) bool {
		left, right := values[i], values[j]
		if left.Kind != right.Kind {
			return left.Kind == "move"
		}
		if left.Kind == "move" {
			if left.To.Row != right.To.Row {
				return left.To.Row < right.To.Row
			}
			return left.To.Column < right.To.Column
		}
		if left.Wall.Row != right.Wall.Row {
			return left.Wall.Row < right.Wall.Row
		}
		if left.Wall.Column != right.Wall.Column {
			return left.Wall.Column < right.Wall.Column
		}
		return left.Wall.Orientation < right.Wall.Orientation
	})
}

func cellID(cell Cell) int {
	return cell.Row*9 + cell.Column
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

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
