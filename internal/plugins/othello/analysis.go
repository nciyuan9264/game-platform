package othello

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const (
	analysisKind          = "othello-position-analysis"
	analysisStatusPending = "pending"
	analysisStatusReady   = "ready"
	analysisStatusFailed  = "unavailable"
	analysisEngineVersion = "edax-4.7"
	finalEngineVersion    = "platform-result"

	CommandAnalysisResult      = "analysis_result"
	CommandAnalysisUnavailable = "analysis_unavailable"

	humanAnalysisTimeout = 8 * time.Second
	aiAnalysisTimeout    = 15 * time.Second
	edaxOutputLimit      = 64 * 1024
	edaxBinaryPath       = "/opt/edax/bin/edax"
	edaxEvalFilePath     = "/opt/edax/data/eval.dat"
	edaxBookFilePath     = "/tmp/edax-analysis-book.dat"
	edaxWorkersEnv       = "OTHELLO_EDAX_WORKERS"
	defaultEdaxWorkers   = 2
	maxEdaxWorkers       = 2
)

type positionAnalyzer interface {
	Analyze(context.Context, *State) (EngineAnalysis, error)
}

type analysisPriority uint8

const (
	analysisPriorityAI analysisPriority = iota
	analysisPriorityHuman
)

type EngineAnalysis struct {
	BestMove  *int
	Score     int
	Depth     int
	ElapsedMs int64
}

type edaxAnalyzer struct {
	binaryPath string
	evalPath   string
	gate       *edaxPriorityGate
}

type edaxPriorityGate struct {
	mu         sync.Mutex
	limit      int
	active     int
	humanQueue list.List
	aiQueue    list.List
}

type edaxWaiter struct {
	ready   chan struct{}
	element *list.Element
	granted bool
}

type analysisResultPayload struct {
	RequestID    string  `json:"requestID"`
	PositionKey  string  `json:"positionKey"`
	BestMove     *int    `json:"bestMove,omitempty"`
	BlackWinRate float64 `json:"blackWinRate"`
	DrawRate     float64 `json:"drawRate"`
	WhiteWinRate float64 `json:"whiteWinRate"`
	Score        int     `json:"score"`
	Depth        int     `json:"depth"`
	ElapsedMs    int64   `json:"elapsedMs"`
}

type analysisUnavailablePayload struct {
	RequestID   string `json:"requestID"`
	PositionKey string `json:"positionKey"`
}

func newEdaxAnalyzer() *edaxAnalyzer {
	return &edaxAnalyzer{
		binaryPath: edaxBinaryPath,
		evalPath:   edaxEvalFilePath,
		gate:       newEdaxPriorityGate(configuredEdaxWorkers()),
	}
}

func newEdaxPriorityGate(limit int) *edaxPriorityGate {
	if limit < 1 {
		panic("Edax gate limit must be positive")
	}
	return &edaxPriorityGate{limit: limit}
}

func (g *edaxPriorityGate) acquire(
	ctx context.Context,
	priority analysisPriority,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	waiter := &edaxWaiter{
		ready: make(chan struct{}),
	}
	g.mu.Lock()
	if priority == analysisPriorityHuman {
		waiter.element = g.humanQueue.PushBack(waiter)
	} else {
		waiter.element = g.aiQueue.PushBack(waiter)
	}
	g.dispatchLocked()
	g.mu.Unlock()

	select {
	case <-waiter.ready:
		if err := ctx.Err(); err != nil {
			g.release()
			return err
		}
		return nil
	case <-ctx.Done():
		g.mu.Lock()
		if waiter.granted {
			g.active--
			g.dispatchLocked()
		} else if waiter.element != nil {
			g.queue(priority).Remove(waiter.element)
			waiter.element = nil
		}
		g.mu.Unlock()
		return ctx.Err()
	}
}

func (g *edaxPriorityGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active <= 0 {
		panic("released an inactive Edax gate")
	}
	g.active--
	g.dispatchLocked()
}

