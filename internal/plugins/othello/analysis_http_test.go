package othello

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestEdaxHTTPHandlerHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	newEdaxHTTPHandler("", fakePositionAnalyzer{}, time.Now).
		ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, body = %s", response.Code, response.Body)
	}
}

func TestEdaxHTTPHandlerAuthenticatesAndAnalyzes(t *testing.T) {
	const secret = "test-secret-with-at-least-32-bytes"
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	state := startPreparedMatch(t, 1)
	bestMove := 19
	body, err := json.Marshal(EdaxHTTPAnalyzeRequest{
		RequestID:   state.Analysis.RequestID,
		PositionKey: state.Analysis.PositionKey,
		State:       state,
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp, signature := SignEdaxHTTPRequest(secret, now, body)
	request := httptest.NewRequest(http.MethodPost, "/analyze", bytes.NewReader(body))
	request.Header.Set(EdaxTimestampHeader, timestamp)
	request.Header.Set(EdaxSignatureHeader, signature)
	response := httptest.NewRecorder()

	newEdaxHTTPHandler(secret, fakePositionAnalyzer{
		result: EngineAnalysis{
			BestMove:  &bestMove,
			Score:     8,
			Depth:     11,
			ElapsedMs: 900,
		},
	}, func() time.Time { return now }).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("analysis status = %d, body = %s", response.Code, response.Body)
	}
	var result EdaxHTTPAnalyzeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.RequestID != state.Analysis.RequestID ||
		result.PositionKey != state.Analysis.PositionKey ||
		result.BestMove == nil ||
		*result.BestMove != bestMove ||
		result.EngineVersion != analysisEngineVersion {
		t.Fatalf("analysis response = %+v", result)
	}
}

func TestEdaxHTTPHandlerRejectsInvalidAuthentication(t *testing.T) {
	const secret = "test-secret-with-at-least-32-bytes"
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	state := startPreparedMatch(t, 1)
	body, err := json.Marshal(EdaxHTTPAnalyzeRequest{
		RequestID:   state.Analysis.RequestID,
		PositionKey: state.Analysis.PositionKey,
		State:       state,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/analyze", bytes.NewReader(body))
	request.Header.Set(EdaxTimestampHeader, strconv.FormatInt(now.Unix(), 10))
	request.Header.Set(EdaxSignatureHeader, "invalid")
	response := httptest.NewRecorder()

	newEdaxHTTPHandler(secret, fakePositionAnalyzer{}, func() time.Time {
		return now
	}).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
}

func TestEdaxHTTPHandlerRejectsStalePosition(t *testing.T) {
	const secret = "test-secret-with-at-least-32-bytes"
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	state := startPreparedMatch(t, 1)
	body, err := json.Marshal(EdaxHTTPAnalyzeRequest{
		RequestID:   state.Analysis.RequestID,
		PositionKey: "stale-position",
		State:       state,
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp, signature := SignEdaxHTTPRequest(secret, now, body)
	request := httptest.NewRequest(http.MethodPost, "/analyze", bytes.NewReader(body))
	request.Header.Set(EdaxTimestampHeader, timestamp)
	request.Header.Set(EdaxSignatureHeader, signature)
	response := httptest.NewRecorder()

	newEdaxHTTPHandler(secret, fakePositionAnalyzer{}, func() time.Time {
		return now
	}).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
}
