package forsale

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestHistoryProjectionAndReplayFrame(t *testing.T) {
	state := preparedState(t, 3, 401)
	state.GameStartTime = time.Unix(100, 0).UTC()
	projection, err := (&Plugin{}).ProjectHistory(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Started || projection.Finished ||
		projection.ReplaySchemaVersion != ReplaySchemaVersion ||
		len(projection.Players) != 3 ||
		len(projection.StateBlob) == 0 {
		t.Fatalf("projection = %+v", projection)
	}
	assertReplayFrameMatchesGolden(t, projection.ReplayFrame)
}

func TestTiedWinnersDoNotCreateSingleWinnerIdentity(t *testing.T) {
	state := preparedState(t, 3, 402)
	state.GameStartTime = time.Unix(100, 0).UTC()
	finishTestGame(t, state)
	for _, playerID := range state.PlayerOrder {
		state.Result[playerID] = PlayerResult{
			ChequeTotal: 10,
			Coins:       5,
			Total:       15,
			Rank:        1,
			Winner:      true,
		}
	}
	projection, err := (&Plugin{}).ProjectHistory(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Finished || projection.WinnerPlayerID != "" ||
		projection.WinnerUserID != "" {
		t.Fatalf("projection winner = %q/%q",
			projection.WinnerPlayerID, projection.WinnerUserID)
	}
	for _, player := range projection.Players {
		if !player.IsWinner || player.FinalRank == nil ||
			*player.FinalRank != 1 {
			t.Fatalf("player = %+v", player)
		}
	}
}

func finishTestGame(t *testing.T, state *State) {
	t.Helper()
	for guard := 0; state.Status != StatusEnd && guard < 2000; guard++ {
		switch state.Status {
		case StatusAuction:
			if err := applyPass(state, state.CurrentPlayer, SourceAI); err != nil {
				t.Fatal(err)
			}
		case StatusBuyResult:
			if err := advanceBuyRound(state); err != nil {
				t.Fatal(err)
			}
		case StatusBridge:
			if err := startSelling(state); err != nil {
				t.Fatal(err)
			}
		case StatusSellSelect:
			for _, playerID := range state.PlayerOrder {
				player := state.Roster[playerID]
				if player.LockedProperty == 0 {
					if err := lockSale(
						state,
						playerID,
						player.Properties[0],
						SourceAI,
					); err != nil {
						t.Fatal(err)
					}
				}
			}
		case StatusSellLocked:
			if err := revealSale(state, SourceSystem); err != nil {
				t.Fatal(err)
			}
		case StatusSellReveal:
			if err := advanceSaleRound(state); err != nil {
				t.Fatal(err)
			}
		}
	}
	if state.Status != StatusEnd {
		t.Fatal("test game did not finish")
	}
}

func TestRecordableCommandsAndReplaySnapshot(t *testing.T) {
	plugin := &Plugin{}
	for _, command := range []string{
		CommandBid, CommandPass, CommandLockSale, CommandRevealSale,
		CommandNextRound, CommandStartSelling, CommandTurnTimeout,
		CommandPhaseTimeout,
	} {
		if !plugin.IsRecordableCommand(command) {
			t.Fatalf("%s should be recordable", command)
		}
	}
	if plugin.IsRecordableCommand("connect") {
		t.Fatal("connect should not be recordable")
	}
	state := preparedState(t, 3, 403)
	snapshot, err := plugin.BuildReplaySnapshot(
		context.Background(), state, 1, 4,
		CommandPass, state.CurrentPlayer, map[string]any{},
	)
	if err != nil {
		t.Fatal(err)
	}
	frame := snapshot.(map[string]any)
	if frame["seq"] != 1 || frame["totalEvents"] != 4 ||
		frame["currentEvent"] == nil {
		t.Fatalf("snapshot = %+v", frame)
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
		t.Fatalf("frame fields = %v/%v, fixture = %v/%v",
			frameFields, roomFields, fixture.FrameFields,
			fixture.RoomDataFields)
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
