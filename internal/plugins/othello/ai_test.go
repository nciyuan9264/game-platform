package othello

import (
	"context"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestEasyMoveIsDeterministicAndLegal(t *testing.T) {
	state := startPreparedMatch(t, 1)
	first, ok := bestEasyMove(
		state,
		state.CurrentPlayer,
		sideForPlayer(state, state.CurrentPlayer),
	)
	if !ok || len(first.Flips) == 0 {
		t.Fatalf("easy move = %+v, %v", first, ok)
	}
	second, ok := bestEasyMove(
		cloneState(state),
		state.CurrentPlayer,
		sideForPlayer(state, state.CurrentPlayer),
	)
	if !ok || second.Index != first.Index {
		t.Fatalf("easy move changed: %+v then %+v", first, second)
	}
}

func TestHardMoveUsesReadyEdaxMoveAndFallsBackToMedium(t *testing.T) {
	state := startPreparedMatch(t, 1)
	side := sideForPlayer(state, state.CurrentPlayer)
	moves := sortedMoves(legalMoves(state.Board, side))
	edaxMove := moves[len(moves)-1].Index
	state.Analysis = &PositionAnalysis{
		PositionKey: positionKey(state),
		Status:      analysisStatusReady,
		BestMove:    &edaxMove,
	}
	move, ok := bestHardMove(context.Background(), state, side)
	if !ok || move.Index != edaxMove {
		t.Fatalf("hard move = %+v, want %d", move, edaxMove)
	}

	illegal := 0
	state.Analysis.BestMove = &illegal
	hardFallback, ok := bestHardMove(context.Background(), state, side)
	if !ok {
		t.Fatal("hard fallback found no move")
	}
	medium, ok := bestMediumMove(context.Background(), state, side)
	if !ok || hardFallback.Index != medium.Index {
		t.Fatalf("hard fallback = %+v, medium = %+v", hardFallback, medium)
	}
}

func TestHardAIWaitsForExistingAnalysis(t *testing.T) {
	state := startPreparedMatch(t, 2)
	ai := state.Roster[state.CurrentPlayer]
	ai.AIDifficulty = game.AIDifficultyHard
	if state.Analysis == nil || state.Analysis.Status != analysisStatusPending {
		t.Fatalf("opening analysis = %+v", state.Analysis)
	}
	if request := nextAIRequest(state); request != nil {
		t.Fatalf("hard AI scheduled before analysis: %+v", request)
	}

	bestMove := legalMoves(state.Board, currentSide(state))[0].Index
	result, err := (&Plugin{}).ApplyCommand(
		context.Background(),
		state,
		internalAnalysisResultCommand(state, bestMove),
	)
	if err != nil {
		t.Fatal(err)
	}
	expectedDelay := aiThinkDelay(state) - 200*time.Millisecond
	if len(result.AIRequests) != 1 ||
		result.AIRequests[0].Delay != expectedDelay {
		t.Fatalf("hard AI requests = %+v", result.AIRequests)
	}
}

func internalAnalysisResultCommand(
	state *State,
	bestMove int,
) contracts.RoomCommand {
	return contracts.RoomCommand{
		Type: CommandAnalysisResult,
		Payload: analysisResultPayload{
			RequestID:    state.Analysis.RequestID,
			PositionKey:  state.Analysis.PositionKey,
			BestMove:     &bestMove,
			BlackWinRate: 0.45,
			DrawRate:     0.1,
			WhiteWinRate: 0.45,
			Depth:        10,
			ElapsedMs:    200,
		},
	}
}
