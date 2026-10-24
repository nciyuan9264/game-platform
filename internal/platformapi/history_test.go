package platformapi

import (
	"errors"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestCompletedReplayIncludesAllPlayersPrivateState(t *testing.T) {
	payload, err := history.EncodeReplayFrame(1, map[string]any{
		"roomData": map[string]any{"gameStatus": "playing"},
		"playersData": map[string]any{
			"alice": map[string]any{"tiles": []string{"1A"}, "money": 6000},
			"bob":   map[string]any{"tiles": []string{"2B"}, "money": 5000},
		},
		"result": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := acquireSnapshot(history.Game{
		ReplaySchemaVersion:  1,
		InitialReplayPayload: payload,
	}, nil, nil, -1)
	if err != nil {
		t.Fatalf("acquireSnapshot() error = %v", err)
	}
	snapshot := raw.(map[string]any)
	players := snapshot["playersData"].(map[string]any)
	bob := players["bob"].(map[string]any)
	tiles := bob["tiles"].([]any)
	if len(tiles) != 1 || tiles[0] != "2B" {
		t.Fatalf("completed replay hid another participant's state: %+v", bob)
	}
}

func TestMaterializedReplayErrorCode(t *testing.T) {
	if got := materializedReplayErrorCode(history.ErrReplayMaterializationMissing); got != "replay_materialization_missing" {
		t.Fatalf("missing replay error code = %q", got)
	}
	if got := materializedReplayErrorCode(history.ErrReplaySchemaUnsupported); got != "replay_schema_unsupported" {
		t.Fatalf("schema replay error code = %q", got)
	}
	if got := materializedReplayErrorCode(errors.New("bad payload")); got != "replay_error" {
		t.Fatalf("generic replay error code = %q", got)
	}
}
