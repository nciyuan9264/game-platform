package gateway

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/coordination"
)

type fakeOwnerResolver struct {
	owner coordination.RoomOwner
	err   error
}

func (r fakeOwnerResolver) ReadyRoomOwner(context.Context, string) (coordination.RoomOwner, error) {
	return r.owner, r.err
}

func TestWebsocketOriginAllowed(t *testing.T) {
	t.Setenv("WS_ALLOWED_ORIGINS", "https://game.example.com, http://localhost:18032")

	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{name: "non browser client", host: "gateway:8080", want: true},
		{name: "same origin", host: "game.example.com", origin: "https://game.example.com", want: true},
		{name: "configured origin", host: "gateway.example.com", origin: "http://localhost:18032", want: true},
		{name: "untrusted origin", host: "gateway.example.com", origin: "https://evil.example.com", want: false},
		{name: "invalid scheme", host: "gateway.example.com", origin: "file:///tmp/game", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tt.host+"/ws", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if got := websocketOriginAllowed(req); got != tt.want {
				t.Fatalf("websocketOriginAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRoomRouteRefreshRequired(t *testing.T) {
	tests := []struct {
		name string
		ack  contracts.CommandAck
		err  error
		want bool
	}{
		{name: "transport error", err: errors.New("no responders"), want: true},
		{name: "stale epoch", ack: contracts.CommandAck{ErrorCode: "stale_epoch"}, want: true},
		{name: "owner mismatch", ack: contracts.CommandAck{ErrorCode: "room_error"}, want: true},
		{name: "owner draining", ack: contracts.CommandAck{ErrorCode: "roomhost_draining"}, want: true},
		{name: "accepted", ack: contracts.CommandAck{Accepted: true}, want: false},
		{name: "invalid action", ack: contracts.CommandAck{ErrorCode: "invalid_action"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := roomRouteRefreshRequired(tt.ack, tt.err); got != tt.want {
				t.Fatalf("roomRouteRefreshRequired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDisconnectMatchesCurrentRoute(t *testing.T) {
	disconnect := contracts.RoomCommand{RoomID: "room-1", MatchID: "room-1-r1"}
	if !disconnectMatchesCurrentRoute(disconnect, contracts.RoomRecord{
		RoomID: "room-1", CurrentMatchID: "room-1-r1",
	}) {
		t.Fatal("same-match disconnect was not retryable")
	}
	for _, room := range []contracts.RoomRecord{
		{RoomID: "room-1", CurrentMatchID: "room-1-r2"},
		{RoomID: "room-2", CurrentMatchID: "room-1-r1"},
	} {
		if disconnectMatchesCurrentRoute(disconnect, room) {
			t.Fatalf("disconnect crossed match or room boundary: %+v", room)
		}
	}
}

func TestReadyRouteOverridesPostgresProjection(t *testing.T) {
	server := &Server{owners: fakeOwnerResolver{owner: coordination.RoomOwner{
		RoomID:    "room-1",
		OwnerNode: "roomhost-b",
		Epoch:     8,
	}}}
	routed, err := server.readyRoute(context.Background(), contracts.RoomRecord{
		RoomID:    "room-1",
		OwnerNode: "roomhost-a",
		Epoch:     7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if routed.OwnerNode != "roomhost-b" || routed.Epoch != 8 {
		t.Fatalf("route = %+v", routed)
	}
}

func TestReadyRouteRejectsStaleLeaseProjection(t *testing.T) {
	server := &Server{owners: fakeOwnerResolver{owner: coordination.RoomOwner{
		RoomID:    "room-1",
		OwnerNode: "roomhost-a",
		Epoch:     7,
	}}}
	if _, err := server.readyRoute(context.Background(), contracts.RoomRecord{
		RoomID: "room-1",
		Epoch:  8,
	}); err == nil {
		t.Fatal("stale ready route was accepted")
	}
}

func TestClientCommandTypeAllowed(t *testing.T) {
	tests := []struct {
		commandType string
		want        bool
	}{
		{commandType: "game_ready", want: true},
		{commandType: "runtime_room_close", want: false},
		{commandType: "runtime_room_create", want: false},
		{commandType: "runtime_unknown", want: false},
		{commandType: "", want: false},
	}
	for _, tt := range tests {
		if got := clientCommandTypeAllowed(tt.commandType); got != tt.want {
			t.Fatalf("clientCommandTypeAllowed(%q) = %v, want %v", tt.commandType, got, tt.want)
		}
	}
}

func TestClientCommandPayload(t *testing.T) {
	t.Run("common payload", func(t *testing.T) {
		got, err := clientCommandPayload(contracts.GameAcquire, "match_ready", map[string]any{
			"common": map[string]any{
				"matchReady": map[string]any{"ready": true},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		payload, ok := got.(map[string]any)
		if !ok || payload["ready"] != true {
			t.Fatalf("clientCommandPayload() = %#v", got)
		}
	})

	t.Run("empty payload", func(t *testing.T) {
		got, err := clientCommandPayload(contracts.GameDaVinci, "game_ready", map[string]any{
			"common": map[string]any{"empty": map[string]any{}},
		})
		if err != nil || got != nil {
			t.Fatalf("clientCommandPayload() = %#v, %v", got, err)
		}
	})

	t.Run("game payload", func(t *testing.T) {
		got, err := clientCommandPayload(contracts.GameAcquire, "game_place_tile", map[string]any{
			"acquire": map[string]any{
				"placeTile": map[string]any{"tileKey": "1A"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		payload, ok := got.(map[string]any)
		if !ok || payload["tileKey"] != "1A" {
			t.Fatalf("clientCommandPayload() = %#v", got)
		}
	})

	t.Run("davinci initialization payload", func(t *testing.T) {
		got, err := clientCommandPayload(contracts.GameDaVinci, "game_initialize_hand", map[string]any{
			"davinci": map[string]any{
				"initializeHand": map[string]any{
					"jokerPlacements": []any{
						map[string]any{"id": "joker-1", "index": float64(2)},
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		payload, ok := got.(map[string]any)
		if !ok || len(payload["jokerPlacements"].([]any)) != 1 {
			t.Fatalf("clientCommandPayload() = %#v", got)
		}
	})

	t.Run("splendor card id", func(t *testing.T) {
		got, err := clientCommandPayload(contracts.GameSplendor, "game_buy_card", map[string]any{
			"splendor": map[string]any{
				"buyCard": map[string]any{"cardID": float64(9)},
			},
		})
		if err != nil || got != float64(9) {
			t.Fatalf("clientCommandPayload() = %#v, %v", got, err)
		}
	})

	t.Run("yinsh move payload", func(t *testing.T) {
		got, err := clientCommandPayload(contracts.GameYinsh, "game_move_ring", map[string]any{
			"yinsh": map[string]any{
				"moveRing": map[string]any{"from": "E4", "to": "E8"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		payload, ok := got.(map[string]any)
		if !ok || payload["from"] != "E4" || payload["to"] != "E8" {
			t.Fatalf("clientCommandPayload() = %#v", got)
		}
	})

	t.Run("othello move and empty payloads", func(t *testing.T) {
		got, err := clientCommandPayload(
			contracts.GameOthello,
			"game_place_disc",
			map[string]any{
				"othello": map[string]any{
					"placeDisc": map[string]any{"index": float64(19)},
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		payload, ok := got.(map[string]any)
		if !ok || payload["index"] != float64(19) {
			t.Fatalf("clientCommandPayload() = %#v", got)
		}
		got, err = clientCommandPayload(
			contracts.GameOthello,
			"game_pass",
			map[string]any{
				"othello": map[string]any{
					"pass": map[string]any{},
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.(map[string]any); !ok {
			t.Fatalf("pass payload = %#v", got)
		}
	})

	t.Run("fugitive move payload", func(t *testing.T) {
		got, err := clientCommandPayload(
			contracts.GameFugitive,
			"game_play_hideout",
			map[string]any{
				"fugitive": map[string]any{
					"playHideout": map[string]any{
						"hideout": float64(20),
						"sprints": []any{float64(12)},
					},
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		payload, ok := got.(map[string]any)
		if !ok ||
			payload["hideout"] != float64(20) ||
			len(payload["sprints"].([]any)) != 1 {
			t.Fatalf("clientCommandPayload() = %#v", got)
		}
	})

	t.Run("warchest action payload", func(t *testing.T) {
		got, err := clientCommandPayload(
			contracts.GameWarChest,
			"game_play_action",
			map[string]any{
				"warchest": map[string]any{
					"playAction": map[string]any{
						"actionID": "a-4-abcdef",
					},
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		payload, ok := got.(map[string]any)
		if !ok || payload["actionID"] != "a-4-abcdef" {
			t.Fatalf("clientCommandPayload() = %#v", got)
		}
	})

	t.Run("quoridor move wall and empty payloads", func(t *testing.T) {
		got, err := clientCommandPayload(
			contracts.GameQuoridor,
			"game_move_pawn",
			map[string]any{
				"quoridor": map[string]any{
					"movePawn": map[string]any{
						"to": map[string]any{
							"row": float64(7), "column": float64(4),
						},
					},
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		payload, ok := got.(map[string]any)
		to, toOK := payload["to"].(map[string]any)
		if !ok || !toOK ||
			to["row"] != float64(7) ||
			to["column"] != float64(4) {
			t.Fatalf("move payload = %#v", got)
		}
		got, err = clientCommandPayload(
			contracts.GameQuoridor,
			"game_place_wall",
			map[string]any{
				"quoridor": map[string]any{
					"placeWall": map[string]any{
						"wall": map[string]any{
							"row":         float64(4),
							"column":      float64(3),
							"orientation": "h",
						},
					},
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		payload, ok = got.(map[string]any)
		wall, wallOK := payload["wall"].(map[string]any)
		if !ok || !wallOK || wall["orientation"] != "h" {
			t.Fatalf("wall payload = %#v", got)
		}
		got, err = clientCommandPayload(
			contracts.GameQuoridor,
			"game_resign",
			map[string]any{
				"quoridor": map[string]any{
					"resign": map[string]any{},
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.(map[string]any); !ok {
			t.Fatalf("resign payload = %#v", got)
		}
		if _, err := clientCommandPayload(
			contracts.GameQuoridor,
			"game_move_pawn",
			map[string]any{
				"quoridor": map[string]any{},
			},
		); err == nil {
			t.Fatal("missing quoridor union variant was accepted")
		}
	})

	t.Run("wrong union group", func(t *testing.T) {
		if _, err := clientCommandPayload(contracts.GameAcquire, "game_place_tile", map[string]any{
			"davinci": map[string]any{},
		}); err == nil {
			t.Fatal("clientCommandPayload() accepted a mismatched union group")
		}
	})
}

func TestWebsocketReadLimitStaysBelowCommandLogLimit(t *testing.T) {
	if websocketReadLimit <= 0 || websocketReadLimit > 1<<20 {
		t.Fatalf("websocketReadLimit = %d", websocketReadLimit)
	}
}

func TestCommandResult(t *testing.T) {
	room := contracts.RoomRecord{
		RoomID:         "room-1",
		CurrentMatchID: "match-1",
		LastAppliedSeq: 6,
	}

	t.Run("accepted", func(t *testing.T) {
		got := commandResult(room, "cmd-1", contracts.CommandAck{
			CommandID: "cmd-1",
			Accepted:  true,
			Seq:       7,
		}, nil)
		if got.SchemaVersion != contracts.RealtimeSchemaVersion ||
			got.Type != contracts.MessageTypeCommandResult ||
			got.RoomID != room.RoomID ||
			got.MatchID != room.CurrentMatchID ||
			got.CommandID != "cmd-1" ||
			!got.Accepted ||
			got.Seq != 7 ||
			got.Retryable {
			t.Fatalf("commandResult() = %+v", got)
		}
	})

	t.Run("transport failure is retryable", func(t *testing.T) {
		got := commandResult(room, "cmd-2", contracts.CommandAck{}, errors.New("no responders"))
		if got.Accepted ||
			!got.Retryable ||
			got.ErrorCode != "command_transport_unavailable" ||
			got.Seq != room.LastAppliedSeq {
			t.Fatalf("commandResult() = %+v", got)
		}
	})

	t.Run("command log failure is retryable", func(t *testing.T) {
		got := commandResult(room, "cmd-3", contracts.CommandAck{
			ErrorCode: "command_log_unavailable",
			Message:   "append failed",
		}, nil)
		if got.Accepted || !got.Retryable {
			t.Fatalf("commandResult() = %+v", got)
		}
	})

	t.Run("invalid action is final", func(t *testing.T) {
		got := commandResult(room, "cmd-4", contracts.CommandAck{
			ErrorCode: "invalid_action",
			Message:   "not allowed",
		}, nil)
		if got.Accepted || got.Retryable {
			t.Fatalf("commandResult() = %+v", got)
		}
	})
}
