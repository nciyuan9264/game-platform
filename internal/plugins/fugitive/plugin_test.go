package fugitive

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestBaseDeckAndInitialDeal(t *testing.T) {
	state := startedState(t, RoleFugitive)
	fugitive := state.Roster[state.FugitivePlayerID]
	marshal := state.Roster[state.MarshalPlayerID]
	if len(fugitive.Hand) != 9 || len(marshal.Hand) != 0 {
		t.Fatalf(
			"initial hands = (%d, %d), want (9, 0)",
			len(fugitive.Hand),
			len(marshal.Hand),
		)
	}
	if got := []int{
		len(state.Decks[0]),
		len(state.Decks[1]),
		len(state.Decks[2]),
	}; !reflect.DeepEqual(got, []int{8, 12, 13}) {
		t.Fatalf("deck counts = %v", got)
	}
	for _, number := range []int{1, 2, 3, 42} {
		if !handContains(fugitive.Hand, number) {
			t.Fatalf("fugitive hand is missing %d", number)
		}
	}
	if err := validateCardConservation(state); err != nil {
		t.Fatal(err)
	}
}

func TestRoomSettingsKeepFixedFugitiveRules(t *testing.T) {
	plugin := &Plugin{}
	value, err := plugin.InitRoom(
		context.Background(),
		game.RoomConfig{
			RoomID:             "fixed-rules",
			OwnerPlayerID:      "owner",
			MaxPlayers:         2,
			RandomSeed:         42,
			Visibility:         contracts.VisibilityPublic,
			TurnTimeoutSeconds: 60,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := value.(*State)
	apply(t, plugin, state, "owner", "connect", nil)
	apply(t, plugin, state, "owner", "room_update_settings", map[string]any{
		"visibility":         contracts.VisibilityPrivate,
		"displayName":        "legacy client",
		"turnTimeoutSeconds": 0,
	})
	if state.Visibility != contracts.VisibilityPrivate {
		t.Fatalf("visibility = %q", state.Visibility)
	}
	if state.TurnTimeoutSeconds != defaultTurnTimeout {
		t.Fatalf("turn timeout = %d", state.TurnTimeoutSeconds)
	}
}

func TestOpeningAndFirstMarshalDraw(t *testing.T) {
	state := startedState(t, RoleFugitive)
	plugin := &Plugin{}
	apply(t, plugin, state, state.FugitivePlayerID, CommandPlayHideout,
		map[string]any{"hideout": 1, "sprints": []int{}})
	if state.Status != StatusOpening || state.OpeningHideouts != 1 {
		t.Fatalf(
			"opening state = (%s, %d)",
			state.Status,
			state.OpeningHideouts,
		)
	}
	apply(t, plugin, state, state.FugitivePlayerID, CommandFinishOpening, nil)
	if state.Status != StatusDraw ||
		state.CurrentPlayer != state.MarshalPlayerID ||
		state.DrawsRemaining != 2 {
		t.Fatalf(
			"marshal first turn = (%s, %s, %d)",
			state.Status,
			state.CurrentPlayer,
			state.DrawsRemaining,
		)
	}
}

func TestMoveValidationAndLayLow(t *testing.T) {
	state := &State{
		Route: []Hideout{{
			Card:     card(15),
			Revealed: false,
			Sprints:  []Card{},
		}},
	}
	player := &Player{Hand: []Card{card(12), card(18), card(20), card(42)}}
	if _, _, err := validateMove(state, player, 18, nil); err != nil {
		t.Fatalf("direct move rejected: %v", err)
	}
	if _, _, err := validateMove(state, player, 20, nil); err == nil {
		t.Fatal("move without required sprint was accepted")
	}
	if _, sprints, err := validateMove(
		state,
		player,
		20,
		[]int{12},
	); err != nil || len(sprints) != 1 {
		t.Fatalf("sprint move = %v, %v", sprints, err)
	}
	if _, _, err := validateMove(
		state,
		player,
		20,
		[]int{42},
	); err == nil {
		t.Fatal("42 was accepted as a sprint card")
	}
}

func TestMultiSearchIsAtomicAndManhuntMissEscapes(t *testing.T) {
	state := searchState()
	if err := searchHideouts(
		state,
		state.MarshalPlayerID,
		[]int{7, 11},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if state.Route[1].Revealed || state.Route[2].Revealed {
		t.Fatal("partial multi-search revealed a hideout")
	}

	state = searchState()
	state.Status = StatusManhunt
	if err := searchHideouts(
		state,
		state.MarshalPlayerID,
		[]int{11},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusEnd ||
		state.WinnerRole != RoleFugitive {
		t.Fatalf(
			"manhunt result = (%s, %s)",
			state.Status,
			state.WinnerRole,
		)
	}
}

func TestPlayerViewDoesNotLeakHiddenRoute(t *testing.T) {
	state := searchState()
	plugin := &Plugin{}
	view, err := plugin.BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{
			MatchID:  "match-1",
			PlayerID: state.MarshalPlayerID,
			Seq:      3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	root := view.(map[string]any)
	roomData := root["roomData"].(map[string]any)
	route := roomData["route"].([]map[string]any)
	if _, ok := route[1]["card"]; ok {
		t.Fatal("marshal view leaked hidden card")
	}
	if route[1]["sprintCount"] != 1 {
		t.Fatalf("public sprint count = %v", route[1]["sprintCount"])
	}

	view, err = plugin.BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{
			MatchID:  "match-1",
			PlayerID: state.FugitivePlayerID,
			Seq:      3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	root = view.(map[string]any)
	roomData = root["roomData"].(map[string]any)
	route = roomData["route"].([]map[string]any)
	if route[1]["card"].(Card).Number != 7 {
		t.Fatal("fugitive could not see own route")
	}
}

func TestFinishedViewDoesNotLeakPrivateDrawLog(t *testing.T) {
	state := searchState()
	state.Status = StatusEnd
	state.Log = []Action{{
		ID:              1,
		RoundNo:         2,
		Kind:            "draw",
		PlayerID:        state.FugitivePlayerID,
		Role:            RoleFugitive,
		DeckIndex:       intPointer(1),
		Numbers:         []int{12},
		PrivatePlayerID: state.FugitivePlayerID,
	}}
	plugin := &Plugin{}

	marshalView, err := plugin.BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{
			MatchID:  "match-1",
			PlayerID: state.MarshalPlayerID,
			Seq:      9,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	marshalLog := marshalView.(map[string]any)["roomData"].(map[string]any)["activityLog"].([]Action)[0]
	if len(marshalLog.Numbers) != 0 || marshalLog.Private {
		t.Fatalf("marshal log leaked private draw: %+v", marshalLog)
	}

	fugitiveView, err := plugin.BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{
			MatchID:  "match-1",
			PlayerID: state.FugitivePlayerID,
			Seq:      9,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fugitiveLog := fugitiveView.(map[string]any)["roomData"].(map[string]any)["activityLog"].([]Action)[0]
	if !reflect.DeepEqual(fugitiveLog.Numbers, []int{12}) ||
		!fugitiveLog.Private {
		t.Fatalf("fugitive log lost private draw: %+v", fugitiveLog)
	}
}

func TestMarshalAIUsesOnlyVisibleInformation(t *testing.T) {
	first := searchState()
	second := cloneState(first)
	second.Route[1].Card = card(8)
	second.Route[2].Card = card(10)

	left, err := (&Plugin{}).Decide(
		context.Background(),
		game.AIRequest{
			PlayerID: first.MarshalPlayerID,
			State:    first,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	right, err := (&Plugin{}).Decide(
		context.Background(),
		game.AIRequest{
			PlayerID: second.MarshalPlayerID,
			State:    second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("AI changed with hidden values: %#v != %#v", left, right)
	}
}

func intPointer(value int) *int {
	return &value
}

func startedState(t *testing.T, ownerRole Role) *State {
	t.Helper()
	plugin := &Plugin{}
	value, err := plugin.InitRoom(
		context.Background(),
		game.RoomConfig{
			RoomID:             "fugitive-test",
			OwnerPlayerID:      "owner",
			MaxPlayers:         2,
			RandomSeed:         42,
			RoundNo:            1,
			Visibility:         contracts.VisibilityPrivate,
			TurnTimeoutSeconds: 60,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := value.(*State)
	apply(t, plugin, state, "owner", "connect", nil)
	apply(t, plugin, state, "guest", "connect", nil)
	apply(t, plugin, state, "guest", "match_ready",
		map[string]any{"ready": true})
	apply(t, plugin, state, "owner", CommandSelectRole,
		map[string]any{"role": ownerRole})
	apply(t, plugin, state, "owner", "match_begin", nil)
	apply(t, plugin, state, "owner", "game_ready", nil)
	apply(t, plugin, state, "guest", "game_ready", nil)
	return state
}

func searchState() *State {
	state := &State{
		RoomID:             "search",
		OwnerID:            "runner",
		Status:             StatusMarshalSearch,
		MaxPlayers:         2,
		Visibility:         contracts.VisibilityPrivate,
		TurnTimeoutSeconds: 60,
		RandomSeed:         9,
		RoundNo:            4,
		Roster: map[string]*Player{
			"runner": {
				PlayerID: "runner",
				Role:     RoleFugitive,
				Online:   true,
				Hand:     []Card{card(12), card(42)},
			},
			"hunter": {
				PlayerID: "hunter",
				Role:     RoleMarshal,
				Online:   true,
				Hand:     []Card{card(4), card(5), card(9)},
			},
		},
		PlayerOrder:      []string{"runner", "hunter"},
		FugitivePlayerID: "runner",
		MarshalPlayerID:  "hunter",
		CurrentPlayer:    "hunter",
		Route: []Hideout{
			{Card: card(0), Revealed: true, Sprints: []Card{}},
			{Card: card(7), Revealed: false, Sprints: []Card{card(2)}},
			{Card: card(10), Revealed: false, Sprints: []Card{}},
		},
		Decks: [][]Card{{}, {}, {}},
		Log:   []Action{},
	}
	return state
}

func apply(
	t *testing.T,
	plugin *Plugin,
	state *State,
	playerID,
	commandType string,
	payload any,
) {
	t.Helper()
	_, err := plugin.ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			PlayerID: playerID,
			Type:     commandType,
			Payload:  payload,
			SentAt:   time.Unix(100, 0).UTC(),
		},
	)
	if err != nil {
		t.Fatalf("%s: %v", commandType, err)
	}
}

func handContains(hand []Card, number int) bool {
	for _, value := range hand {
		if value.Number == number {
			return true
		}
	}
	return false
}
