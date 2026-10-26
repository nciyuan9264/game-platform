package quoridor

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

func TestHistoryProjectionAndReplayFrame(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	projection, err := (&Plugin{}).ProjectHistory(
		context.Background(),
		state,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Started ||
		projection.Finished ||
		projection.ReplaySchemaVersion != ReplaySchemaVersion ||
		len(projection.Players) != 2 ||
		len(projection.StateBlob) == 0 {
		t.Fatalf("projection = %+v", projection)
	}
	assertReplayFrameMatchesGolden(t, projection.ReplayFrame)

	encoded, err := (&Plugin{}).EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	decodedRaw, err := (&Plugin{}).DecodeState(
		context.Background(),
		encoded,
	)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := json.Marshal(decodedRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(encoded, reencoded) {
		t.Fatalf("state round trip changed:\n%s\n%s", encoded, reencoded)
	}
}

func TestFinishedHistoryProjectsWinnerRolesAndWallScores(t *testing.T) {
	state := startPreparedMatch(t, 40, 1)
	state.Pawns[SideSouth] = Cell{Row: 1, Column: 3}
	if err := movePawn(
		state,
		"owner",
		Cell{Row: 0, Column: 3},
		false,
	); err != nil {
		t.Fatal(err)
	}
	projection, err := (&Plugin{}).ProjectHistory(
		context.Background(),
		state,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Finished ||
		!projection.AbandonedEligible ||
		projection.WinnerPlayerID != "owner" ||
		len(projection.Players) != 2 {
		t.Fatalf("projection = %+v", projection)
	}
	owner := projection.Players[0]
	opponent := projection.Players[1]
	if !owner.IsWinner ||
		owner.FinalRank == nil || *owner.FinalRank != 1 ||
		owner.FinalScore == nil || *owner.FinalScore != 10 ||
		owner.GameRole != string(SideSouth) ||
		opponent.FinalRank == nil || *opponent.FinalRank != 2 ||
		opponent.GameRole != string(SideNorth) {
		t.Fatalf("players = %+v", projection.Players)
	}
}

func TestRecordableCommandsAndReplaySnapshot(t *testing.T) {
	plugin := &Plugin{}
	for _, command := range []string{
		CommandMovePawn,
		CommandPlaceWall,
		CommandResign,
		CommandTimeout,
	} {
		if !plugin.IsRecordableCommand(command) {
			t.Fatalf("%s should be recordable", command)
		}
	}
	for _, command := range []string{
		"connect",
		"disconnect",
		"match_begin",
		"game_ready",
	} {
		if plugin.IsRecordableCommand(command) {
			t.Fatalf("%s should not be recordable", command)
		}
	}
	state := startPreparedMatch(t, 40, 1)
	snapshot, err := plugin.BuildReplaySnapshot(
		context.Background(),
		state,
		1,
		4,
		CommandMovePawn,
		"owner",
		map[string]any{"to": Cell{Row: 7, Column: 4}},
	)
	if err != nil {
		t.Fatal(err)
	}
	frame := snapshot.(map[string]any)
	if frame["seq"] != 1 ||
		frame["totalEvents"] != 4 ||
		frame["currentEvent"] == nil {
		t.Fatalf("snapshot = %+v", frame)
	}
	definition := Definition()
	if definition.Version != EngineVersion ||
		definition.History == nil ||
		definition.LegacyReplay == nil {
		t.Fatalf("definition = %+v", definition)
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
			"replay frame shape = %v/%v, fixture = %v/%v",
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
