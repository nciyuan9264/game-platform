package warchest

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestRecommendedSetupStartsWithConservedInventory(t *testing.T) {
	state := startedTestState(t)
	if state.Status != StatusPlaying ||
		state.CurrentPlayer != state.InitialPlayerID ||
		state.InitiativePlayer != state.InitialPlayerID {
		t.Fatalf("unexpected start state: %+v", state)
	}
	if state.InitialSide != SideOak {
		t.Fatalf("initial side = %q", state.InitialSide)
	}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if len(player.Hand) != 3 || len(player.Bag) != 6 {
			t.Fatalf(
				"player %s hand/bag = %d/%d",
				playerID,
				len(player.Hand),
				len(player.Bag),
			)
		}
		if controlledLocations(state, player.Side) != 2 {
			t.Fatalf("player %s starting controls are invalid", playerID)
		}
	}
	if err := validateCoinConservation(state); err != nil {
		t.Fatal(err)
	}
}

func TestRandomAndDraftSetupAreDeterministic(t *testing.T) {
	first := lobbyTestState(t, 2)
	second := lobbyTestState(t, 2)
	for _, state := range []*State{first, second} {
		applyTestCommand(t, state, state.OwnerID, CommandSetSetupMode, map[string]any{
			"mode": SetupRandom,
		})
	}
	if !reflect.DeepEqual(
		setupSnapshot(first),
		setupSnapshot(second),
	) {
		t.Fatalf("random setup is not deterministic")
	}

	draft := lobbyTestState(t, 1)
	applyTestCommand(t, draft, draft.OwnerID, CommandSetSetupMode, map[string]any{
		"mode": SetupDraft,
	})
	if draft.Draft == nil ||
		len(draft.Draft.Pool) != 8 ||
		len(availableDraftUnits(draft.Draft)) != 8 {
		t.Fatalf("invalid draft: %+v", draft.Draft)
	}
	expectedOrder := draftPickOrder(draft.Draft)
	for index := 0; index < 8; index++ {
		playerID := draft.Draft.CurrentPickerID
		if playerID != expectedOrder[index] {
			t.Fatalf(
				"pick %d player = %q, want %q",
				index,
				playerID,
				expectedOrder[index],
			)
		}
		unit := availableDraftUnits(draft.Draft)[0]
		applyTestCommand(t, draft, playerID, CommandDraftUnit, map[string]any{
			"unitType": unit,
		})
	}
	if !draft.Draft.Complete || !setupComplete(draft) {
		t.Fatalf("draft did not complete: %+v", draft.Draft)
	}
	if draft.InitialPlayerID != otherPlayerID(
		draft,
		draft.Draft.FirstPickerID,
	) {
		t.Fatalf("draft initial player is invalid")
	}
}

func TestRematchSwapsRecommendedSides(t *testing.T) {
	odd := lobbyTestState(t, 1)
	even := lobbyTestState(t, 2)
	for _, playerID := range odd.PlayerOrder {
		if odd.Roster[playerID].Side == even.Roster[playerID].Side {
			t.Fatalf("player %s did not swap sides", playerID)
		}
		if reflect.DeepEqual(
			odd.Roster[playerID].Roster,
			even.Roster[playerID].Roster,
		) {
			t.Fatalf("player %s did not swap recommended roster", playerID)
		}
	}
}

