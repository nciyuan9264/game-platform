package history

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"testing"
)

func TestReplayCodecRoundTrip(t *testing.T) {
	payload, err := EncodeReplayFrame(1, map[string]any{
		"roomData":    map[string]any{"status": "playing"},
		"playersData": map[string]any{"alice": map[string]any{"score": 3}},
		"deck":        []any{map[string]any{"cardID": "c1", "rank": 1}},
		"reserve":     map[string]any{"cardID": "c0", "rank": 0},
		"result":      map[string]any{"alice": map[string]any{"rank": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := DecodeReplayFrame(1, payload)
	if err != nil {
		t.Fatal(err)
	}
	var room map[string]any
	if err := json.Unmarshal(frame.RoomData, &room); err != nil {
		t.Fatal(err)
	}
	if room["status"] != "playing" {
		t.Fatalf("room data = %+v", room)
	}

	event := &Event{
		Seq:         2,
		PlayerID:    "alice",
		DisplayName: "Alice-Cooper-7",
		CommandType: "game_move",
		Payload:     json.RawMessage(`{"value":1}`),
	}
	userID := int64(7)
	envelope, err := MaterializedReplayJSON(1, payload, 2, 5, event, []Player{{
		UserID:      &userID,
		PlayerID:    "alice",
		DisplayName: "Alice-Cooper-7",
	}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(envelope, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["seq"] != float64(2) || decoded["totalEvents"] != float64(5) {
		t.Fatalf("envelope metadata = %+v", decoded)
	}
	current := decoded["currentEvent"].(map[string]any)
	if current["payloadJSON"] != `{"value":1}` {
		t.Fatalf("current event = %+v", current)
	}
	if current["displayName"] != "Alice-Cooper-7" {
		t.Fatalf("current event display name = %+v", current)
	}
	deck := decoded["deck"].([]any)
	reserve := decoded["reserve"].(map[string]any)
	if len(deck) != 1 || reserve["cardID"] != "c0" {
		t.Fatalf("hidden replay fields = deck %+v reserve %+v", deck, reserve)
	}
	roomData := decoded["roomData"].(map[string]any)
	profile := roomData["players"].(map[string]any)["alice"].(map[string]any)
	if profile["userID"] != "7" || profile["displayName"] != "Alice-Cooper-7" {
		t.Fatalf("replay profile = %+v", profile)
	}
}

func TestReplayCodecRejectsMissingCorruptAndUnknownPayloads(t *testing.T) {
	if _, err := DecodeReplayFrame(1, nil); !errors.Is(err, ErrReplayMaterializationMissing) {
		t.Fatalf("missing payload error = %v", err)
	}
	if _, err := DecodeReplayFrame(1, []byte("not-gzip")); !errors.Is(err, ErrReplayPayloadCorrupt) {
		t.Fatalf("corrupt payload error = %v", err)
	}
	payload, err := EncodeReplayFrame(1, map[string]any{
		"roomData":    map[string]any{},
		"playersData": map[string]any{},
		"result":      map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeReplayFrame(99, payload); !errors.Is(err, ErrReplaySchemaUnsupported) {
		t.Fatalf("unknown schema error = %v", err)
	}
}

func TestReplayCodecRejectsOversizedDecodedPayload(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(bytes.Repeat([]byte("x"), MaxReplayFrameBytes+1)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeReplayFrame(1, compressed.Bytes()); !errors.Is(err, ErrReplayPayloadTooLarge) {
		t.Fatalf("oversized payload error = %v", err)
	}
}
