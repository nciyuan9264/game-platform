package othello

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	edaxRemoteURLEnv        = "OTHELLO_EDAX_URL"
	edaxRemoteSecretEnv     = "OTHELLO_EDAX_SHARED_SECRET"
	edaxRemoteResponseLimit = 64 * 1024
)

type remoteEdaxAnalyzer struct {
	endpoint string
	secret   string
	client   *http.Client
	now      func() time.Time
}

func configuredPositionAnalyzer() positionAnalyzer {
	rawURL := strings.TrimSpace(os.Getenv(edaxRemoteURLEnv))
	secret := strings.TrimSpace(os.Getenv(edaxRemoteSecretEnv))
	if rawURL == "" && secret == "" {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("PAM_ENV")), "prod") {
			panic(fmt.Sprintf(
				"%s and %s are required in production",
				edaxRemoteURLEnv,
				edaxRemoteSecretEnv,
			))
		}
		return newEdaxAnalyzer()
	}
	if rawURL == "" || len(secret) < 32 {
		panic(fmt.Sprintf(
			"%s must be set and %s must contain at least 32 characters",
			edaxRemoteURLEnv,
			edaxRemoteSecretEnv,
		))
	}
	endpoint, err := edaxAnalyzeEndpoint(rawURL, true)
	if err != nil {
		panic(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 16
	transport.MaxIdleConnsPerHost = 8
	transport.IdleConnTimeout = 90 * time.Second
	return &remoteEdaxAnalyzer{
		endpoint: endpoint,
		secret:   secret,
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(
				_ *http.Request,
				_ []*http.Request,
			) error {
				return http.ErrUseLastResponse
			},
		},
		now: time.Now,
	}
}

func (a *remoteEdaxAnalyzer) Analyze(
	ctx context.Context,
	state *State,
) (EngineAnalysis, error) {
	if err := validateReplayState(state); err != nil {
		return EngineAnalysis{}, err
	}
	if state.Analysis == nil ||
		state.Analysis.Status != analysisStatusPending ||
		state.Analysis.RequestID == "" ||
		state.Analysis.PositionKey != positionKey(state) {
		return EngineAnalysis{}, errors.New("Edax remote request is stale")
	}
	body, err := json.Marshal(EdaxHTTPAnalyzeRequest{
		RequestID:   state.Analysis.RequestID,
		PositionKey: state.Analysis.PositionKey,
		State:       state,
	})
	if err != nil {
		return EngineAnalysis{}, fmt.Errorf("encode Edax request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		a.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return EngineAnalysis{}, fmt.Errorf("create Edax request: %w", err)
	}
	timestamp, signature := SignEdaxHTTPRequest(a.secret, a.now(), body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(EdaxTimestampHeader, timestamp)
	request.Header.Set(EdaxSignatureHeader, signature)

	response, err := a.client.Do(request)
	if err != nil {
		return EngineAnalysis{}, fmt.Errorf("call Edax service: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(
		io.LimitReader(response.Body, edaxRemoteResponseLimit+1),
	)
	if err != nil {
		return EngineAnalysis{}, fmt.Errorf("read Edax response: %w", err)
	}
	if len(responseBody) > edaxRemoteResponseLimit {
		return EngineAnalysis{}, errors.New("Edax response exceeded limit")
	}
	if response.StatusCode != http.StatusOK {
		return EngineAnalysis{}, fmt.Errorf(
			"Edax service returned HTTP %d",
			response.StatusCode,
		)
	}
	var result EdaxHTTPAnalyzeResponse
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil ||
		decoder.Decode(&struct{}{}) != io.EOF {
		return EngineAnalysis{}, errors.New("invalid Edax response")
	}
	if result.RequestID != state.Analysis.RequestID ||
		result.PositionKey != state.Analysis.PositionKey ||
		result.EngineVersion != analysisEngineVersion ||
		(result.BestMove != nil &&
			(*result.BestMove < 0 || *result.BestMove >= 64)) ||
		result.Score < -64 ||
		result.Score > 64 ||
		result.Depth < 0 ||
		result.Depth > 60 ||
		result.ElapsedMs < 0 {
		return EngineAnalysis{}, errors.New("invalid Edax response")
	}
	if result.BestMove != nil {
		move := moveAt(state.Board, *result.BestMove, currentSide(state))
		if len(move.Flips) == 0 {
			return EngineAnalysis{}, errors.New("Edax returned an illegal move")
		}
	}
	return EngineAnalysis{
		BestMove:  result.BestMove,
		Score:     result.Score,
		Depth:     result.Depth,
		ElapsedMs: result.ElapsedMs,
	}, nil
}

func edaxAnalyzeEndpoint(rawURL string, requireHTTPS bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil ||
		parsed.Host == "" ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(requireHTTPS && parsed.Scheme != "https") ||
		(!requireHTTPS && parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("%s must be a valid HTTPS base URL", edaxRemoteURLEnv)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/analyze"
	return parsed.String(), nil
}
