package onitama

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

func TestHistoryProjectionAndReplayFrame(t *testing.T) {
	state := startPreparedMatch(t, 9264, 1)
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
	for _, player := range projection.Players {
		if player.GameRole != string(SideBlue) &&
			player.GameRole != string(SideRed) {
			t.Fatalf("history role = %+v", player)
		}
	}
}

func TestFinishedHistoryProjectsWinnerAndPieceCounts(t *testing.T) {
	state := startPreparedMatch(t, 9264, 1)
	winnerSide := currentSide(state)
	winnerID := state.CurrentPlayer
	loserID := playerForSide(state, other(winnerSide))
	finish(state, winnerID, winnerSide, "resign")
	projection, err := (&Plugin{}).ProjectHistory(
		context.Background(),
		state,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Finished ||
		projection.WinnerPlayerID != winnerID {
		t.Fatalf("projection = %+v", projection)
	}
	for _, player := range projection.Players {
		if player.FinalScore == nil || player.FinalRank == nil {
			t.Fatalf("missing result = %+v", player)
		}
		if player.PlayerID == winnerID &&
			(!player.IsWinner || *player.FinalRank != 1) {
			t.Fatalf("winner = %+v", player)
		}
		if player.PlayerID == loserID &&
			(player.IsWinner || *player.FinalRank != 2) {
			t.Fatalf("loser = %+v", player)
		}
	}
}

func TestRecordableCommandsDefinitionAndReplayCodec(t *testing.T) {
	plugin := &Plugin{}
	for _, command := range []string{
		CommandMovePiece,
		CommandPass,
		CommandResign,
		CommandTimeout,
	} {
		if !plugin.IsRecordableCommand(command) {
			t.Fatalf("%s should be recordable", command)
		}
	}
	definition := Definition()
	if definition.Version != EngineVersion ||
		definition.History == nil ||
		definition.LegacyReplay == nil {
		t.Fatalf("definition = %+v", definition)
	}
	state := startPreparedMatch(t, 9264, 1)
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := plugin.DecodeReplayState(
		context.Background(),
		nil,
		nil,
		encoded,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, decoded) {
		t.Fatalf("replay codec changed state")
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
