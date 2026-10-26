package yinsh

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

func TestHistoryProjectionAndReplayFrame(t *testing.T) {
	state := startPreparedMatch(t, 1)
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

	encoded, err := (&Plugin{}).EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	decodedRaw, err := (&Plugin{}).DecodeState(context.Background(), encoded)
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

func TestFinishedHistoryProjectsWinnerScoresAndRanks(t *testing.T) {
	state := startPreparedMatch(t, 1)
	state.Status = StatusEnd
	state.WinnerID = "owner"
	state.EndReason = "test win"
	state.RingsRemoved[SideWhite] = 3
	state.RingsRemoved[SideBlack] = 1
	projection, err := (&Plugin{}).ProjectHistory(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Finished || projection.WinnerPlayerID != "owner" {
		t.Fatalf("projection = %+v", projection)
	}
	if len(projection.Players) != 2 ||
		!projection.Players[0].IsWinner ||
		projection.Players[0].FinalScore == nil ||
		*projection.Players[0].FinalScore != 3 ||
		projection.Players[0].FinalRank == nil ||
		*projection.Players[0].FinalRank != 1 ||
		projection.Players[1].FinalRank == nil ||
		*projection.Players[1].FinalRank != 2 {
		t.Fatalf("history players = %+v", projection.Players)
	}
}

func TestRecordableCommands(t *testing.T) {
	plugin := &Plugin{}
	for _, command := range []string{
		CommandPlaceRing,
		CommandMoveRing,
		CommandRemoveRow,
		CommandRemoveRing,
		CommandResign,
		CommandTimeout,
	} {
		if !plugin.IsRecordableCommand(command) {
			t.Fatalf("%s should be recordable", command)
		}
	}
	for _, command := range []string{"connect", "disconnect", "match_begin", "game_ready"} {
		if plugin.IsRecordableCommand(command) {
			t.Fatalf("%s should not be recordable", command)
		}
	}
	if definition := Definition(); definition.Version != EngineVersion ||
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
