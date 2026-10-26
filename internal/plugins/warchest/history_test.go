package warchest

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	platform "github.com/nciyuan9264/game-platform-client/kitex_gen/pam/game/platform/v1"
	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestReplayFramePreservesOmniscientPrivateData(t *testing.T) {
	state := startedTestState(t)
	projection, err := (&Plugin{}).ProjectHistory(
		context.Background(),
		state,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertWarChestReplayFrameMatchesGolden(t, projection.ReplayFrame)
	payload, err := history.EncodeReplayFrame(
		projection.ReplaySchemaVersion,
		projection.ReplayFrame,
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := history.MaterializedReplayJSON(
		projection.ReplaySchemaVersion,
		payload,
		-1,
		0,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot platform.WarChestReplaySnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PrivateData) != 2 {
		t.Fatalf("private data = %+v", snapshot.PrivateData)
	}
	for _, playerID := range state.PlayerOrder {
		private := snapshot.PrivateData[playerID]
		if private == nil ||
			len(private.Hand) != 3 ||
			len(private.BagOrder) != 6 {
			t.Fatalf("player %s private data = %+v", playerID, private)
		}
	}
}

func assertWarChestReplayFrameMatchesGolden(t *testing.T, value any) {
	t.Helper()
	data, err := os.ReadFile("testdata/replay_frame_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SchemaVersion  int      `json:"schemaVersion"`
		FrameFields    []string `json:"frameFields"`
		RoomDataFields []string `json:"roomDataFields"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &frame); err != nil {
		t.Fatal(err)
	}
	var roomData map[string]json.RawMessage
	if err := json.Unmarshal(frame["roomData"], &roomData); err != nil {
		t.Fatal(err)
	}
	frameFields := sortedReplayKeys(frame)
	roomFields := sortedReplayKeys(roomData)
	sort.Strings(fixture.FrameFields)
	sort.Strings(fixture.RoomDataFields)
	if fixture.SchemaVersion != ReplaySchemaVersion ||
		!reflect.DeepEqual(frameFields, fixture.FrameFields) ||
		!reflect.DeepEqual(roomFields, fixture.RoomDataFields) {
		t.Fatalf(
			"replay frame shape = %v/%v, fixture = %v/%v",
			frameFields,
			roomFields,
			fixture.FrameFields,
			fixture.RoomDataFields,
		)
	}
}

func sortedReplayKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
