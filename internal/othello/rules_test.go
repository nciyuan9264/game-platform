package othello

import (
	"reflect"
	"testing"
)

func TestStandardOpeningAndFirstMoves(t *testing.T) {
	board := freshBoard()
	black, white, empty := counts(board)
	if black != 2 || white != 2 || empty != 60 {
		t.Fatalf("opening counts = %d/%d/%d", black, white, empty)
	}
	moves := legalMoves(board, SideBlack)
	indices := make([]int, len(moves))
	for index, move := range moves {
		indices[index] = move.Index
	}
	if !reflect.DeepEqual(indices, []int{19, 26, 37, 44}) {
		t.Fatalf("first moves = %v", indices)
	}
	move := moveAt(board, 26, SideBlack)
	if !reflect.DeepEqual(move.Flips, []int{27}) {
		t.Fatalf("C4 flips = %v", move.Flips)
	}
}

func TestMoveRecognizesAllDirectionsAndDoesNotChain(t *testing.T) {
	board := make([]Disc, 64)
	for _, direction := range directions {
		near := position(3+direction[0], 3+direction[1])
		far := position(3+direction[0]*2, 3+direction[1]*2)
		board[near] = DiscWhite
		board[far] = DiscBlack
	}
	move := moveAt(board, position(3, 3), SideBlack)
	if len(move.Lines) != 8 || len(move.Flips) != 8 {
		t.Fatalf("eight direction move = %+v", move)
	}

	chain := make([]Disc, 64)
	chain[1] = DiscWhite
	chain[2] = DiscBlack
	chain[9] = DiscWhite
	chain[17] = DiscBlack
	next, err := applyMove(chain, moveAt(chain, 0, SideBlack), SideBlack)
	if err != nil {
		t.Fatal(err)
	}
	if next[1] != DiscBlack || next[9] != DiscWhite {
		t.Fatalf("chain reaction occurred: %v/%v", next[1], next[9])
	}
}

func TestIllegalGapsOccupiedSquaresAndMissingClosure(t *testing.T) {
	board := make([]Disc, 64)
	board[2] = DiscWhite
	board[3] = DiscBlack
	if move := moveAt(board, 0, SideBlack); len(move.Flips) != 0 {
		t.Fatalf("gap move = %+v", move)
	}
	board[1] = DiscBlack
	if move := moveAt(board, 0, SideBlack); len(move.Flips) != 0 {
		t.Fatalf("own disc skip = %+v", move)
	}
	board = make([]Disc, 64)
	for index := 1; index < 8; index++ {
		board[index] = DiscWhite
	}
	if move := moveAt(board, 0, SideBlack); len(move.Flips) != 0 {
		t.Fatalf("unclosed edge = %+v", move)
	}
	board[0] = DiscBlack
	if move := moveAt(board, 0, SideWhite); len(move.Flips) != 0 {
		t.Fatalf("occupied move = %+v", move)
	}
}

func TestPassAndEarlyFinish(t *testing.T) {
	board := make([]Disc, 64)
	for index := range board {
		board[index] = DiscWhite
	}
	board[0] = DiscEmpty
	board[1] = DiscBlack
	board[63] = DiscEmpty
	state := &State{
		Status:        StatusPlaying,
		Board:         board,
		CurrentPlayer: "black",
		BlackPlayerID: "black",
		WhitePlayerID: "white",
		Roster: map[string]*Player{
			"black": {PlayerID: "black", Side: SideBlack},
			"white": {PlayerID: "white", Side: SideWhite},
		},
		Log: []Action{},
	}
	settle(state)
	if state.Status != StatusPass {
		t.Fatalf("status = %s", state.Status)
	}
	if err := passTurn(state, "black", false); err != nil {
		t.Fatal(err)
	}
	if state.CurrentPlayer != "white" || state.Status != StatusPlaying {
		t.Fatalf("after pass = %s/%s", state.CurrentPlayer, state.Status)
	}
	if err := playMove(state, "white", 0, false); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusEnd ||
		state.WinnerID != "white" ||
		state.EndReason != "双方均无合法落点，对局提前结束。" {
		t.Fatalf("result = %+v", state)
	}
	_, _, empty := counts(state.Board)
	if empty != 1 {
		t.Fatalf("empty = %d", empty)
	}
}
