package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestBuildReplaySnapshotUsesMaterializedFrame(t *testing.T) {
	payload, err := history.EncodeReplayFrame(1, map[string]any{
		"roomData": map[string]any{
			"companyInfo":   map[string]any{},
			"currentPlayer": "alice",
			"gameStatus":    "setTile",
			"tiles":         map[string]any{},
			"players":       map[string]any{},
			"endGame":       map[string]any{},
		},
		"playersData": map[string]any{},
		"result":      map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	event := &history.Event{
		Seq:           2,
		PlayerID:      "alice",
		CommandType:   "game_place_tile",
		Payload:       json.RawMessage(`{"tileKey":"1A"}`),
		ReplayPayload: payload,
	}
	snapshot, err := (&Handler{}).buildReplaySnapshot(
		context.Background(),
		history.Game{
			GameType:            contracts.GameAcquire,
			ReplaySchemaVersion: 1,
		},
		nil,
		event,
		2,
		5,
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Acquire == nil ||
		snapshot.Acquire.Seq != 2 ||
		snapshot.Acquire.TotalEvents != 5 ||
		snapshot.Acquire.CurrentEvent == nil ||
		snapshot.Acquire.CurrentEvent.GetPayloadJSON() != `{"tileKey":"1A"}` {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestBuildReplaySnapshotFailsClosedForMissingAndUnknownSchema(t *testing.T) {
	handler := &Handler{}
	_, err := handler.buildReplaySnapshot(
		context.Background(),
		history.Game{
			GameType:            contracts.GameAcquire,
			ReplaySchemaVersion: 1,
		},
		nil,
		nil,
		-1,
		0,
	)
	assertPlatformErrorCode(t, err, "replay_materialization_missing")

	payload, encodeErr := history.EncodeReplayFrame(1, map[string]any{
		"roomData":    map[string]any{},
		"playersData": map[string]any{},
		"result":      map[string]any{},
	})
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	_, err = handler.buildReplaySnapshot(
		context.Background(),
		history.Game{
			GameType:             contracts.GameAcquire,
			ReplaySchemaVersion:  99,
			InitialReplayPayload: payload,
		},
		nil,
		nil,
		-1,
		0,
	)
	assertPlatformErrorCode(t, err, "replay_schema_unsupported")
}

func TestBuildReplaySnapshotSupportsYinsh(t *testing.T) {
	payload, err := history.EncodeReplayFrame(1, map[string]any{
		"roomData": map[string]any{
			"rings": map[string]any{}, "markers": map[string]any{}, "currentPlayer": "alice",
			"gameStatus": "placement", "placedRings": map[string]int{"white": 0, "black": 0},
			"markerPool": 51, "legalMoves": map[string]any{}, "candidateRows": []any{},
			"players": map[string]any{}, "playerOrder": []string{"alice", "bob"}, "roundNo": 1,
		},
		"playersData": map[string]any{},
		"result": map[string]any{
			"reason": "", "ringsRemoved": map[string]int{"white": 0, "black": 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := (&Handler{}).buildReplaySnapshot(
		context.Background(),
		history.Game{
			GameType:             contracts.GameYinsh,
			ReplaySchemaVersion:  1,
			InitialReplayPayload: payload,
		},
		nil,
		nil,
		-1,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Yinsh == nil ||
		snapshot.Yinsh.Seq != -1 ||
		snapshot.Yinsh.RoomData.MarkerPool != 51 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func assertPlatformErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var platformErr interface{ GetCode() string }
	if !errors.As(err, &platformErr) || platformErr.GetCode() != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}
