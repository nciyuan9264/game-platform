package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunProbeValidatesPublicAndAuthenticatedEndpoints(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html></html>"))
		case "/pam-api/healthz", "/pam-api/readyz":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/pam-api/platform/rankings":
			gameType := r.URL.Query().Get("gameType")
			if gameType != "acquire" ||
				r.URL.Query().Get("sort") == "unknown" ||
				r.URL.Query().Get("limit") == "-1" ||
				r.URL.Query().Get("offset") == "-1" {
				http.Error(w, `{"code":"invalid_request","message":"invalid query"}`, http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{
				"status_code":200,
				"message":"ok",
				"data":{
					"gameType":"acquire",
					"entries":[{"metric":{"rank":{"avgRank":1}}}],
					"page":{"limit":5,"offset":0,"hasMore":false}
				}
			}`))
		case "/pam-api/platform/rooms":
			if r.Header.Get("Authorization") == "" {
				http.Error(w, `{"code":"unauthorized","message":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"status_code":200,"message":"ok","data":{"rooms":[]}}`))
		case "/pam-api/platform/auth/profile":
			requireTestAuthorization(t, r)
			_, _ = w.Write([]byte(`{"status_code":200,"message":"ok","data":{"user_id":"7"}}`))
		case "/pam-api/platform/history/games":
			requireTestAuthorization(t, r)
			_, _ = w.Write([]byte(`{"status_code":200,"message":"ok","data":{"games":[],"page":{}}}`))
		case "/pam-api/platform/history/stats":
			requireTestAuthorization(t, r)
			_, _ = w.Write([]byte(`{"status_code":200,"message":"ok","data":{"totalGames":0}}`))
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	client := newAPIClient(config{
		HTTPBase:    server.URL + "/pam-api",
		FrontendURL: server.URL,
		Origin:      server.URL,
		AccessToken: "test-secret",
	})
	checks := client.runProbe(context.Background(), []string{"acquire"})
	for _, check := range checks {
		if check.Status != checkPassed {
			encoded, _ := json.Marshal(checks)
			t.Fatalf("probe check %q = %s: %s\n%s", check.Name, check.Status, check.Message, encoded)
		}
	}
}

func requireTestAuthorization(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Header.Get("Authorization") != "Bearer test-secret" {
		t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
	}
}
