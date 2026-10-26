package othello

import (
	"errors"
	"sort"
	"time"
)

var directions = [][2]int{
	{-1, -1},
	{-1, 0},
	{-1, 1},
	{0, -1},
	{0, 1},
	{1, -1},
	{1, 0},
	{1, 1},
}

func freshBoard() []Disc {
	board := make([]Disc, 64)
	board[position(3, 3)] = DiscWhite
	board[position(3, 4)] = DiscBlack
	board[position(4, 3)] = DiscBlack
	board[position(4, 4)] = DiscWhite
	return board
}

func position(row, column int) int {
	return row*8 + column
}

func moveAt(board []Disc, index int, side Side) Move {
	move := Move{Index: index, Flips: []int{}, Lines: [][]int{}}
	if len(board) != 64 || index < 0 || index >= 64 || board[index] != DiscEmpty {
		return move
	}
	row, column := index/8, index%8
	self := discFor(side)
	opponent := discFor(other(side))
	for _, direction := range directions {
		nextRow := row + direction[0]
		nextColumn := column + direction[1]
		line := []int{}
		for inside(nextRow, nextColumn) &&
			board[position(nextRow, nextColumn)] == opponent {
			line = append(line, position(nextRow, nextColumn))
			nextRow += direction[0]
			nextColumn += direction[1]
		}
		if len(line) == 0 ||
			!inside(nextRow, nextColumn) ||
			board[position(nextRow, nextColumn)] != self {
			continue
		}
		move.Lines = append(move.Lines, line)
		move.Flips = append(move.Flips, line...)
	}
	return move
}

func legalMoves(board []Disc, side Side) []Move {
	result := []Move{}
	for index, disc := range board {
		if disc != DiscEmpty {
			continue
		}
		move := moveAt(board, index, side)
		if len(move.Flips) > 0 {
			result = append(result, move)
		}
	}
	return result
}

func applyMove(board []Disc, move Move, side Side) ([]Disc, error) {
	if len(board) != 64 ||
		move.Index < 0 ||
		move.Index >= 64 ||
		board[move.Index] != DiscEmpty ||
		len(move.Flips) == 0 {
		return nil, errors.New("illegal move")
	}
	next := append([]Disc(nil), board...)
	disc := discFor(side)
	next[move.Index] = disc
	for _, index := range move.Flips {
		if index < 0 || index >= 64 {
			return nil, errors.New("invalid flip index")
		}
		next[index] = disc
	}
	return next, nil
}

func counts(board []Disc) (black, white, empty int) {
	for _, disc := range board {
		switch disc {
		case DiscBlack:
			black++
		case DiscWhite:
			white++
		default:
			empty++
		}
	}
	return black, white, empty
}

func settle(state *State) {
	if state.Status == StatusEnd {
		return
	}
	side := sideForPlayer(state, state.CurrentPlayer)
	if len(legalMoves(state.Board, side)) > 0 {
		state.Status = StatusPlaying
		return
	}
	opponent := other(side)
	if len(legalMoves(state.Board, opponent)) > 0 {
		state.Status = StatusPass
		return
	}
	finishByCount(state)
}

func finishByCount(state *State) {
	black, white, empty := counts(state.Board)
	state.Status = StatusEnd
	state.CurrentPlayer = ""
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	switch {
	case black > white:
		state.WinnerSide = SideBlack
		state.WinnerID = playerForSide(state, SideBlack)
	case white > black:
		state.WinnerSide = SideWhite
		state.WinnerID = playerForSide(state, SideWhite)
	default:
		state.WinnerSide = ""
		state.WinnerID = ""
	}
	if empty == 0 {
		state.EndReason = "棋盘已满，对局结束。"
	} else {
		state.EndReason = "双方均无合法落点，对局提前结束。"
	}
}

func playMove(state *State, playerID string, index int, automatic bool) error {
	if state.Status != StatusPlaying {
		return errors.New("not in playing phase")
	}
	if state.CurrentPlayer != playerID {
		return errors.New("not the active player")
	}
	side := sideForPlayer(state, playerID)
	move := moveAt(state.Board, index, side)
	if len(move.Flips) == 0 {
		return errors.New("move does not outflank an opponent disc")
	}
	board, err := applyMove(state.Board, move, side)
	if err != nil {
		return err
	}
	state.Board = board
	actionIndex := index
	appendAction(state, Action{
		Kind:      "move",
		PlayerID:  playerID,
		Side:      side,
		Index:     &actionIndex,
		Flips:     append([]int(nil), move.Flips...),
		Automatic: automatic,
	})
	state.CurrentPlayer = playerForSide(state, other(side))
	settle(state)
	return nil
}

func passTurn(state *State, playerID string, automatic bool) error {
	if state.Status != StatusPass {
		return errors.New("pass is not required")
	}
	if state.CurrentPlayer != playerID {
		return errors.New("not the active player")
	}
	side := sideForPlayer(state, playerID)
	if len(legalMoves(state.Board, side)) > 0 {
		return errors.New("a legal move is available")
	}
	appendAction(state, Action{
		Kind:      "pass",
		PlayerID:  playerID,
		Side:      side,
		Flips:     []int{},
		Automatic: automatic,
	})
	state.CurrentPlayer = playerForSide(state, other(side))
	settle(state)
	return nil
}

func resign(state *State, playerID string) error {
	if state.Status == StatusMatch ||
		state.Status == StatusWaiting ||
		state.Status == StatusEnd {
		return errors.New("game is not active")
	}
	player := state.Roster[playerID]
	if player == nil {
		return errors.New("player not found")
	}
	appendAction(state, Action{
		Kind:     "resign",
		PlayerID: playerID,
		Side:     player.Side,
		Flips:    []int{},
	})
	state.Status = StatusEnd
	state.WinnerSide = other(player.Side)
	state.WinnerID = playerForSide(state, state.WinnerSide)
	state.EndReason = sideName(player.Side) + "主动认输，对局结束。"
	state.CurrentPlayer = ""
	return nil
}

func appendAction(state *State, action Action) {
	action.ID = len(state.Log) + 1
	copyAction := cloneAction(action)
	state.Log = append(state.Log, copyAction)
	state.LastAction = &copyAction
}

func sideForPlayer(state *State, playerID string) Side {
	if player := state.Roster[playerID]; player != nil {
		return player.Side
	}
	if playerID == state.BlackPlayerID {
		return SideBlack
	}
	return SideWhite
}

func playerForSide(state *State, side Side) string {
	if side == SideBlack {
		return state.BlackPlayerID
	}
	return state.WhitePlayerID
}

func sideName(side Side) string {
	if side == SideBlack {
		return "黑方"
	}
	return "白方"
}

func inside(row, column int) bool {
	return row >= 0 && row < 8 && column >= 0 && column < 8
}

func sortedMoves(moves []Move) []Move {
	result := append([]Move(nil), moves...)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Index < result[j].Index
	})
	return result
}