func TestNextMatchPreservesSetupModeAndRegeneratesSetup(t *testing.T) {
	plugin := &Plugin{}
	previous := lobbyTestState(t, 1)
	applyTestCommand(t, previous, previous.OwnerID, CommandSetSetupMode, map[string]any{
		"mode": SetupDraft,
	})
	nextAny, err := plugin.InitRoom(
		context.Background(),
		game.RoomConfig{
			RoomID:        previous.RoomID,
			OwnerPlayerID: previous.OwnerID,
			MaxPlayers:    2,
			RandomSeed:    99,
			RoundNo:       2,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	next := nextAny.(*State)
	if err := plugin.PrepareNextMatch(
		context.Background(),
		previous,
		next,
	); err != nil {
		t.Fatal(err)
	}
	applyTestCommand(t, next, "alice", "connect", nil)
	applyTestCommand(t, next, "bob", "connect", nil)
	if next.SetupMode != SetupDraft ||
		next.Draft == nil ||
		reflect.DeepEqual(previous.Draft.Pool, next.Draft.Pool) {
		t.Fatalf(
			"next setup = mode %s draft %+v",
			next.SetupMode,
			next.Draft,
		)
	}
	if previous.Roster["alice"].Side == next.Roster["alice"].Side {
		t.Fatal("next match did not swap sides")
	}
}

func TestPlayerViewHidesOpponentPrivateCoins(t *testing.T) {
	state := startedTestState(t)
	viewAny, err := (&Plugin{}).BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{
			RoomID:   state.RoomID,
			MatchID:  "room-r1",
			PlayerID: state.PlayerOrder[0],
			Seq:      7,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	view := viewAny.(map[string]any)
	privateData := view["privateData"].(map[string]any)
	if len(privateData["hand"].([]map[string]any)) != 3 {
		t.Fatalf("own hand missing: %#v", privateData)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	opponent := state.Roster[state.PlayerOrder[1]]
	for _, coinID := range append(
		append([]string{}, opponent.Hand...),
		opponent.Bag...,
	) {
		if stringContains(string(encoded), coinID) {
			t.Fatalf("opponent hidden coin %q leaked", coinID)
		}
	}
}

func TestActionIDRejectsStaleAction(t *testing.T) {
	state := startedTestState(t)
	playerID := state.CurrentPlayer
	actions := legalActions(state, playerID)
	first := findAction(t, actions, ActionPass, func(LegalAction) bool {
		return true
	})
	applyAction(t, state, playerID, first)
	payload, _ := json.Marshal(map[string]any{"actionID": first.ActionID})
	if err := playAction(
		state,
		state.CurrentPlayer,
		payload,
		false,
		time.Now().UTC(),
	); err == nil {
		t.Fatal("stale action ID was accepted")
	}
}

func TestEncodeDecodePreservesLegalActions(t *testing.T) {
	plugin := &Plugin{}
	state := startedTestState(t)
	before := legalActions(state, state.CurrentPlayer)
	encoded, err := plugin.EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	decodedAny, err := plugin.DecodeState(context.Background(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodedAny.(*State)
	after := legalActions(decoded, decoded.CurrentPlayer)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("legal actions changed after restore")
	}
}

func TestCoinConservationRejectsWrongOwner(t *testing.T) {
	state := startedTestState(t)
	playerID := state.PlayerOrder[0]
	coinID := state.Roster[playerID].Hand[0]
	state.CoinOwners[coinID] = state.PlayerOrder[1]
	if err := validateCoinConservation(state); err == nil {
		t.Fatal("coin owner corruption was accepted")
	}
}

func TestTurnDeadlineIsFixedAndReconnectDoesNotExtendIt(t *testing.T) {
	state := startedTestState(t)
	want := time.Date(2026, 10, 26, 12, 1, 30, 0, time.UTC)
	if !state.TurnDeadline.Equal(want) ||
		state.TurnTimeout != 90*time.Second {
		t.Fatalf(
			"deadline = %s timeout = %s",
			state.TurnDeadline,
			state.TurnTimeout,
		)
	}
	applyTestCommandAt(
		t,
		state,
		state.CurrentPlayer,
		"disconnect",
		nil,
		want.Add(-70*time.Second),
	)
	applyTestCommandAt(
		t,
		state,
		state.CurrentPlayer,
		"connect",
		nil,
		want.Add(-60*time.Second),
	)
	if !state.TurnDeadline.Equal(want) {
		t.Fatalf("reconnect extended deadline to %s", state.TurnDeadline)
	}
	commands, err := (&Plugin{}).PendingCommands(
		context.Background(),
		state,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 ||
		commands[0].Type != CommandTimeout ||
		!commands[0].ExecuteAt.Equal(want) {
		t.Fatalf("scheduled commands = %+v", commands)
	}
}

func TestOrdinaryTimeoutDiscardsStableFirstCoinFaceDown(t *testing.T) {
	state := startedTestState(t)
	playerID := state.CurrentPlayer
	firstCoinID := state.Roster[playerID].Hand[0]
	applyTestCommandAt(
		t,
		state,
		playerID,
		CommandTimeout,
		nil,
		state.TurnDeadline,
	)
	if state.ActionCounter != 1 ||
		state.LastAction == nil ||
		state.LastAction.Action != ActionPass ||
		state.LastAction.CoinID != firstCoinID ||
		!state.LastAction.Automatic ||
		state.LastAction.PrivateFor != playerID {
		t.Fatalf("timeout action = %+v", state.LastAction)
	}
}

func TestDraftTimeoutUsesFairPicker(t *testing.T) {
	state := lobbyTestState(t, 1)
	applyTestCommand(t, state, state.OwnerID, CommandSetSetupMode, map[string]any{
		"mode": SetupDraft,
	})
	playerID := state.Draft.CurrentPickerID
	available := availableDraftUnits(state.Draft)
	applyTestCommandAt(
		t,
		state,
		playerID,
		CommandTimeout,
		nil,
		state.Draft.TurnDeadline,
	)
	if state.Draft.CompletedPickCount != 1 ||
		len(state.Draft.Rosters[playerID]) != 1 ||
		!containsUnit(available, state.Draft.Rosters[playerID][0]) {
		t.Fatalf("draft timeout result = %+v", state.Draft)
	}
}

func lobbyTestState(t *testing.T, roundNo int) *State {
	t.Helper()
	plugin := &Plugin{}
	value, err := plugin.InitRoom(context.Background(), game.RoomConfig{
		RoomID:        "warchest-test",
		OwnerPlayerID: "alice",
		MaxPlayers:    2,
		RandomSeed:    4815162342,
		RoundNo:       roundNo,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := value.(*State)
	applyTestCommand(t, state, "alice", "connect", nil)
	applyTestCommand(t, state, "bob", "connect", nil)
	applyTestCommand(t, state, "bob", "match_ready", map[string]any{
		"ready": true,
	})
	return state
}

func startedTestState(t *testing.T) *State {
	t.Helper()
	state := lobbyTestState(t, 1)
	applyTestCommand(t, state, state.OwnerID, "match_begin", nil)
	for _, playerID := range state.PlayerOrder {
		applyTestCommand(t, state, playerID, "game_ready", nil)
	}
	return state
}

func applyTestCommand(
	t *testing.T,
	state *State,
	playerID,
	commandType string,
	payload any,
) {
	t.Helper()
	applyTestCommandAt(
		t,
		state,
		playerID,
		commandType,
		payload,
		time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
	)
}

func applyTestCommandAt(
	t *testing.T,
	state *State,
	playerID,
	commandType string,
	payload any,
	sentAt time.Time,
) {
	t.Helper()
	_, err := (&Plugin{}).ApplyCommand(
		context.Background(),
		state,
		contracts.RoomCommand{
			PlayerID: playerID,
			Type:     commandType,
			Payload:  payload,
			SentAt:   sentAt,
		},
	)
	if err != nil {
		t.Fatalf("%s by %s: %v", commandType, playerID, err)
	}
}

func applyAction(
	t *testing.T,
	state *State,
	playerID string,
	action LegalAction,
) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"actionID": action.ActionID})
	if err := playAction(
		state,
		playerID,
		data,
		false,
		time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatal(err)
	}
}

func findAction(
	t *testing.T,
	actions []LegalAction,
	actionType ActionType,
	match func(LegalAction) bool,
) LegalAction {
	t.Helper()
	for _, action := range actions {
		if action.Action == actionType && match(action) {
			return action
		}
	}
	t.Fatalf("missing action %s in %+v", actionType, actions)
	return LegalAction{}
}

func setupSnapshot(state *State) any {
	rosters := map[string][]UnitType{}
	for _, playerID := range state.PlayerOrder {
		rosters[playerID] = copyUnits(state.Roster[playerID].Roster)
	}
	return struct {
		Rosters map[string][]UnitType
		Initial string
		Side    Side
	}{
		Rosters: rosters,
		Initial: state.InitialPlayerID,
		Side:    state.InitialSide,
	}
}

func stringContains(value, target string) bool {
	for index := 0; index+len(target) <= len(value); index++ {
		if value[index:index+len(target)] == target {
			return true
		}
	}
	return false
}
