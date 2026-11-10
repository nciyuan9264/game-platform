package othello

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

type fakePositionAnalyzer struct {
	result EngineAnalysis
	err    error
}

func (a fakePositionAnalyzer) Analyze(
	context.Context,
	*State,
) (EngineAnalysis, error) {
	return a.result, a.err
}

func TestWinRatesAreNormalizedSymmetricAndMonotonic(t *testing.T) {
	neutralBlack, neutralDraw, neutralWhite := winRates(0, 40, 10)
	if math.Abs(neutralBlack-neutralWhite) > 1e-12 {
		t.Fatalf(
			"neutral rates are not symmetric: %.12f/%.12f",
			neutralBlack,
			neutralWhite,
		)
	}
	if math.Abs(neutralBlack+neutralDraw+neutralWhite-1) > 1e-12 {
		t.Fatalf(
			"neutral rates do not sum to one: %.12f",
			neutralBlack+neutralDraw+neutralWhite,
		)
	}
	leadingBlack, leadingDraw, leadingWhite := winRates(12, 40, 10)
	trailingBlack, trailingDraw, trailingWhite := winRates(-12, 40, 10)
	if leadingBlack <= neutralBlack || trailingBlack >= neutralBlack {
		t.Fatalf(
			"black rate is not monotonic: trailing=%f neutral=%f leading=%f",
			trailingBlack,
			neutralBlack,
			leadingBlack,
		)
	}
	if math.Abs(leadingBlack-trailingWhite) > 1e-12 ||
		math.Abs(leadingWhite-trailingBlack) > 1e-12 ||
		math.Abs(leadingDraw-trailingDraw) > 1e-12 {
		t.Fatalf(
			"mirrored rates differ: lead=%f/%f/%f trail=%f/%f/%f",
			leadingBlack,
			leadingDraw,
			leadingWhite,
			trailingBlack,
			trailingDraw,
			trailingWhite,
		)
	}
}

func TestConfiguredEdaxWorkers(t *testing.T) {
	t.Run("defaults to two", func(t *testing.T) {
		t.Setenv(edaxWorkersEnv, "")
		analyzer := newEdaxAnalyzer()
		if got := analyzer.gate.limit; got != 2 {
			t.Fatalf("worker capacity = %d, want 2", got)
		}
	})

	t.Run("allows one", func(t *testing.T) {
		t.Setenv(edaxWorkersEnv, "1")
		analyzer := newEdaxAnalyzer()
		if got := analyzer.gate.limit; got != 1 {
			t.Fatalf("worker capacity = %d, want 1", got)
		}
	})

	for _, value := range []string{"0", "3", "invalid"} {
		t.Run("rejects "+value, func(t *testing.T) {
			t.Setenv(edaxWorkersEnv, value)
			defer func() {
				if recover() == nil {
					t.Fatalf("worker value %q was accepted", value)
				}
			}()
			_ = newEdaxAnalyzer()
		})
	}
}

func TestAnalysisRequestTimeoutMatchesCurrentPlayer(t *testing.T) {
	humanState := startPreparedMatch(t, 1)
	if isAITurn(humanState) {
		t.Fatal("round one unexpectedly started with an AI turn")
	}
	if got := analysisRequest(humanState).Timeout; got != humanAnalysisTimeout {
		t.Fatalf(
			"human analysis timeout = %s, want %s",
			got,
			humanAnalysisTimeout,
		)
	}
	if got := analysisPriorityFor(humanState); got != analysisPriorityHuman {
		t.Fatalf("human analysis priority = %d", got)
	}

	aiState := startPreparedMatch(t, 2)
	if !isAITurn(aiState) {
		t.Fatal("round two unexpectedly started with a human turn")
	}
	if got := analysisRequest(aiState).Timeout; got != aiAnalysisTimeout {
		t.Fatalf(
			"AI analysis timeout = %s, want %s",
			got,
			aiAnalysisTimeout,
		)
	}
	if got := analysisPriorityFor(aiState); got != analysisPriorityAI {
		t.Fatalf("AI analysis priority = %d", got)
	}
}

func TestPassPositionDoesNotScheduleAnalysis(t *testing.T) {
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
		t.Fatalf("status = %s, want pass", state.Status)
	}
	if key := positionKey(state); key != "" {
		t.Fatalf("pass position key = %q, want empty", key)
	}
	state.Analysis = &PositionAnalysis{
		RequestID:     "stale-pass-analysis",
		PositionKey:   "stale-pass-position",
		Status:        analysisStatusPending,
		EngineVersion: analysisEngineVersion,
	}
	requests, err := (&Plugin{}).PendingBackgroundRequests(
		context.Background(),
		state,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 0 {
		t.Fatalf("pass analysis requests = %+v", requests)
	}
}

