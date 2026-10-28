package cantstop

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestLifecycleAndViews(t *testing.T) {
	state := startPreparedMatch(t)
	if state.Status != StatusRoll || state.CurrentPlayer == "" {
		t.Fatalf("state = %+v", state)
	}
	viewAny, err := (&Plugin{}).BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{PlayerID: "owner", MatchID: "match", Seq: 3},
	)
	if err != nil {
		t.Fatal(err)
	}
	view := viewAny.(map[string]any)
	room := view["roomData"].(map[string]any)
	if room["gameStatus"] != StatusRoll ||
		len(room["playerOrder"].([]string)) != 2 {
		t.Fatalf("room view = %#v", room)
	}
}

func TestRejectedCommandIsAtomic(t *testing.T) {
	state := startPreparedMatch(t)
	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&Plugin{}).ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{Type: CommandAdvance, PlayerID: "owner", Payload: map[string]any{"actionID": "fake"}},
	)
	if err == nil {
		t.Fatal("fake advance was accepted")
	}
	after, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state changed after rejection:\n%s\n%s", before, after)
	}
}

func TestRollAdvanceBankFlow(t *testing.T) {
	state := startPreparedMatch(t)
	active := state.CurrentPlayer
	_, err := (&Plugin{}).ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{Type: CommandRollDice, PlayerID: active},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusChoose || len(state.PairingOptions) == 0 {
		t.Fatalf("after roll = %+v", state)
	}
	actionID := state.PairingOptions[0].ActionID
	_, err = (&Plugin{}).ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{Type: CommandAdvance, PlayerID: active, Payload: map[string]any{"actionID": actionID}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusDecide || turnGain(state) <= 0 {
		t.Fatalf("after advance = %+v", state)
	}
	_, err = (&Plugin{}).ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{Type: CommandBank, PlayerID: active},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusBanked {
		t.Fatalf("after bank = %+v", state)
	}
}

func TestTimeoutAndAI(t *testing.T) {
	state := startPreparedMatch(t)
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	armAutomation(state, now)
	pending, err := (&Plugin{}).PendingCommands(context.Background(), state)
	if err != nil || len(pending) != 1 ||
		!pending[0].ExecuteAt.Equal(now.Add(turnTimeout)) {
		t.Fatalf("pending = %+v err=%v", pending, err)
	}
	_, err = (&Plugin{}).ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type: CommandTimeout, PlayerID: state.CurrentPlayer,
			SentAt: now.Add(turnTimeout),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastAction == nil || state.LastAction.Source != SourceTimeout {
		t.Fatalf("last action = %+v", state.LastAction)
	}
}

func TestAIRequestsUseThreeToFiveSecondPacing(t *testing.T) {
	if aiMinDelay != 3*time.Second || aiMaxDelay != 5*time.Second {
		t.Fatalf("AI delay range = %s-%s", aiMinDelay, aiMaxDelay)
	}
	state := startPreparedMatch(t)
	playerID := state.CurrentPlayer
	state.Roster[playerID].AI = true
	assertDelay := func(label string, request *game.AIRequest) {
		t.Helper()
		if request == nil {
			t.Fatalf("%s AI request is nil", label)
		}
		if request.Delay < aiMinDelay || request.Delay > aiMaxDelay {
			t.Fatalf("%s delay = %s", label, request.Delay)
		}
	}

	assertDelay(StatusRoll, nextAIRequest(state))
	plugin := &Plugin{}
	result, err := plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{Type: CommandRollDice, PlayerID: playerID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AIRequests) != 1 {
		t.Fatalf("%s AI requests = %+v", StatusChoose, result.AIRequests)
	}
	assertDelay(StatusChoose, &result.AIRequests[0])

	result, err = plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type:     CommandAdvance,
			PlayerID: playerID,
			Payload: map[string]any{
				"actionID": state.PairingOptions[0].ActionID,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AIRequests) != 1 {
		t.Fatalf("%s AI requests = %+v", StatusDecide, result.AIRequests)
	}
	assertDelay(StatusDecide, &result.AIRequests[0])
}

func startPreparedMatch(t *testing.T) *State {
	t.Helper()
	raw, err := (&Plugin{}).InitRoom(context.Background(), game.RoomConfig{
		RoomID: "cantstop-room", OwnerPlayerID: "owner",
		MaxPlayers: defaultMaxPlayers, RandomSeed: 40, RoundNo: 1,
		Visibility: contracts.VisibilityPublic,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*State)
	commands := []contracts.RoomCommand{
		{Type: "connect", UserID: "owner", PlayerID: "owner"},
		{
			Type: "match_add_ai", PlayerID: "owner",
			Payload: map[string]any{"seatIndex": 1},
		},
		{Type: "match_begin", PlayerID: "owner"},
		{
			Type: "game_ready", PlayerID: "owner",
			SentAt: time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC),
		},
	}
	for _, command := range commands {
		if _, err := (&Plugin{}).ApplyCommand(
			context.Background(),
			state,
			command,
		); err != nil {
			t.Fatalf("%s: %v", command.Type, err)
		}
	}
	return state
}