func (g *edaxPriorityGate) dispatchLocked() {
	for g.active < g.limit {
		queue := &g.humanQueue
		if queue.Len() == 0 {
			queue = &g.aiQueue
		}
		element := queue.Front()
		if element == nil {
			return
		}
		waiter := element.Value.(*edaxWaiter)
		queue.Remove(element)
		waiter.element = nil
		waiter.granted = true
		g.active++
		close(waiter.ready)
	}
}

func (g *edaxPriorityGate) queue(priority analysisPriority) *list.List {
	if priority == analysisPriorityHuman {
		return &g.humanQueue
	}
	return &g.aiQueue
}

func configuredEdaxWorkers() int {
	value := strings.TrimSpace(os.Getenv(edaxWorkersEnv))
	if value == "" {
		return defaultEdaxWorkers
	}
	workers, err := strconv.Atoi(value)
	if err != nil || workers < 1 || workers > maxEdaxWorkers {
		panic(fmt.Sprintf(
			"%s must be an integer between 1 and %d",
			edaxWorkersEnv,
			maxEdaxWorkers,
		))
	}
	return workers
}

func (p *Plugin) Run(
	ctx context.Context,
	request game.BackgroundRequest,
) (contracts.RoomCommand, error) {
	if request.Kind != analysisKind {
		return contracts.RoomCommand{}, fmt.Errorf(
			"unsupported background request %q",
			request.Kind,
		)
	}
	state, err := asState(request.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	if state.Analysis == nil ||
		state.Analysis.Status != analysisStatusPending ||
		state.Analysis.RequestID != request.RequestID ||
		state.Analysis.PositionKey != positionKey(state) {
		return contracts.RoomCommand{}, errors.New("stale analysis request")
	}
	if p.analyzer == nil {
		return contracts.RoomCommand{}, errors.New("Edax analyzer is unavailable")
	}
	startedAt := time.Now()
	engineResult, err := p.analyzer.Analyze(ctx, state)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	elapsedMs := engineResult.ElapsedMs
	if elapsedMs <= 0 {
		elapsedMs = time.Since(startedAt).Milliseconds()
	}
	score := engineResult.Score
	if currentSide(state) == SideWhite {
		score = -score
	}
	_, _, empty := counts(state.Board)
	black, draw, white := winRates(score, empty, engineResult.Depth)
	return contracts.RoomCommand{
		Type: CommandAnalysisResult,
		Payload: analysisResultPayload{
			RequestID:    request.RequestID,
			PositionKey:  state.Analysis.PositionKey,
			BestMove:     engineResult.BestMove,
			BlackWinRate: black,
			DrawRate:     draw,
			WhiteWinRate: white,
			Score:        score,
			Depth:        engineResult.Depth,
			ElapsedMs:    elapsedMs,
		},
	}, nil
}

func (p *Plugin) PendingBackgroundRequests(
	_ context.Context,
	state game.State,
) ([]game.BackgroundRequest, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	if typed.Analysis == nil ||
		typed.Analysis.Status != analysisStatusPending ||
		typed.Analysis.PositionKey != positionKey(typed) {
		return nil, nil
	}
	return []game.BackgroundRequest{analysisRequest(typed)}, nil
}

func prepareAnalysis(state *State) game.BackgroundRequest {
	key := positionKey(state)
	requestID := "othello-analysis:" + key
	state.Analysis = &PositionAnalysis{
		RequestID:     requestID,
		PositionKey:   key,
		Status:        analysisStatusPending,
		EngineVersion: analysisEngineVersion,
	}
	return analysisRequest(state)
}

func analysisRequest(state *State) game.BackgroundRequest {
	fallback := contracts.RoomCommand{
		Type: CommandAnalysisUnavailable,
		Payload: analysisUnavailablePayload{
			RequestID:   state.Analysis.RequestID,
			PositionKey: state.Analysis.PositionKey,
		},
	}
	return game.BackgroundRequest{
		Kind:            analysisKind,
		RequestID:       state.Analysis.RequestID,
		RoomID:          state.RoomID,
		GameType:        GameType,
		State:           cloneState(state),
		Timeout:         analysisTimeoutFor(state),
		FallbackCommand: &fallback,
	}
}

func analysisTimeoutFor(state *State) time.Duration {
	if isAITurn(state) {
		return aiAnalysisTimeout
	}
	return humanAnalysisTimeout
}

func analysisPriorityFor(state *State) analysisPriority {
	if isAITurn(state) {
		return analysisPriorityAI
	}
	return analysisPriorityHuman
}

func isAITurn(state *State) bool {
	if state == nil {
		return false
	}
	player := state.Roster[state.CurrentPlayer]
	return player != nil && player.AI
}

func positionKey(state *State) string {
	if state == nil ||
		len(state.Board) != 64 ||
		state.Status != StatusPlaying {
		return ""
	}
	canonical := struct {
		RoomID      string `json:"roomID"`
		RoundNo     int    `json:"roundNo"`
		Board       []Disc `json:"board"`
		CurrentSide Side   `json:"currentSide"`
		Status      string `json:"status"`
		ActionCount int    `json:"actionCount"`
	}{
		RoomID:      state.RoomID,
		RoundNo:     state.RoundNo,
		Board:       state.Board,
		CurrentSide: currentSide(state),
		Status:      state.Status,
		ActionCount: len(state.Log),
	}
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func setFinalAnalysis(state *State) {
	if state == nil || state.Status != StatusEnd {
		return
	}
	blackRate, drawRate, whiteRate := 0.0, 0.0, 0.0
	switch state.WinnerSide {
	case SideBlack:
		blackRate = 1
	case SideWhite:
		whiteRate = 1
	default:
		drawRate = 1
	}
	black, white, _ := counts(state.Board)
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"%s:%d:%d:%s:%d",
		state.RoomID,
		state.RoundNo,
		len(state.Log),
		state.WinnerSide,
		black-white,
	)))
	key := hex.EncodeToString(sum[:])
	state.Analysis = &PositionAnalysis{
		RequestID:     "othello-final:" + key,
		PositionKey:   key,
		Status:        analysisStatusReady,
		BlackWinRate:  blackRate,
		DrawRate:      drawRate,
		WhiteWinRate:  whiteRate,
		Score:         black - white,
		Exact:         true,
		EngineVersion: finalEngineVersion,
	}
}