func TestEdaxPriorityGatePrioritizesHumansAndPreservesFIFO(t *testing.T) {
	gate := newEdaxPriorityGate(1)
	if err := gate.acquire(
		context.Background(),
		analysisPriorityAI,
	); err != nil {
		t.Fatal(err)
	}

	type acquireResult struct {
		label string
		err   error
	}
	results := make(chan acquireResult, 4)
	releases := map[string]chan struct{}{
		"ai-1":    make(chan struct{}),
		"ai-2":    make(chan struct{}),
		"human-1": make(chan struct{}),
		"human-2": make(chan struct{}),
	}
	start := func(label string, priority analysisPriority) {
		go func() {
			err := gate.acquire(context.Background(), priority)
			results <- acquireResult{label: label, err: err}
			if err == nil {
				<-releases[label]
				gate.release()
			}
		}()
	}

	start("ai-1", analysisPriorityAI)
	waitForEdaxGate(t, gate, 1, 0, 1)
	start("ai-2", analysisPriorityAI)
	waitForEdaxGate(t, gate, 1, 0, 2)
	start("human-1", analysisPriorityHuman)
	waitForEdaxGate(t, gate, 1, 1, 2)
	start("human-2", analysisPriorityHuman)
	waitForEdaxGate(t, gate, 1, 2, 2)

	gate.release()
	for _, want := range []string{"human-1", "human-2", "ai-1", "ai-2"} {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("%s acquire failed: %v", result.label, result.err)
			}
			if result.label != want {
				t.Fatalf("acquired %s, want %s", result.label, want)
			}
			close(releases[result.label])
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}
	waitForEdaxGate(t, gate, 0, 0, 0)
}

func TestEdaxPriorityGateCancellationDoesNotLeakSlot(t *testing.T) {
	gate := newEdaxPriorityGate(1)
	if err := gate.acquire(
		context.Background(),
		analysisPriorityAI,
	); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- gate.acquire(ctx, analysisPriorityHuman)
	}()
	waitForEdaxGate(t, gate, 1, 1, 0)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled acquire error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled acquire did not return")
	}
	waitForEdaxGate(t, gate, 1, 0, 0)

	gate.release()
	retryCtx, retryCancel := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	defer retryCancel()
	if err := gate.acquire(retryCtx, analysisPriorityHuman); err != nil {
		t.Fatalf("acquire after cancellation failed: %v", err)
	}
	gate.release()
}

func TestEdaxPriorityGateLimitsConcurrency(t *testing.T) {
	gate := newEdaxPriorityGate(2)
	for range 2 {
		if err := gate.acquire(
			context.Background(),
			analysisPriorityAI,
		); err != nil {
			t.Fatal(err)
		}
	}

	acquired := make(chan error, 1)
	go func() {
		acquired <- gate.acquire(
			context.Background(),
			analysisPriorityHuman,
		)
	}()
	waitForEdaxGate(t, gate, 2, 1, 0)
	select {
	case err := <-acquired:
		t.Fatalf("third task acquired before release: %v", err)
	default:
	}

	gate.release()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("third task did not acquire released slot")
	}
	gate.release()
	gate.release()
	waitForEdaxGate(t, gate, 0, 0, 0)
}

func waitForEdaxGate(
	t *testing.T,
	gate *edaxPriorityGate,
	active,
	humanQueued,
	aiQueued int,
) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		gate.mu.Lock()
		gotActive := gate.active
		gotHumanQueued := gate.humanQueue.Len()
		gotAIQueued := gate.aiQueue.Len()
		gate.mu.Unlock()
		if gotActive == active &&
			gotHumanQueued == humanQueued &&
			gotAIQueued == aiQueued {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"gate state = active:%d human:%d AI:%d, want %d/%d/%d",
				gotActive,
				gotHumanQueued,
				gotAIQueued,
				active,
				humanQueued,
				aiQueued,
			)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestParseNBoardSearchUsesLastCompleteResponse(t *testing.T) {
	output := strings.Join([]string{
		"status Edax is thinking",
		"search D3 C3 2 0 8",
		"search C4c3D3 -6 0 12",
		"status Edax is waiting",
	}, "\n")
	bestMove, score, depth, err := parseNBoardSearch(output)
	if err != nil {
		t.Fatal(err)
	}
	if bestMove == nil || *bestMove != 26 || score != -6 || depth != 12 {
		t.Fatalf(
			"parsed move/score/depth = %v/%d/%d, want 26/-6/12",
			bestMove,
			score,
			depth,
		)
	}
	if _, _, _, parseErr := parseNBoardSearch(
		"status Edax is waiting",
	); parseErr == nil {
		t.Fatal("missing search response was accepted")
	}
	if _, _, _, parseErr := parseNBoardSearch(
		"search D3 65 0 12",
	); parseErr == nil {
		t.Fatal("out-of-range score was accepted")
	}
	pass, _, _, err := parseNBoardSearch("search PA 0 0 12")
	if err != nil || pass != nil {
		t.Fatalf("pass response = %v, %v", pass, err)
	}
	if _, _, _, err := parseNBoardSearch("search Z9 0 0 12"); err == nil {
		t.Fatal("malformed principal variation was accepted")
	}
}

