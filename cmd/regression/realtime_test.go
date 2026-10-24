package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRealtimeClientHandlesSyncBeforeCommandResult(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{
			"schemaVersion": 2,
			"type":          "MATCH_SYNC",
			"roomID":        "room-1",
			"matchID":       "room-1-r1",
			"seq":           1,
			"playerID":      "7",
			"players":       map[string]any{},
		})
		var command clientCommand
		if err := conn.ReadJSON(&command); err != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{
			"schemaVersion": 2,
			"type":          "ROOM_SYNC",
			"roomID":        "room-1",
			"matchID":       "room-1-r1",
			"seq":           2,
			"view":          map[string]any{"acquire": map[string]any{}},
		})
		_ = conn.WriteJSON(map[string]any{
			"schemaVersion": 2,
			"type":          "COMMAND_RESULT",
			"roomID":        "room-1",
			"matchID":       "room-1-r1",
			"seq":           2,
			"commandID":     command.CommandID,
			"accepted":      true,
		})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := dialRealtime(ctx, config{
		WSBase:      "ws" + strings.TrimPrefix(server.URL, "http"),
		Origin:      server.URL,
		AccessToken: "test-token",
	}, "room-1", "room-1-r1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.waitForSync(ctx, func(message realtimeEnvelope) bool {
		return message.Type == "MATCH_SYNC"
	}); err != nil {
		t.Fatal(err)
	}
	command := commonCommand("room-1-r1", "command-1", "game_ready", nil)
	result, err := client.sendAndWait(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || result.Seq != 2 {
		t.Fatalf("command result = %+v", result)
	}
	if client.latestSync == nil || client.latestSync.Seq != 2 {
		t.Fatalf("latest sync = %+v", client.latestSync)
	}
}