func applyAnalysisResult(
	state *State,
	payload analysisResultPayload,
) error {
	if err := validatePendingAnalysis(
		state,
		payload.RequestID,
		payload.PositionKey,
	); err != nil {
		return err
	}
	rates := []float64{
		payload.BlackWinRate,
		payload.DrawRate,
		payload.WhiteWinRate,
	}
	total := 0.0
	for _, rate := range rates {
		if math.IsNaN(rate) ||
			math.IsInf(rate, 0) ||
			rate < 0 ||
			rate > 1 {
			return errors.New("invalid analysis probability")
		}
		total += rate
	}
	if math.Abs(total-1) > 1e-6 ||
		(payload.BestMove != nil &&
			(*payload.BestMove < 0 || *payload.BestMove >= 64)) ||
		payload.Score < -64 ||
		payload.Score > 64 ||
		payload.Depth < 0 ||
		payload.Depth > 60 ||
		payload.ElapsedMs < 0 {
		return errors.New("invalid analysis result")
	}
	state.Analysis = &PositionAnalysis{
		RequestID:     payload.RequestID,
		PositionKey:   payload.PositionKey,
		Status:        analysisStatusReady,
		BestMove:      payload.BestMove,
		BlackWinRate:  payload.BlackWinRate,
		DrawRate:      payload.DrawRate,
		WhiteWinRate:  payload.WhiteWinRate,
		Score:         payload.Score,
		Depth:         payload.Depth,
		ElapsedMs:     payload.ElapsedMs,
		EngineVersion: analysisEngineVersion,
	}
	resolvePendingHints(state)
	return nil
}

