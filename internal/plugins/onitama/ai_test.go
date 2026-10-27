package onitama

import (
	"context"
	"testing"
)

func TestAISelectsImmediateStoneWin(t *testing.T) {
	state := ruleTestState()
	state.Hands[SideBlue] = []string{"tiger", "crab"}
	state.Pieces["blue-master"].Cell = Cell{Row: 2, Column: 2}
	state.Pieces["red-master"].Cell = Cell{Row: 0, Column: 2}
	action, ok := bestAction(context.Background(), state, SideBlue)
	if !ok ||
		action.CardID != "tiger" ||
		action.PieceID != "blue-master" ||
		action.To != (Cell{Row: 0, Column: 2}) {
		t.Fatalf("winning action = %+v/%v", action, ok)
	}
}

func TestAIIsStableAndCancellationUsesFallback(t *testing.T) {
	state := ruleTestState()
	first, ok := bestAction(context.Background(), state, SideBlue)
	if !ok {
		t.Fatal("no action")
	}
	for index := 0; index < 3; index++ {
		next, nextOK := bestAction(
			context.Background(),
			cloneState(state),
			SideBlue,
		)
		if !nextOK || next != first {
			t.Fatalf("unstable action = %+v/%v", next, nextOK)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fallback, fallbackOK := bestAction(ctx, state, SideBlue)
	if !fallbackOK {
		t.Fatal("cancelled search returned no fallback")
	}
	found := false
	for _, action := range allCandidates(state, SideBlue) {
		if action == fallback {
			found = true
		}
	}
	if !found {
		t.Fatalf("fallback is not legal: %+v", fallback)
	}
}

func TestAIChoosesPassCardWhenBlocked(t *testing.T) {
	state := ruleTestState()
	for id, piece := range state.Pieces {
		if piece.Side == SideBlue {
			delete(state.Pieces, id)
		}
	}
	action, ok := bestAction(context.Background(), state, SideBlue)
	if !ok || action.Kind != "pass" ||
		!handContains(state.Hands[SideBlue], action.CardID) {
		t.Fatalf("pass action = %+v/%v", action, ok)
	}
}
