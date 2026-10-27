package hex

import (
	"context"
	"testing"
)

func TestBestCellWinsAndBlocks(t *testing.T) {
	win := ruleTestState()
	for row := 0; row < boardSize-1; row++ {
		win.Board[position(Cell{Row: row, Column: 4})] = StoneRed
	}
	cell, ok := bestCell(context.Background(), win, SideRed)
	board := append([]Stone(nil), win.Board...)
	if ok {
		board[position(cell)] = StoneRed
	}
	if !ok || len(connection(board, SideRed)) == 0 {
		t.Fatalf("winning cell = %+v/%v", cell, ok)
	}

	block := ruleTestState()
	for column := 0; column < boardSize-1; column++ {
		block.Board[position(Cell{Row: 6, Column: column})] = StoneTeal
	}
	cell, ok = bestCell(context.Background(), block, SideRed)
	threat := append([]Stone(nil), block.Board...)
	if ok {
		threat[position(cell)] = StoneTeal
	}
	if !ok || len(connection(threat, SideTeal)) == 0 {
		t.Fatalf("blocking cell = %+v/%v", cell, ok)
	}
}

func TestSwapPolicyPrefersCentralOpening(t *testing.T) {
	state := ruleTestState()
	if err := placeStone(
		state,
		"red",
		Cell{Row: 5, Column: 5},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if !shouldSwap(state) {
		t.Fatal("central opening was not swapped")
	}

	edge := ruleTestState()
	if err := placeStone(
		edge,
		"red",
		Cell{Row: 0, Column: 0},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if shouldSwap(edge) {
		t.Fatal("weak corner opening was swapped")
	}
}

func TestBestCellUsesWorstReplyScoreInsteadOfGreedyScore(t *testing.T) {
	for sample := uint64(1); sample <= 32; sample++ {
		board := deterministicMidgame(sample)
		greedy, expected, ok := lookaheadFixture(
			context.Background(),
			board,
			SideRed,
		)
		if !ok || greedy == expected {
			continue
		}
		state := ruleTestState()
		state.Board = board
		got, found := bestCell(context.Background(), state, SideRed)
		if !found || got != expected {
			t.Fatalf(
				"lookahead choice = %+v/%v, want %+v (greedy %+v)",
				got,
				found,
				expected,
				greedy,
			)
		}
		return
	}
	t.Fatal("failed to construct a deterministic non-greedy lookahead fixture")
}

func deterministicMidgame(seed uint64) []Stone {
	board := freshBoard()
	value := seed
	for turn := 0; turn < 30; turn++ {
		value = value*6364136223846793005 + 1442695040888963407
		index := int(value % uint64(len(board)))
		for board[index] != StoneEmpty {
			index = (index + 1) % len(board)
		}
		if turn%2 == 0 {
			board[index] = StoneRed
		} else {
			board[index] = StoneTeal
		}
	}
	return board
}

func lookaheadFixture(
	ctx context.Context,
	board []Stone,
	side Side,
) (Cell, Cell, bool) {
	legal := sortedCells(legalCells(board))
	for _, candidate := range legal {
		self := append([]Stone(nil), board...)
		self[position(candidate)] = stoneFor(side)
		opponent := append([]Stone(nil), board...)
		opponent[position(candidate)] = stoneFor(other(side))
		if len(connection(self, side)) > 0 ||
			len(connection(opponent, other(side))) > 0 {
			return Cell{}, Cell{}, false
		}
	}
	scored := make([]scoredCell, 0, len(legal))
	for _, cell := range legal {
		next := append([]Stone(nil), board...)
		next[position(cell)] = stoneFor(side)
		scored = append(scored, scoredCell{
			cell:  cell,
			score: evaluateBoard(next, side, cell),
		})
	}
	sortScored(scored)
	if len(scored) > aiSearchWidth {
		scored = scored[:aiSearchWidth]
	}
	if len(scored) == 0 {
		return Cell{}, Cell{}, false
	}
	best := scoredCell{score: -(1 << 30)}
	for _, candidate := range scored {
		next := append([]Stone(nil), board...)
		next[position(candidate.cell)] = stoneFor(side)
		worst := candidate.score
		replies := topReplies(ctx, next, other(side))
		if len(replies) > 0 {
			worst = 1 << 30
			for _, reply := range replies {
				after := append([]Stone(nil), next...)
				after[position(reply.cell)] = stoneFor(other(side))
				score := evaluatePosition(after, side)
				if score < worst {
					worst = score
				}
			}
		}
		if worst > best.score ||
			worst == best.score && cellLess(candidate.cell, best.cell) {
			best = scoredCell{cell: candidate.cell, score: worst}
		}
	}
	return scored[0].cell, best.cell, true
}
