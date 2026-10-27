package sixnimmt

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

func TestHistoryProjectionAndReplayFrame(t *testing.T) {
	plugin, state := startedTestState(t, 3)
	projection, err := plugin.ProjectHistory(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Started || projection.Finished ||
		projection.ReplaySchemaVersion != ReplaySchemaVersion ||
		len(projection.Players) != 3 {
		t.Fatalf("projection = %+v", projection)
	}
	frame, ok := projection.ReplayFrame.(map[string]any)
	if !ok {
		t.Fatalf("replay frame = %T", projection.ReplayFrame)
	}
	assertReplayFrameGoldenShape(t, frame)
	privateData, ok := frame["privateData"].(map[string]any)
	if !ok || len(privateData) != 3 {
		t.Fatalf("private data = %+v", frame["privateData"])
	}
	snapshot, err := plugin.BuildReplaySnapshot(
		context.Background(),
		state,
		4,
		9,
		CommandLockCard,
		"p1",
		map[string]any{"card": state.Roster["p1"].Hand[0]},
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Seq          int            `json:"seq"`
		TotalEvents  int            `json:"totalEvents"`
		CurrentEvent map[string]any `json:"currentEvent"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Seq != 4 || decoded.TotalEvents != 9 ||
		decoded.CurrentEvent["cmdType"] != CommandLockCard {
		t.Fatalf("snapshot metadata = %+v", decoded)
	}
}

func assertReplayFrameGoldenShape(t *testing.T, frame map[string]any) {
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
	frameFields := sortedKeys(frame)
	roomData, ok := frame["roomData"].(map[string]any)
	if !ok {
		t.Fatalf("roomData = %T", frame["roomData"])
	}
	if fixture.SchemaVersion != ReplaySchemaVersion ||
		!reflect.DeepEqual(frameFields, fixture.FrameFields) ||
		!reflect.DeepEqual(sortedKeys(roomData), fixture.RoomDataFields) {
		t.Fatalf(
			"fixture mismatch: schema=%d frame=%v room=%v",
			fixture.SchemaVersion,
			frameFields,
			sortedKeys(roomData),
		)
	}
}

func sortedKeys(value map[string]any) []string {
	result := make([]string, 0, len(value))
	for key := range value {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