func TestNBoardInputReplaysMovesAndPasses(t *testing.T) {
	index := 19
	state := &State{Log: []Action{
		{Kind: "move", Index: &index},
		{Kind: "pass"},
	}}
	input, err := nboardInput(state)
	if err != nil {
		t.Fatal(err)
	}
	want := "nboard 1\nmove d3\nmove pa\nhint 1\n"
	if input != want {
		t.Fatalf("NBoard input = %q, want %q", input, want)
	}
}

func TestAnalysisResultPreservesTurnDeadlineAndRejectsStaleResult(t *testing.T) {
	state := startPreparedMatch(t, 1)
	if state.Analysis == nil || state.Analysis.Status != analysisStatusPending {
		t.Fatalf("opening analysis = %+v", state.Analysis)
	}
	deadline := state.TurnDeadline
	requestID := state.Analysis.RequestID
	positionKey := state.Analysis.PositionKey
	bestMove := 19
	plugin := &Plugin{}
	result, err := plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type: CommandAnalysisResult,
			Payload: analysisResultPayload{
				RequestID:    requestID,
				PositionKey:  positionKey,
				BestMove:     &bestMove,
				BlackWinRate: 0.45,
				DrawRate:     0.1,
				WhiteWinRate: 0.45,
				Depth:        10,
				ElapsedMs:    400,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Analysis == nil ||
		state.Analysis.Status != analysisStatusReady ||
		state.Analysis.Depth != 10 ||
		state.Analysis.BestMove == nil ||
		*state.Analysis.BestMove != bestMove {
		t.Fatalf("ready analysis = %+v", state.Analysis)
	}
	if !state.TurnDeadline.Equal(deadline) {
		t.Fatalf(
			"analysis changed deadline: got %s want %s",
			state.TurnDeadline,
			deadline,
		)
	}
	if len(result.AIRequests) != 0 ||
		len(result.BackgroundRequests) != 0 {
		t.Fatalf("analysis result scheduled work: %+v", result)
	}
	if _, err := plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type: CommandAnalysisUnavailable,
			Payload: analysisUnavailablePayload{
				RequestID:   "stale",
				PositionKey: positionKey,
			},
		},
	); err == nil {
		t.Fatal("stale analysis result was accepted")
	}
}

func TestBackgroundRunConvertsWhiteScoreToBlackPerspective(t *testing.T) {
	state := startPreparedMatch(t, 1)
	move := legalMoves(state.Board, SideBlack)[0]
	result, err := (&Plugin{}).ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type:     CommandPlaceDisc,
			PlayerID: state.BlackPlayerID,
			Payload:  map[string]any{"index": move.Index},
			SentAt:   time.Now().UTC(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if currentSide(state) != SideWhite ||
		len(result.BackgroundRequests) != 1 {
		t.Fatalf(
			"post-move side/requests = %q/%d",
			currentSide(state),
			len(result.BackgroundRequests),
		)
	}
	plugin := &Plugin{analyzer: fakePositionAnalyzer{
		result: EngineAnalysis{
			BestMove:  &move.Index,
			Score:     8,
			Depth:     12,
			ElapsedMs: 250,
		},
	}}
	command, err := plugin.Run(
		context.Background(),
		result.BackgroundRequests[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := command.Payload.(analysisResultPayload)
	if payload.Score != -8 {
		t.Fatalf("black-perspective score = %d, want -8", payload.Score)
	}
	if payload.BestMove == nil || *payload.BestMove != move.Index {
		t.Fatalf("best move = %v, want %d", payload.BestMove, move.Index)
	}
	if math.Abs(
		payload.BlackWinRate+payload.DrawRate+payload.WhiteWinRate-1,
	) > 1e-12 {
		t.Fatalf("rates do not sum to one: %+v", payload)
	}
}

func TestTerminalAnalysisUsesAuthoritativeResult(t *testing.T) {
	state := startPreparedMatch(t, 1)
	if err := resign(state, state.BlackPlayerID); err != nil {
		t.Fatal(err)
	}
	if state.Analysis == nil ||
		!state.Analysis.Exact ||
		state.Analysis.WhiteWinRate != 1 ||
		state.Analysis.BlackWinRate != 0 ||
		state.Analysis.DrawRate != 0 ||
		state.Analysis.EngineVersion != finalEngineVersion {
		t.Fatalf("terminal analysis = %+v", state.Analysis)
	}
}

func TestEdaxAnalyzerIntegration(t *testing.T) {
	binaryPath := os.Getenv("EDAX_TEST_BINARY")
	evalPath := os.Getenv("EDAX_TEST_EVAL")
	if binaryPath == "" || evalPath == "" {
		t.Skip("set EDAX_TEST_BINARY and EDAX_TEST_EVAL to run")
	}
	analyzer := &edaxAnalyzer{
		binaryPath: binaryPath,
		evalPath:   evalPath,
		gate:       newEdaxPriorityGate(1),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := analyzer.Analyze(ctx, startPreparedMatch(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if result.Score < -64 ||
		result.Score > 64 ||
		result.Depth <= 0 ||
		result.Depth > 60 ||
		result.ElapsedMs <= 0 {
		t.Fatalf("Edax result = %+v", result)
	}
}
