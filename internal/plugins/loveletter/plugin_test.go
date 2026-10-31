package loveletter

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestAuthoritativeViewHidesOtherHandsAndDeck(t *testing.T) {
	state := startPreparedMatch(t, 4)
	view, err := (&Plugin{}).BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{
			MatchID:  "loveletter-room-r1",
			PlayerID: "owner",
			Seq:      7,
			PlayerProfiles: map[string]game.PlayerProfile{
				"owner": {
					UserID:      "100",
					DisplayName: "Alice",
					Avatar:      "https://example.com/alice.png",
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := view.(map[string]any)
	privateData := payload["privateData"].(map[string]any)
	if len(privateData["hand"].([]Card)) != 1 {
		t.Fatalf("private data = %+v", privateData)
	}
	playerData := payload["playerData"].(map[string]any)
	for playerID, raw := range playerData {
		data := raw.(map[string]any)
		if _, leaked := data["hand"]; leaked {
			t.Fatalf("%s hand leaked: %+v", playerID, data)
		}
	}
	roomData := payload["roomData"].(map[string]any)
	if _, leaked := roomData["deck"]; leaked {
		t.Fatalf("deck order leaked: %+v", roomData)
	}
	players := roomData["players"].(map[string]any)
	owner := players["owner"].(map[string]any)
	if owner["displayName"] != "Alice" ||
		owner["avatar"] != "https://example.com/alice.png" {
		t.Fatalf("profile = %+v", owner)
	}
}

func TestHumanTimeoutRoundTransitionAndAIChain(t *testing.T) {
	state := startPreparedMatch(t, 2)
	state.CurrentPlayer = "owner"
	state.Status = StatusDraw
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	updateDeadlines(state, now)
	pending, err := (&Plugin{}).PendingCommands(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 ||
		pending[0].Type != CommandTimeout ||
		!pending[0].ExecuteAt.Equal(now.Add(turnTimeout)) {
		t.Fatalf("turn pending = %+v", pending)
	}
	if _, err := (&Plugin{}).ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type:     CommandTimeout,
			PlayerID: "owner",
			SentAt:   now.Add(turnTimeout),
			ConnectionRef: contracts.ConnectionRef{
				SessionID: "client", GatewayNode: "gateway",
			},
		},
	); err == nil {
		t.Fatal("client timeout was accepted")
	}
	if _, err := (&Plugin{}).ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			Type: CommandTimeout, PlayerID: "owner",
			SentAt: now.Add(turnTimeout),
		},
	); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusPlay ||
		state.LastAction == nil ||
		!state.LastAction.Automatic {
		t.Fatalf("timeout result = %s/%+v", state.Status, state.LastAction)
	}

	state.Status = StatusRoundEnd
	state.CurrentPlayer = ""
	state.RoundResult = &RoundResult{WinnerIDs: []string{"owner"}}
	state.RoundTransitionDeadline = now.Add(roundTransitionDelay)
	pending, err = (&Plugin{}).PendingCommands(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 ||
		pending[0].Type != CommandRoundTransition ||
		pending[0].PlayerID != "owner" {
		t.Fatalf("round pending = %+v", pending)
	}
}

func TestStateRoundTripAndDeterministicRecovery(t *testing.T) {
	first := startPreparedMatch(t, 4)
	second := startPreparedMatch(t, 4)
	if !reflect.DeepEqual(first.Deck, second.Deck) ||
		first.CurrentPlayer != second.CurrentPlayer {
		t.Fatal("same seed did not produce the same opening")
	}
	encoded, err := (&Plugin{}).EncodeState(context.Background(), first)
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
		t.Fatalf("round trip changed state:\n%s\n%s", encoded, reencoded)
	}
}

func TestAIUsesOnlyVisibleInformation(t *testing.T) {
	first := scenarioState(t, []int{1, 4}, []int{9}, []int{2}, []int{5})
	first.Roster["p0"].AI = true
	second := cloneState(first)
	second.Roster["p1"].Hand[0].Rank = 3
	second.Roster["p2"].Hand[0].Rank = 9

	left, err := (&Plugin{}).Decide(context.Background(), game.AIRequest{
		PlayerID: "p0", State: first,
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := (&Plugin{}).Decide(context.Background(), game.AIRequest{
		PlayerID: "p0", State: second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left.Payload, right.Payload) {
		t.Fatalf("hidden hands changed AI decision: %#v / %#v", left.Payload, right.Payload)
	}
}

func TestAIScheduleUsesReadableDelay(t *testing.T) {
	state := startPreparedMatch(t, 2)
	state.CurrentPlayer = "ai_001"
	state.Status = StatusDraw

	request := nextAIRequest(state)
	if request == nil {
		t.Fatal("AI request was not scheduled")
	}
	if request.Delay < 3*time.Second || request.Delay > 5*time.Second {
		t.Fatalf("AI delay = %s", request.Delay)
	}
}

func TestHistoryAndReplayExposeAllHandsAfterTheMatch(t *testing.T) {
	state := startPreparedMatch(t, 4)
	state.Roster["owner"].Favors = state.Goal
	state.Status = StatusEnd
	state.Result = &Result{
		WinnerIDs: []string{"owner"},
		Favors:    map[string]int{"owner": state.Goal},
		FinalRank: map[string]int{
			"owner": 1, "ai_001": 2, "ai_002": 2, "ai_003": 2,
		},
		RoundNo: state.RoundNo,
		Reason:  "favor",
	}
	projection, err := (&Plugin{}).ProjectHistory(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Finished ||
		projection.WinnerPlayerID != "owner" ||
		projection.ReplaySchemaVersion != ReplaySchemaVersion {
		t.Fatalf("projection = %+v", projection)
	}
	frame := projection.ReplayFrame.(map[string]any)
	privateData := frame["privateData"].(map[string]any)
	if len(privateData) != 4 {
		t.Fatalf("replay private data = %+v", privateData)
	}
	assertReplayFrameShape(t, frame)
}

func startPreparedMatch(t *testing.T, playerCount int) *State {
	t.Helper()
	plugin := &Plugin{}
	raw, err := plugin.InitRoom(context.Background(), game.RoomConfig{
		RoomID:        "loveletter-room",
		OwnerPlayerID: "owner",
		MaxPlayers:    6,
		RandomSeed:    42,
		RoundNo:       1,
		Visibility:    contracts.VisibilityPublic,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*State)
	commands := []contracts.RoomCommand{
		{Type: "connect", PlayerID: "owner"},
	}
	for seat := 1; seat < playerCount; seat++ {
		commands = append(commands, contracts.RoomCommand{
			Type: "match_add_ai", PlayerID: "owner",
			Payload: map[string]any{"seatIndex": seat},
		})
	}
	commands = append(commands,
		contracts.RoomCommand{Type: "match_begin", PlayerID: "owner"},
		contracts.RoomCommand{
			Type: "game_ready", PlayerID: "owner",
			SentAt: time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC),
		},
	)
	for _, command := range commands {
		if _, err := plugin.ApplyCommand(
			context.Background(),
			state,
			command,
		); err != nil {
			t.Fatalf("%s: %v", command.Type, err)
		}
	}
	return state
}

func assertReplayFrameShape(t *testing.T, frame map[string]any) {
	t.Helper()
	frameKeys := make([]string, 0, len(frame))
	for key := range frame {
		frameKeys = append(frameKeys, key)
	}
	sort.Strings(frameKeys)
	want := []string{
		"deck", "playersData", "privateData", "reserve", "result", "roomData",
	}
	if !reflect.DeepEqual(frameKeys, want) {
		t.Fatalf("frame keys = %v", frameKeys)
	}
}