func applyAnalysisUnavailable(
	state *State,
	payload analysisUnavailablePayload,
) error {
	if err := validatePendingAnalysis(
		state,
		payload.RequestID,
		payload.PositionKey,
	); err != nil {
		return err
	}
	state.Analysis = &PositionAnalysis{
		RequestID:     payload.RequestID,
		PositionKey:   payload.PositionKey,
		Status:        analysisStatusFailed,
		EngineVersion: analysisEngineVersion,
	}
	clearPendingHints(state, payload.PositionKey)
	return nil
}

func validatePendingAnalysis(
	state *State,
	requestID,
	key string,
) error {
	if state.Analysis == nil ||
		state.Analysis.Status != analysisStatusPending ||
		requestID == "" ||
		requestID != state.Analysis.RequestID ||
		key == "" ||
		key != state.Analysis.PositionKey ||
		key != positionKey(state) {
		return errors.New("stale analysis result")
	}
	return nil
}

func analysisView(analysis *PositionAnalysis) map[string]any {
	if analysis == nil {
		return nil
	}
	result := map[string]any{
		"requestID":     analysis.RequestID,
		"status":        analysis.Status,
		"exact":         analysis.Exact,
		"engineVersion": analysis.EngineVersion,
	}
	if analysis.Status == analysisStatusReady {
		result["blackWinRate"] = analysis.BlackWinRate
		result["drawRate"] = analysis.DrawRate
		result["whiteWinRate"] = analysis.WhiteWinRate
		result["score"] = analysis.Score
		result["depth"] = analysis.Depth
		result["elapsedMs"] = analysis.ElapsedMs
	}
	return result
}

func winRates(score, empty, depth int) (black, draw, white float64) {
	x := -0.10026799*float64(empty) + 0.31027733*float64(depth)
	sigma := 0.07585621*x*x + 1.16492647*x + 5.4171698
	if sigma < 0.01 {
		sigma = 0.01
	}
	cdf := func(value float64) float64 {
		return 0.5 * (1 + math.Erf(value/math.Sqrt2))
	}
	white = cdf((-1 - float64(score)) / sigma)
	upper := cdf((1 - float64(score)) / sigma)
	draw = upper - white
	black = 1 - upper
	black = clampProbability(black)
	draw = clampProbability(draw)
	white = clampProbability(white)
	total := black + draw + white
	if total <= 0 {
		return 0.5, 0, 0.5
	}
	return black / total, draw / total, white / total
}

