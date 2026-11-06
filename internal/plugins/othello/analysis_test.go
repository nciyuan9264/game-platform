package othello

import (
	"context"
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
		if got := cap(analyzer.slot); got != 2 {
			t.Fatalf("worker capacity = %d, want 2", got)
		}
	})

	t.Run("allows one", func(t *testing.T) {
		t.Setenv(edaxWorkersEnv, "1")
		analyzer := newEdaxAnalyzer()
		if got := cap(analyzer.slot); got != 1 {
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

func TestParseNBoardSearchUsesLastCompleteResponse(t *testing.T) {
	output := strings.Join([]string{
		"status Edax is thinking",
		"search D3 C3 2 0 8",
		"search C4 C3 D3 -6 0 12",
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
	if _, _, _, err := parseNBoardSearch("status Edax is waiting"); err == nil {
		t.Fatal("missing search response was accepted")
	}
	if _, _, _, err := parseNBoardSearch("search D3 65 0 12"); err == nil {
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
		slot:       make(chan struct{}, 1),
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
