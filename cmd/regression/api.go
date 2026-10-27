package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 4 << 20

type apiClient struct {
	baseURL     string
	frontendURL string
	origin      string
	token       string
	client      *http.Client
}

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("HTTP %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
}

type roomInfo struct {
	RoomID             string `json:"roomID"`
	GameType           string `json:"gameType"`
	Status             string `json:"status"`
	Visibility         string `json:"visibility"`
	OwnerUserID        string `json:"ownerUserID"`
	OwnerPlayerID      string `json:"ownerPlayerID"`
	MaxPlayers         int    `json:"maxPlayers"`
	PlayerCount        int    `json:"playerCount"`
	CurrentMatchID     string `json:"currentMatchID"`
	CurrentMatchStatus string `json:"currentMatchStatus"`
}

type roomEnvelope struct {
	StatusCode int      `json:"status_code"`
	Message    string   `json:"message"`
	Data       roomInfo `json:"data"`
}

func newAPIClient(cfg config) *apiClient {
	return &apiClient{
		baseURL:     cfg.HTTPBase,
		frontendURL: cfg.FrontendURL,
		origin:      cfg.Origin,
		token:       cfg.AccessToken,
		client:      &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *apiClient) runProbe(ctx context.Context, games []string) []checkResult {
	checks := []checkResult{
		runCheck("frontend", func() error {
			status, _, err := c.request(ctx, false, http.MethodGet, c.frontendURL, nil, nil)
			return expectStatus(status, err, http.StatusOK)
		}),
		runCheck("gateway health", func() error {
			return c.probeStatus(ctx, "/healthz", http.StatusOK)
		}),
		runCheck("gateway readiness", func() error {
			return c.probeStatus(ctx, "/readyz", http.StatusOK)
		}),
		runCheck("protected rooms reject anonymous access", func() error {
			status, _, err := c.request(ctx, false, http.MethodGet, c.baseURL+"/platform/rooms", url.Values{
				"gameType": {"acquire"},
			}, nil)
			return expectStatus(status, err, http.StatusUnauthorized)
		}),
	}
	for _, gameType := range games {
		gameType := gameType
		checks = append(checks, runCheck("public leaderboard "+gameType, func() error {
			return c.probeLeaderboard(ctx, gameType)
		}))
	}
	invalid := []struct {
		name  string
		query url.Values
	}{
		{
			name:  "wrong query name",
			query: url.Values{"game_type": {"acquire"}, "limit": {"10"}, "offset": {"0"}},
		},
		{
			name:  "invalid game type",
			query: url.Values{"gameType": {"invalid"}, "limit": {"10"}, "offset": {"0"}},
		},
		{
			name:  "invalid pagination",
			query: url.Values{"gameType": {"acquire"}, "limit": {"-1"}, "offset": {"-1"}},
		},
		{
			name:  "invalid sort",
			query: url.Values{"gameType": {"acquire"}, "sort": {"unknown"}, "limit": {"10"}, "offset": {"0"}},
		},
	}
	for _, test := range invalid {
		test := test
		checks = append(checks, runCheck("leaderboard rejects "+test.name, func() error {
			status, _, err := c.request(ctx, false, http.MethodGet, c.baseURL+"/platform/rankings", test.query, nil)
			if status < 400 || status >= 500 {
				if err != nil {
					return err
				}
				return fmt.Errorf("status = %d, want 4xx", status)
			}
			return nil
		}))
	}
	if c.token == "" {
		checks = append(checks, skippedCheck(
			"authenticated API probes",
			"REGRESSION_ACCESS_TOKEN is not set",
		))
		return checks
	}
	checks = append(checks, runCheck("authenticated profile", func() error {
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		if err := c.getJSON(ctx, "/platform/auth/profile", nil, &envelope); err != nil {
			return err
		}
		if stringValue(envelope.Data, "user_id", "userID") == "" {
			return errors.New("profile user ID is missing")
		}
		return nil
	}))
	for _, gameType := range games {
		gameType := gameType
		checks = append(checks,
			runCheck("authenticated rooms "+gameType, func() error {
				return c.probeEnvelope(ctx, "/platform/rooms", url.Values{"gameType": {gameType}}, "rooms")
			}),
			runCheck("authenticated history "+gameType, func() error {
				return c.probeEnvelope(ctx, "/platform/history/games", url.Values{
					"gameType": {gameType},
					"limit":    {"1"},
					"offset":   {"0"},
				}, "games")
			}),
			runCheck("authenticated stats "+gameType, func() error {
				return c.probeEnvelope(ctx, "/platform/history/stats", url.Values{"gameType": {gameType}}, "totalGames")
			}),
		)
	}
	return checks
}

func (c *apiClient) probeStatus(ctx context.Context, path string, expected int) error {
	status, _, err := c.request(ctx, false, http.MethodGet, c.baseURL+path, nil, nil)
	return expectStatus(status, err, expected)
}

func (c *apiClient) probeLeaderboard(ctx context.Context, gameType string) error {
	var envelope struct {
		Data struct {
			GameType string           `json:"gameType"`
			Entries  []map[string]any `json:"entries"`
			Page     map[string]any   `json:"page"`
		} `json:"data"`
	}
	if err := c.getPublicJSON(ctx, "/platform/rankings", url.Values{
		"gameType": {gameType},
		"sort":     {"metric"},
		"limit":    {"5"},
		"offset":   {"0"},
	}, &envelope); err != nil {
		return err
	}
	if envelope.Data.GameType != gameType {
		return fmt.Errorf("response gameType = %q, want %q", envelope.Data.GameType, gameType)
	}
	if envelope.Data.Page == nil {
		return errors.New("leaderboard page is missing")
	}
	for _, entry := range envelope.Data.Entries {
		metric, _ := entry["metric"].(map[string]any)
		if metric == nil {
			return errors.New("leaderboard metric is missing")
		}
		key := "rank"
		if gameType == "davinci" {
			key = "davinci"
		} else if gameType == "yinsh" {
			key = "yinsh"
		} else if gameType == "othello" {
			key = "othello"
		} else if gameType == "fugitive" {
			key = "fugitive"
		} else if gameType == "warchest" {
			key = "warchest"
		} else if gameType == "quoridor" {
			key = "quoridor"
		} else if gameType == "hex" {
			key = "hex"
		} else if gameType == "onitama" {
			key = "onitama"
		} else if gameType == "hive" {
			key = "hive"
		}
		if metric[key] == nil {
			return fmt.Errorf("leaderboard metric %q is missing", key)
		}
	}
	return nil
}

func (c *apiClient) probeEnvelope(ctx context.Context, path string, query url.Values, dataKey string) error {
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := c.getJSON(ctx, path, query, &envelope); err != nil {
		return err
	}
	if len(envelope.Data[dataKey]) == 0 {
		return fmt.Errorf("response data.%s is missing", dataKey)
	}
	return nil
}

func (c *apiClient) createRoom(
	ctx context.Context,
	gameType,
	requestID string,
	maxPlayers int,
) (roomInfo, error) {
	var envelope roomEnvelope
	err := c.doJSON(ctx, http.MethodPost, "/platform/rooms", nil, map[string]any{
		"gameType":   gameType,
		"visibility": "private",
		"maxPlayers": maxPlayers,
		"requestID":  requestID,
	}, &envelope)
	if err != nil {
		return roomInfo{}, err
	}
	if envelope.Data.RoomID == "" || envelope.Data.CurrentMatchID == "" {
		return roomInfo{}, errors.New("create room response omitted roomID or currentMatchID")
	}
	return envelope.Data, nil
}

func (c *apiClient) closeRoom(ctx context.Context, gameType, roomID, requestID string) (roomInfo, error) {
	var envelope roomEnvelope
	err := c.doJSON(ctx, http.MethodDelete, "/platform/rooms/"+url.PathEscape(roomID), url.Values{
		"gameType": {gameType},
	}, map[string]any{"requestID": requestID}, &envelope)
	return envelope.Data, err
}

func (c *apiClient) getRoom(ctx context.Context, gameType, roomID string) (roomInfo, error) {
	var envelope roomEnvelope
	err := c.doJSON(ctx, http.MethodGet, "/platform/rooms/"+url.PathEscape(roomID), url.Values{
		"gameType": {gameType},
	}, nil, &envelope)
	return envelope.Data, err
}

func (c *apiClient) doJSON(
	ctx context.Context,
	method,
	path string,
	query url.Values,
	body any,
	output any,
) error {
	status, raw, err := c.request(ctx, true, method, c.baseURL+path, query, body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return decodeAPIError(status, raw)
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, path, err)
	}
	return nil
}

