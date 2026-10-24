package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

const realtimeSchemaVersion = 2

type clientCommand struct {
	SchemaVersion int            `json:"schemaVersion"`
	CommandID     string         `json:"commandID"`
	MatchID       string         `json:"matchID"`
	Type          string         `json:"type"`
	Payload       map[string]any `json:"payload"`
}

type realtimeEnvelope struct {
	SchemaVersion int
	Type          string
	RoomID        string
	MatchID       string
	Seq           int64
	CommandID     string
	Accepted      bool
	ErrorCode     string
	Message       string
	PlayerID      string
	Status        string
	View          map[string]json.RawMessage
	Raw           map[string]json.RawMessage
}

type realtimeClient struct {
	conn       *websocket.Conn
	roomID     string
	matchID    string
	latestSync *realtimeEnvelope
}

func dialRealtime(
	ctx context.Context,
	cfg config,
	roomID,
	matchID string,
	lastSeq int64,
) (*realtimeClient, error) {
	endpoint, err := url.Parse(cfg.WSBase)
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("roomID", roomID)
	query.Set("matchID", matchID)
	query.Set("lastSeq", strconv.FormatInt(lastSeq, 10))
	endpoint.RawQuery = query.Encode()
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+cfg.AccessToken)
	headers.Set("Origin", cfg.Origin)
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, endpoint.String(), headers)
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("websocket handshake status %d: %w", response.StatusCode, err)
		}
		return nil, err
	}
	return &realtimeClient{conn: conn, roomID: roomID, matchID: matchID}, nil
}

func (c *realtimeClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *realtimeClient) send(command clientCommand) error {
	return c.conn.WriteJSON(command)
}

func (c *realtimeClient) sendAndWait(
	ctx context.Context,
	command clientCommand,
) (realtimeEnvelope, error) {
	if err := c.send(command); err != nil {
		return realtimeEnvelope{}, err
	}
	return c.readUntil(ctx, func(message realtimeEnvelope) bool {
		return message.Type == "COMMAND_RESULT" && message.CommandID == command.CommandID
	})
}

func (c *realtimeClient) waitForSync(
	ctx context.Context,
	predicate func(realtimeEnvelope) bool,
) (realtimeEnvelope, error) {
	if c.latestSync != nil && predicate(*c.latestSync) {
		return *c.latestSync, nil
	}
	return c.readUntil(ctx, func(message realtimeEnvelope) bool {
		return (message.Type == "MATCH_SYNC" || message.Type == "ROOM_SYNC") &&
			predicate(message)
	})
}

func (c *realtimeClient) readUntil(
	ctx context.Context,
	predicate func(realtimeEnvelope) bool,
) (realtimeEnvelope, error) {
	for {
		if deadline, ok := ctx.Deadline(); ok {
			if err := c.conn.SetReadDeadline(deadline); err != nil {
				return realtimeEnvelope{}, err
			}
		}
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			return realtimeEnvelope{}, err
		}
		message, err := decodeRealtimeEnvelope(payload)
		if err != nil {
			return realtimeEnvelope{}, err
		}
		if message.RoomID != c.roomID || message.MatchID != c.matchID {
			return realtimeEnvelope{}, fmt.Errorf(
				"unexpected realtime route %s/%s",
				message.RoomID,
				message.MatchID,
			)
		}
		if message.Type == "MATCH_SYNC" || message.Type == "ROOM_SYNC" {
			copy := message
			c.latestSync = &copy
		}
		if predicate(message) {
			return message, nil
		}
	}
}

func decodeRealtimeEnvelope(payload []byte) (realtimeEnvelope, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		return realtimeEnvelope{}, fmt.Errorf("decode realtime JSON: %w", err)
	}
	var message realtimeEnvelope
	message.Raw = raw
	decodeField(raw, "schemaVersion", &message.SchemaVersion)
	decodeField(raw, "type", &message.Type)
	decodeField(raw, "roomID", &message.RoomID)
	decodeField(raw, "matchID", &message.MatchID)
	decodeField(raw, "commandID", &message.CommandID)
	decodeField(raw, "accepted", &message.Accepted)
	decodeField(raw, "errorCode", &message.ErrorCode)
	decodeField(raw, "message", &message.Message)
	decodeField(raw, "playerID", &message.PlayerID)
	decodeField(raw, "status", &message.Status)
	if value := raw["seq"]; len(value) > 0 {
		seq, err := decodeInt64(value)
		if err != nil {
			return realtimeEnvelope{}, fmt.Errorf("decode realtime seq: %w", err)
		}
		message.Seq = seq
	}
	if value := raw["view"]; len(value) > 0 {
		if err := json.Unmarshal(value, &message.View); err != nil {
			return realtimeEnvelope{}, fmt.Errorf("decode realtime view: %w", err)
		}
	}
	if message.SchemaVersion != realtimeSchemaVersion {
		return realtimeEnvelope{}, fmt.Errorf("schemaVersion = %d, want %d", message.SchemaVersion, realtimeSchemaVersion)
	}
	if message.Type == "" || message.RoomID == "" || message.MatchID == "" {
		return realtimeEnvelope{}, errors.New("realtime envelope omitted route fields")
	}
	return message, nil
}

func decodeField(raw map[string]json.RawMessage, name string, output any) {
	if value := raw[name]; len(value) > 0 {
		_ = json.Unmarshal(value, output)
	}
}

func decodeInt64(raw json.RawMessage) (int64, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.Int64()
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return strconv.ParseInt(value, 10, 64)
}

func commonCommand(matchID, commandID, commandType string, payload map[string]any) clientCommand {
	variant := "empty"
	switch commandType {
	case "match_add_ai":
		variant = "matchSeat"
	case "match_ready":
		variant = "matchReady"
	case "room_update_settings":
		variant = "roomSettings"
	case "match_remove_player":
		variant = "removePlayer"
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return clientCommand{
		SchemaVersion: realtimeSchemaVersion,
		CommandID:     commandID,
		MatchID:       matchID,
		Type:          commandType,
		Payload: map[string]any{
			"common": map[string]any{variant: payload},
		},
	}
}

func gameCommand(
	gameType,
	matchID,
	commandID,
	commandType,
	variant string,
	payload any,
) clientCommand {
	return clientCommand{
		SchemaVersion: realtimeSchemaVersion,
		CommandID:     commandID,
		MatchID:       matchID,
		Type:          commandType,
		Payload: map[string]any{
			gameType: map[string]any{variant: payload},
		},
	}
}

func commandContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 35*time.Second)
}
