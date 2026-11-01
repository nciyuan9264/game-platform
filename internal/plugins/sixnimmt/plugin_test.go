package sixnimmt

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestPlayerViewHidesOtherHandsAndLockedCards(t *testing.T) {
	plugin, state := startedTestState(t, 2)
	p1 := state.Roster["p1"]
	p2 := state.Roster["p2"]
	card := p1.Hand[0]
	if err := lockCard(state, "p1", card, ""); err != nil {
		t.Fatal(err)
	}
	view, err := plugin.BuildPlayerView(
		context.Background(),
		state,
		game.ViewRequest{
			MatchID:  "room-r1",
			PlayerID: "p2",
			Seq:      7,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := view.(map[string]any)
	privateData := payload["privateData"].(map[string]any)
	if !reflect.DeepEqual(privateData["hand"], p2.Hand) {
		t.Fatalf("private hand = %v, want %v", privateData["hand"], p2.Hand)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" {
		t.Fatal("view is empty")
	}
	if privateData["lockedCard"] != nil {
		t.Fatalf("viewer received another player's locked card: %v", privateData)
	}
	playerData := payload["playerData"].(map[string]any)
	if !playerData["p1"].(map[string]any)["locked"].(bool) {
		t.Fatal("public locked state is missing")
	}
}

func TestOwnerUserCanStartWithRuntimePlayerID(t *testing.T) {
	plugin := &Plugin{}
	stateRaw, err := plugin.InitRoom(context.Background(), game.RoomConfig{
		RoomID: "room", OwnerUserID: "owner-user", OwnerPlayerID: "owner-user",
		MaxPlayers: defaultMaxPlayers, RandomSeed: 43, RoundNo: 1,
		Visibility: contracts.VisibilityPublic,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := stateRaw.(*State)
	for _, command := range []contracts.RoomCommand{
		{Type: "connect", UserID: "owner-user", PlayerID: "runtime-owner"},
		{Type: "match_add_ai", UserID: "owner-user", PlayerID: "runtime-owner", Payload: map[string]any{"seatIndex": 1}},
		{Type: "match_begin", UserID: "owner-user", PlayerID: "runtime-owner", SentAt: time.Unix(100, 0)},
	} {
		if _, err := plugin.ApplyCommand(context.Background(), state, command); err != nil {
			t.Fatalf("%s: %v", command.Type, err)
		}
	}
	if state.Status != StatusWaiting {
		t.Fatalf("status = %q, want %q", state.Status, StatusWaiting)
	}
}

func TestStateCodecPreservesPartialResolution(t *testing.T) {
	plugin, state := startedTestState(t, 2)
	state.Rows = [][]int{{12}, {30}, {61}, {79}}
	state.Roster["p1"].Hand = []int{7}
	state.Roster["p2"].Hand = []int{24}
	state.UnusedCards = remainingCards(state)
	if err := lockCard(state, "p1", 7, ""); err != nil {
		t.Fatal(err)
	}
	if err := lockCard(state, "p2", 24, ""); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusRevealing {
		t.Fatalf("status = %s, want revealing", state.Status)
	}
	if err := resolveRevealedSelections(state); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusChooseRow {
		t.Fatalf("status = %s, want chooseRow", state.Status)
	}
	encoded, err := plugin.EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := plugin.DecodeState(context.Background(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.(*State)
	if got.QueueCursor != state.QueueCursor ||
		!reflect.DeepEqual(got.PendingChoice, state.PendingChoice) ||
		!reflect.DeepEqual(got.Rows, state.Rows) {
		t.Fatalf("decoded partial resolution differs: %+v", got)
	}
}

func TestRevealPhaseWaitsFifteenSecondsBeforeResolving(t *testing.T) {
	plugin, state := startedTestState(t, 2)
	state.Rows = [][]int{{12}, {30}, {61}, {79}}
	state.Roster["p1"].Hand = []int{80}
	state.Roster["p2"].Hand = []int{90}
	state.UnusedCards = remainingCards(state)
	commandTime := time.Unix(1_800_000_000, 0).UTC()

	for _, command := range []contracts.RoomCommand{
		{
			Type:     CommandLockCard,
			PlayerID: "p1",
			Payload:  map[string]any{"card": 80},
			SentAt:   commandTime.Add(-time.Second),
		},
		{
			Type:     CommandLockCard,
			PlayerID: "p2",
			Payload:  map[string]any{"card": 90},
			SentAt:   commandTime,
		},
	} {
		if _, err := plugin.ApplyCommand(context.Background(), state, command); err != nil {
			t.Fatalf("%s: %v", command.PlayerID, err)
		}
	}

	if state.Status != StatusRevealing {
		t.Fatalf("status = %s, want revealing", state.Status)
	}
	if got, want := state.RevealDeadline, commandTime.Add(15*time.Second); !got.Equal(want) {
		t.Fatalf("reveal deadline = %s, want %s", got, want)
	}
	if !reflect.DeepEqual(state.Rows, [][]int{{12}, {30}, {61}, {79}}) {
		t.Fatalf("rows changed before reveal deadline: %v", state.Rows)
	}
	if state.LastResolution == nil ||
		!reflect.DeepEqual(state.LastResolution.Selections, []Selection{
			{PlayerID: "p1", Card: 80},
			{PlayerID: "p2", Card: 90},
		}) {
		t.Fatalf("revealed selections = %+v", state.LastResolution)
	}

	pending, err := plugin.PendingCommands(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 ||
		pending[0].Type != CommandRevealTransition ||
		!pending[0].ExecuteAt.Equal(state.RevealDeadline) {
		t.Fatalf("pending reveal command = %+v", pending)
	}

	if _, err := plugin.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type:     CommandRevealTransition,
		PlayerID: state.OwnerID,
		SentAt:   state.RevealDeadline,
		ConnectionRef: contracts.ConnectionRef{
			GatewayNode: "gateway",
			SessionID:   "session",
		},
	}); err == nil {
		t.Fatal("client submitted reveal transition was accepted")
	}

	if _, err := plugin.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type:     CommandRevealTransition,
		PlayerID: state.OwnerID,
		SentAt:   state.RevealDeadline,
	}); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusSelecting || state.TurnNo != 2 {
		t.Fatalf("state = %s turn %d, want selecting turn 2", state.Status, state.TurnNo)
	}
	if !state.RevealDeadline.IsZero() {
		t.Fatalf("reveal deadline was not cleared: %s", state.RevealDeadline)
	}
	if !reflect.DeepEqual(state.Rows, [][]int{{12}, {30}, {61}, {79, 80, 90}}) {
		t.Fatalf("resolved rows = %v", state.Rows)
	}
}

func TestAIChoiceIgnoresOtherHiddenInformation(t *testing.T) {
	_, state := startedTestState(t, 3)
	state.Roster["p1"].AI = true
	first, err := bestCard(aiObservationFor(state, "p1"))
	if err != nil {
		t.Fatal(err)
	}
	state.Roster["p2"].Hand[0], state.Roster["p3"].Hand[0] =
		state.Roster["p3"].Hand[0], state.Roster["p2"].Hand[0]
	state.UnusedCards[0], state.UnusedCards[1] =
		state.UnusedCards[1], state.UnusedCards[0]
	second, err := bestCard(aiObservationFor(state, "p1"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("AI choice changed from %d to %d", first, second)
	}
}

func startedTestState(t *testing.T, players int) (*Plugin, *State) {
	t.Helper()
	plugin := &Plugin{}
	raw, err := plugin.InitRoom(context.Background(), game.RoomConfig{
		RoomID:        "room",
		OwnerPlayerID: "p1",
		MaxPlayers:    defaultMaxPlayers,
		RandomSeed:    42,
		Visibility:    contracts.VisibilityPrivate,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*State)
	for index := 0; index < players; index++ {
		playerID := "p" + string(rune('1'+index))
		if err := connect(state, playerID); err != nil {
			t.Fatal(err)
		}
		state.Roster[playerID].Ready = true
		state.Roster[playerID].GameReady = true
	}
	state.GameStartTime = time.Now().UTC()
	if err := setupRound(state, 1); err != nil {
		t.Fatal(err)
	}
	return plugin, state
}

func remainingCards(state *State) []int {
	used := map[int]bool{}
	for _, row := range state.Rows {
		for _, card := range row {
			used[card] = true
		}
	}
	for _, player := range state.Roster {
		for _, card := range player.Hand {
			used[card] = true
		}
		for _, card := range player.Taken {
			used[card] = true
		}
	}
	for _, selection := range state.Queue {
		used[selection.Card] = true
	}
	result := []int{}
	for card := 1; card <= cardCount; card++ {
		if !used[card] {
			result = append(result, card)
		}
	}
	return result
}