func (c *apiClient) getJSON(ctx context.Context, path string, query url.Values, output any) error {
	return c.doJSON(ctx, http.MethodGet, path, query, nil, output)
}

func (c *apiClient) getPublicJSON(ctx context.Context, path string, query url.Values, output any) error {
	status, raw, err := c.request(ctx, false, http.MethodGet, c.baseURL+path, query, nil)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return decodeAPIError(status, raw)
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return fmt.Errorf("decode public response: %w", err)
	}
	return nil
}

func (c *apiClient) request(
	ctx context.Context,
	authenticated bool,
	method,
	rawURL string,
	query url.Values,
	body any,
) (int, []byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0, nil, err
	}
	if query != nil {
		parsed.RawQuery = query.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", c.origin)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		if c.token == "" {
			return 0, nil, errors.New("access token is required")
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(raw) > maxResponseBytes {
		return resp.StatusCode, nil, errors.New("response exceeds size limit")
	}
	return resp.StatusCode, raw, nil
}

func decodeAPIError(status int, raw []byte) error {
	var envelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &envelope)
	if envelope.Message == "" {
		envelope.Message = http.StatusText(status)
	}
	return &apiError{Status: status, Code: envelope.Code, Message: envelope.Message}
}

func expectStatus(status int, err error, expected int) error {
	if err != nil {
		return err
	}
	if status != expected {
		return fmt.Errorf("status = %d, want %d", status, expected)
	}
	return nil
}

func stringValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		switch value := values[key].(type) {
		case string:
			return value
		case json.Number:
			return value.String()
		case float64:
			return strconv.FormatInt(int64(value), 10)
		}
	}
	return ""
}

func isAPIError(err error, status int, code string) bool {
	var target *apiError
	return errors.As(err, &target) &&
		target.Status == status &&
		(code == "" || strings.EqualFold(target.Code, code))
}
