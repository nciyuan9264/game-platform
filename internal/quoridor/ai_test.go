package quoridor

import (
	"context"
	"testing"
)

func TestAISelectsImmediateWin(t *testing.T) {
	state := ruleTestState()
	state.Pawns[SideSouth] = Cell{Row: 1, Column: 3}
	state.Pawns[SideNorth] = Cell{Row: 6, Column: 7}
	action, ok := bestAction(context.Background(), state, SideSouth)
	if !ok || action.Kind != "move" ||
		action.To != (Cell{Row: 0, Column: 3}) {
		t.Fatalf("winning action = %+v/%v", action, ok)
	}
}

func TestAIBlocksImmediateOpponentWin(t *testing.T) {
	state := ruleTestState()
	state.Pawns[SideSouth] = Cell{Row: 8, Column: 4}
	state.Pawns[SideNorth] = Cell{Row: 7, Column: 4}
	action, ok := bestAction(context.Background(), state, SideSouth)
	if !ok || action.Kind != "wall" ||
		action.Wall.Orientation != OrientationHorizontal ||
		action.Wall.Row != 7 ||
		(action.Wall.Column != 3 && action.Wall.Column != 4) {
		t.Fatalf("blocking action = %+v/%v", action, ok)
	}
}

func TestAIUsesMoveWhenWallsAreExhausted(t *testing.T) {
	state := ruleTestState()
	state.WallStock[SideSouth] = 0
	state.Walls = make([]Wall, 0, 10)
	for row := 0; row < 5; row++ {
		for _, column := range []int{0, 2} {
			state.Walls = append(state.Walls, Wall{
				Row:         row,
				Column:      column,
				Orientation: OrientationVertical,
				Side:        SideSouth,
			})
		}
	}
	action, ok := bestAction(context.Background(), state, SideSouth)
	if !ok || action.Kind != "move" {
		t.Fatalf("action without walls = %+v/%v", action, ok)
	}
}

func TestAIIsStableAndCancellationUsesShortestPathFallback(t *testing.T) {
	state := ruleTestState()
	first, ok := bestAction(context.Background(), state, SideSouth)
	if !ok {
		t.Fatal("no first action")
	}
	for index := 0; index < 5; index++ {
		next, nextOK := bestAction(
			context.Background(),
			cloneState(state),
			SideSouth,
		)
		if !nextOK || next != first {
			t.Fatalf("unstable action = %+v/%v, want %+v", next, nextOK, first)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fallback, fallbackOK := bestAction(ctx, state, SideSouth)
	if !fallbackOK || fallback.Kind != "move" ||
		fallback.To != (Cell{Row: 7, Column: 4}) {
		t.Fatalf("cancel fallback = %+v/%v", fallback, fallbackOK)
	}
}