func clampProbability(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func (a *edaxAnalyzer) Analyze(
	ctx context.Context,
	state *State,
) (EngineAnalysis, error) {
	if err := validateReplayState(state); err != nil {
		return EngineAnalysis{}, err
	}
	if err := a.gate.acquire(ctx, analysisPriorityFor(state)); err != nil {
		return EngineAnalysis{}, err
	}
	defer a.gate.release()

	input, err := nboardInput(state)
	if err != nil {
		return EngineAnalysis{}, err
	}
	var stdout, stderr limitedBuffer
	stdout.limit = edaxOutputLimit
	stderr.limit = edaxOutputLimit
	command := exec.CommandContext(
		ctx,
		a.binaryPath,
		"-nboard",
		"-q",
		"-eval-file", a.evalPath,
		"-book-file", edaxBookFilePath,
		"-book-usage", "off",
		"-ponder", "off",
		"-move-time", "1",
		"-hash-table-size", "18",
		"-n-tasks", "1",
	)
	command.Stdin = strings.NewReader(input)
	command.Stdout = &stdout
	command.Stderr = &stderr
	startedAt := time.Now()
	err = command.Run()
	elapsedMs := time.Since(startedAt).Milliseconds()
	if ctx.Err() != nil {
		return EngineAnalysis{}, ctx.Err()
	}
	if stdout.overflow || stderr.overflow {
		return EngineAnalysis{}, errors.New("Edax output exceeded limit")
	}
	if err != nil {
		return EngineAnalysis{}, fmt.Errorf(
			"run Edax: %w: %s",
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	if strings.Contains(strings.ToLower(stderr.String()), "error:") {
		return EngineAnalysis{}, fmt.Errorf(
			"Edax protocol error: %s",
			strings.TrimSpace(stderr.String()),
		)
	}
	bestMove, score, depth, err := parseNBoardSearch(stdout.String())
	if err != nil {
		return EngineAnalysis{}, err
	}
	return EngineAnalysis{
		BestMove:  bestMove,
		Score:     score,
		Depth:     depth,
		ElapsedMs: elapsedMs,
	}, nil
}

func nboardInput(state *State) (string, error) {
	var input strings.Builder
	input.WriteString("nboard 1\n")
	for _, action := range state.Log {
		switch action.Kind {
		case "move":
			if action.Index == nil || *action.Index < 0 || *action.Index >= 64 {
				return "", errors.New("invalid move in Othello log")
			}
			input.WriteString("move ")
			input.WriteByte(byte('a' + *action.Index%8))
			input.WriteByte(byte('1' + *action.Index/8))
			input.WriteByte('\n')
		case "pass":
			input.WriteString("move pa\n")
		default:
			return "", fmt.Errorf(
				"unsupported Othello action %q",
				action.Kind,
			)
		}
	}
	input.WriteString("hint 1\n")
	return input.String(), nil
}

func parseNBoardSearch(
	output string,
) (bestMove *int, score, depth int, err error) {
	var searchLine string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "search ") {
			searchLine = line
		}
	}
	fields := strings.Fields(searchLine)
	if len(fields) < 5 {
		return nil, 0, 0, errors.New("Edax search response is missing")
	}
	bestMove, err = parseNBoardMove(fields[1])
	if err != nil {
		return nil, 0, 0, err
	}
	depth, err = strconv.Atoi(fields[len(fields)-1])
	if err != nil || depth < 0 || depth > 60 {
		return nil, 0, 0, errors.New("invalid Edax search depth")
	}
	if fields[len(fields)-2] != "0" {
		return nil, 0, 0, errors.New("invalid Edax search response")
	}
	score, err = strconv.Atoi(fields[len(fields)-3])
	if err != nil || score < -64 || score > 64 {
		return nil, 0, 0, errors.New("invalid Edax search score")
	}
	return bestMove, score, depth, nil
}

func parseNBoardMove(value string) (*int, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "pa" || value == "pass" {
		return nil, nil
	}
	if len(value) < 2 ||
		value[0] < 'a' ||
		value[0] > 'h' ||
		value[1] < '1' ||
		value[1] > '8' {
		return nil, errors.New("invalid Edax principal variation")
	}
	index := int(value[1]-'1')*8 + int(value[0]-'a')
	return &index, nil
}

func validateReplayState(state *State) error {
	if state == nil ||
		len(state.Board) != 64 ||
		(state.Status != StatusPlaying && state.Status != StatusPass) {
		return errors.New("Othello state cannot be analyzed")
	}
	board := freshBoard()
	side := SideBlack
	for _, action := range state.Log {
		if action.Side != side {
			return errors.New("Othello log side mismatch")
		}
		switch action.Kind {
		case "move":
			if action.Index == nil {
				return errors.New("Othello move index is missing")
			}
			move := moveAt(board, *action.Index, side)
			if len(move.Flips) == 0 {
				return errors.New("Othello log contains an illegal move")
			}
			next, err := applyMove(board, move, side)
			if err != nil {
				return err
			}
			board = next
		case "pass":
			if len(legalMoves(board, side)) != 0 ||
				len(legalMoves(board, other(side))) == 0 {
				return errors.New("Othello log contains an illegal pass")
			}
		default:
			return fmt.Errorf(
				"unsupported Othello action %q",
				action.Kind,
			)
		}
		side = other(side)
	}
	if !slices.Equal(board, state.Board) {
		return errors.New("Othello replay board does not match state")
	}
	if side != currentSide(state) {
		return errors.New("Othello replay side does not match state")
	}
	return nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.overflow = true
		return written, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		b.overflow = true
	}
	_, _ = b.Buffer.Write(value)
	return written, nil
}
