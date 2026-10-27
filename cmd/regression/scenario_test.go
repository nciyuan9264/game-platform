package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type fakeCanaryVerifier struct {
	mu          sync.Mutex
	activeCalls int
	closedCalls int
}

func (v *fakeCanaryVerifier) VerifyActive(_ context.Context, observation canaryObservation) []checkResult {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.activeCalls++
	if observation.Room.RoomID == "" || len(observation.Commands) != 4 {
		return []checkResult{{Name: "fake active", Status: checkFailed, Message: "incomplete observation"}}
	}
	return []checkResult{{Name: "fake active", Status: checkPassed}}
}

func (v *fakeCanaryVerifier) VerifyClosed(_ context.Context, observation canaryObservation) []checkResult {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closedCalls++
	if observation.Room.RoomID == "" {
		return []checkResult{{Name: "fake closed", Status: checkFailed, Message: "room is missing"}}
	}
	return []checkResult{{Name: "fake closed", Status: checkPassed}}
}

func (*fakeCanaryVerifier) Close() {}

func TestRunCanaryCoversIdempotencyRealtimeReconnectAndCleanup(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var mu sync.Mutex
	createRequestID := ""
	actionAttempts := 0
	closed := false

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/pam-api/platform/rooms" && r.Method == http.MethodPost:
			var body struct {
				RequestID  string `json:"requestID"`
				MaxPlayers int    `json:"maxPlayers"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			defer mu.Unlock()
			if createRequestID == "" {
				createRequestID = body.RequestID
			}
			if body.RequestID == createRequestID && body.MaxPlayers != 2 {
				writeTestJSON(w, http.StatusConflict, map[string]any{
					"code": "idempotency_conflict", "message": "conflict",
				})
				return
			}
			writeTestRoom(w, "open")
		case strings.HasPrefix(r.URL.Path, "/pam-api/platform/rooms/") && r.Method == http.MethodDelete:
			mu.Lock()
			closed = true
			mu.Unlock()
			writeTestRoom(w, "closed")
		case r.URL.Path == "/ws":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			lastSeq := r.URL.Query().Get("lastSeq")
			if lastSeq != "" && lastSeq != "0" {
				_ = conn.WriteJSON(testAcquireSync(6))
			} else {
				_ = conn.WriteJSON(map[string]any{
					"schemaVersion": 2,
					"type":          "MATCH_SYNC",
					"roomID":        "room-1",
					"matchID":       "room-1-r1",
					"seq":           1,
					"playerID":      "7",
					"players":       map[string]any{"7": map[string]any{}},
				})
			}
			for {
				var command clientCommand
				if err := conn.ReadJSON(&command); err != nil {
					return
				}
				switch command.Type {
				case "match_add_ai":
					writeTestCommandResult(conn, command, true, 2, "")
				case "match_begin":
					writeTestCommandResult(conn, command, true, 3, "")
				case "game_ready":
					writeTestCommandResult(conn, command, true, 4, "")
					_ = conn.WriteJSON(testAcquireSync(4))
				case "game_place_tile":
					mu.Lock()
					actionAttempts++
					attempt := actionAttempts
					mu.Unlock()
					if attempt == 1 {
						writeTestCommandResult(conn, command, true, 5, "")
						_ = conn.WriteJSON(testAcquireSync(5))
					} else if attempt == 2 {
						writeTestCommandResult(conn, command, true, 5, "")
					} else {
						writeTestCommandResult(conn, command, false, 5, "command_id_reused")
					}
				case "runtime_room_close":
					writeTestCommandResult(conn, command, false, 6, "unauthorized_command")
				}
			}
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	cfg := config{
		RunID:       "test-run",
		HTTPBase:    server.URL + "/pam-api",
		WSBase:      "ws" + strings.TrimPrefix(server.URL, "http") + "/ws",
		FrontendURL: server.URL,
		Origin:      server.URL,
		AccessToken: "test-token",
	}
	verifier := &fakeCanaryVerifier{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := runCanary(ctx, cfg, newAPIClient(cfg), verifier, "acquire")
	for _, check := range result.Checks {
		if check.Status == checkFailed {
			t.Fatalf("canary check %q failed: %s", check.Name, check.Message)
		}
	}
	if result.RoomID != "room-1" || result.MatchID != "room-1-r1" {
		t.Fatalf("result route = %+v", result)
	}
	verifier.mu.Lock()
	activeCalls, closedCalls := verifier.activeCalls, verifier.closedCalls
	verifier.mu.Unlock()
	if activeCalls != 1 || closedCalls != 1 {
		t.Fatalf("verifier calls = active:%d closed:%d", activeCalls, closedCalls)
	}
	mu.Lock()
	wasClosed := closed
	mu.Unlock()
	if !wasClosed {
		t.Fatal("canary room was not closed")
	}
}

func TestSelectFirstQuoridorActionUsesFirstLegalMove(t *testing.T) {
	view, err := json.Marshal(map[string]any{
		"playerId": "7",
		"roomData": map[string]any{
			"currentPlayer": "7",
			"legalMoves": []any{
				map[string]any{"row": 7, "column": 4},
				map[string]any{"row": 8, "column": 3},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	action, ok := selectFirstAction(
		"test-run",
		"quoridor",
		"room-1-r1",
		"7",
		realtimeEnvelope{
			Type: "ROOM_SYNC",
			View: map[string]json.RawMessage{"quoridor": view},
		},
	)
	if !ok ||
		action.Command.Type != "game_move_pawn" ||
		action.ConflictCommand.Type != "game_move_pawn" ||
		action.Command.CommandID != action.ConflictCommand.CommandID {
		t.Fatalf("action = %+v/%v", action, ok)
	}
	move, moveOK := action.Command.Payload["quoridor"].(map[string]any)
	if !moveOK {
		t.Fatalf("payload = %+v", action.Command.Payload)
	}
	movePawn, movePawnOK := move["movePawn"].(map[string]any)
	to, toOK := movePawn["to"].(map[string]any)
	if !movePawnOK || !toOK ||
		numericValue(to["row"]) != 7 ||
		numericValue(to["column"]) != 4 {
		t.Fatalf("move payload = %+v", action.Command.Payload)
	}
}

func TestSelectFirstOnitamaActionUsesFirstLegalMove(t *testing.T) {
	view, err := json.Marshal(map[string]any{
		"playerId": "7",
		"roomData": map[string]any{
			"currentPlayer": "7",
			"legalMoves": []any{
				map[string]any{
					"cardID":  "tiger",
					"pieceID": "blue-master",
					"to": map[string]any{
						"row": 2, "column": 2,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	action, ok := selectFirstAction(
		"test-run",
		"onitama",
		"room-1-r1",
		"7",
		realtimeEnvelope{
			Type: "ROOM_SYNC",
			View: map[string]json.RawMessage{"onitama": view},
		},
	)
	if !ok ||
		action.Command.Type != "game_move_piece" ||
		action.ConflictCommand.Type != "game_move_piece" ||
		action.Command.CommandID != action.ConflictCommand.CommandID {
		t.Fatalf("action = %+v/%v", action, ok)
	}
	group, groupOK := action.Command.Payload["onitama"].(map[string]any)
	move, moveOK := group["movePiece"].(map[string]any)
	to, toOK := move["to"].(map[string]any)
	if !groupOK || !moveOK || !toOK ||
		move["cardID"] != "tiger" ||
		move["pieceID"] != "blue-master" ||
		numericValue(to["row"]) != 2 ||
		numericValue(to["column"]) != 2 {
		t.Fatalf("move payload = %+v", action.Command.Payload)
	}
}

func TestSelectFirstLoveLetterActionDrawsWithoutLeakingIntent(t *testing.T) {
	view, err := json.Marshal(map[string]any{
		"playerId": "7",
		"roomData": map[string]any{
			"currentPlayer": "7",
			"gameStatus":    "draw",
		},
		"privateData": map[string]any{
			"hand": []any{map[string]any{"cardID": "r1-4-0", "rank": 4}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	action, ok := selectFirstAction(
		"test-run",
		"loveletter",
		"room-1-r1",
		"7",
		realtimeEnvelope{
			Type: "ROOM_SYNC",
			View: map[string]json.RawMessage{"loveletter": view},
		},
	)
	if !ok ||
		action.Command.Type != "game_draw_card" ||
		action.ConflictCommand.Type != "game_play_card" ||
		action.Command.CommandID != action.ConflictCommand.CommandID {
		t.Fatalf("action = %+v/%v", action, ok)
	}
	draw, drawOK := action.Command.Payload["loveletter"].(map[string]any)
	if !drawOK || draw["drawCard"] == nil {
		t.Fatalf("draw payload = %+v", action.Command.Payload)
	}
}

func TestSelectFirstHexActionUsesFirstLegalCell(t *testing.T) {
	view, err := json.Marshal(map[string]any{
		"playerId": "7",
		"roomData": map[string]any{
			"currentPlayer": "7",
			"legalMoves": []any{
				map[string]any{"row": 5, "column": 5},
				map[string]any{"row": 5, "column": 6},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	action, ok := selectFirstAction(
		"test-run",
		"hex",
		"room-1-r1",
		"7",
		realtimeEnvelope{
			Type: "ROOM_SYNC",
			View: map[string]json.RawMessage{"hex": view},
		},
	)
	if !ok ||
		action.Command.Type != "game_place_stone" ||
		action.ConflictCommand.Type != "game_place_stone" ||
		action.Command.CommandID != action.ConflictCommand.CommandID {
		t.Fatalf("action = %+v/%v", action, ok)
	}
	group, groupOK := action.Command.Payload["hex"].(map[string]any)
	place, placeOK := group["placeStone"].(map[string]any)
	cell, cellOK := place["cell"].(map[string]any)
	if !groupOK || !placeOK || !cellOK ||
		numericValue(cell["row"]) != 5 ||
		numericValue(cell["column"]) != 5 {
		t.Fatalf("place payload = %+v", action.Command.Payload)
	}
}

func TestSelectFirstSixNimmtActionLocksLowestLegalCard(t *testing.T) {
	view, err := json.Marshal(map[string]any{
		"playerId": "7",
		"roomData": map[string]any{
			"gameStatus": "selecting",
		},
		"privateData": map[string]any{
			"hand":       []int{55, 24, 88},
			"legalCards": []int{55, 24, 88},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	action, ok := selectFirstAction(
		"test-run",
		"sixnimmt",
		"room-1-r1",
		"7",
		realtimeEnvelope{
			Type: "ROOM_SYNC",
			View: map[string]json.RawMessage{"sixnimmt": view},
		},
	)
	if !ok ||
		action.Command.Type != "game_lock_card" ||
		action.ConflictCommand.Type != "game_lock_card" ||
		action.Command.CommandID != action.ConflictCommand.CommandID {
		t.Fatalf("action = %+v/%v", action, ok)
	}
	group, groupOK := action.Command.Payload["sixnimmt"].(map[string]any)
	lock, lockOK := group["lockCard"].(map[string]any)
	if !groupOK || !lockOK || numericValue(lock["card"]) != 24 {
		t.Fatalf("lock payload = %+v", action.Command.Payload)
	}
}

func writeTestRoom(w http.ResponseWriter, status string) {
	writeTestJSON(w, http.StatusOK, map[string]any{
		"status_code": 200,
		"message":     "ok",
		"data": map[string]any{
			"roomID":             "room-1",
			"gameType":           "acquire",
			"status":             status,
			"visibility":         "private",
			"ownerUserID":        "7",
			"ownerPlayerID":      "7",
			"maxPlayers":         2,
			"currentMatchID":     "room-1-r1",
			"currentMatchStatus": "running",
		},
	})
}

func writeTestJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeTestCommandResult(
	conn *websocket.Conn,
	command clientCommand,
	accepted bool,
	seq int64,
	errorCode string,
) {
	_ = conn.WriteJSON(map[string]any{
		"schemaVersion": 2,
		"type":          "COMMAND_RESULT",
		"roomID":        "room-1",
		"matchID":       "room-1-r1",
		"seq":           seq,
		"commandID":     command.CommandID,
		"accepted":      accepted,
		"errorCode":     errorCode,
	})
}

func testAcquireSync(seq int64) map[string]any {
	return map[string]any{
		"schemaVersion": 2,
		"type":          "ROOM_SYNC",
		"roomID":        "room-1",
		"matchID":       "room-1-r1",
		"seq":           seq,
		"view": map[string]any{
			"acquire": map[string]any{
				"playerId":   "7",
				"playerData": map[string]any{"tiles": []string{"1A"}},
				"roomData": map[string]any{
					"currentPlayer": "7",
					"gameStatus":    "setTile",
				},
			},
		},
	}
}
