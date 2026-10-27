package hex

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

func TestHistoryProjectionAndReplayShape(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	projection, err := (&Plugin{}).ProjectHistory(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Started || projection.Finished ||
		projection.ReplaySchemaVersion != ReplaySchemaVersion ||
		len(projection.Players) != 2 ||
		len(projection.StateBlob) == 0 {
		t.Fatalf("projection = %+v", projection)
	}
	assertReplayFrameMatchesGolden(t, projection.ReplayFrame)

	if _, err := (&Plugin{}).BuildReplaySnapshot(
		context.Background(),
		state,
		1,
		3,
		CommandPlaceStone,
		"owner",
		map[string]any{"cell": Cell{Row: 5, Column: 5}},
	); err != nil {
		t.Fatal(err)
	}
}

func TestSwapHistoryUsesFinalSides(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	if err := placeStone(state, "owner", Cell{Row: 5, Column: 5}, false); err != nil {
		t.Fatal(err)
	}
	if err := swapSides(state, "ai_2", true); err != nil {
		t.Fatal(err)
	}
	if err := resign(state, "owner"); err != nil {
		t.Fatal(err)
	}
	projection, err := (&Plugin{}).ProjectHistory(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Finished || projection.WinnerPlayerID != "ai_2" ||
		projection.Players[0].GameRole != string(SideTeal) ||
		projection.Players[1].GameRole != string(SideRed) {
		t.Fatalf("projection = %+v", projection)
	}
}

func assertReplayFrameMatchesGolden(t *testing.T, value any) {
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
	frameFields := sortedKeys(frame)
	roomFields := sortedKeys(roomData)
	sort.Strings(fixture.FrameFields)
	sort.Strings(fixture.RoomDataFields)
	if fixture.SchemaVersion != ReplaySchemaVersion ||
		!reflect.DeepEqual(frameFields, fixture.FrameFields) ||
		!reflect.DeepEqual(roomFields, fixture.RoomDataFields) {
		t.Fatalf(
			"replay shape = %v/%v, fixture = %v/%v",
			frameFields,
			roomFields,
			fixture.FrameFields,
			fixture.RoomDataFields,
		)
	}
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
