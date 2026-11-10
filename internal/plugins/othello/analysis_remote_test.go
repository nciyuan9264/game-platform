package othello

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRemoteEdaxAnalyzerCallsSignedService(t *testing.T) {
	const secret = "test-secret-with-at-least-32-bytes"
	now := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	state := startPreparedMatch(t, 1)
	bestMove := 19
	service := httptest.NewServer(newEdaxHTTPHandler(
		secret,
		fakePositionAnalyzer{result: EngineAnalysis{
			BestMove:  &bestMove,
			Score:     7,
			Depth:     10,
			ElapsedMs: 850,
		}},
		func() time.Time { return now },
	))
	defer service.Close()
	endpoint, err := edaxAnalyzeEndpoint(service.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	analyzer := &remoteEdaxAnalyzer{
		endpoint: endpoint,
		secret:   secret,
		client:   service.Client(),
		now:      func() time.Time { return now },
	}

	result, err := analyzer.Analyze(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if result.BestMove == nil ||
		*result.BestMove != bestMove ||
		result.Score != 7 ||
		result.Depth != 10 ||
		result.ElapsedMs != 850 {
		t.Fatalf("remote analysis = %+v", result)
	}
}

func TestRemoteEdaxAnalyzerRejectsMismatchedResponse(t *testing.T) {
	const secret = "test-secret-with-at-least-32-bytes"
	now := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	state := startPreparedMatch(t, 1)
	service := httptest.NewServer(newEdaxHTTPHandler(
		secret,
		fakePositionAnalyzer{result: EngineAnalysis{
			Score: 65,
			Depth: 10,
		}},
		func() time.Time { return now },
	))
	defer service.Close()
	endpoint, err := edaxAnalyzeEndpoint(service.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	analyzer := &remoteEdaxAnalyzer{
		endpoint: endpoint,
		secret:   secret,
		client:   service.Client(),
		now:      func() time.Time { return now },
	}

	if _, err := analyzer.Analyze(context.Background(), state); err == nil {
		t.Fatal("invalid remote response was accepted")
	}
}

func TestConfiguredPositionAnalyzerRequiresRemoteServiceInProduction(
	t *testing.T,
) {
	t.Setenv("PAM_ENV", "prod")
	t.Setenv(edaxRemoteURLEnv, "")
	t.Setenv(edaxRemoteSecretEnv, "")

	defer func() {
		if recover() == nil {
			t.Fatal("missing production Edax service did not panic")
		}
	}()
	_ = configuredPositionAnalyzer()
}

func TestEdaxAnalyzeEndpointRejectsEndpointPath(t *testing.T) {
	if _, err := edaxAnalyzeEndpoint(
		"https://othello.example.com/healthz",
		true,
	); err == nil {
		t.Fatal("Edax endpoint path was accepted")
	}
}
