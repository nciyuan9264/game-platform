package sixnimmt

import (
	"reflect"
	"testing"
)

func TestBullheads(t *testing.T) {
	cases := map[int]int{23: 1, 25: 2, 30: 3, 44: 5, 55: 7}
	for card, want := range cases {
		if got := bullheads(card); got != want {
			t.Fatalf("bullheads(%d) = %d, want %d", card, got, want)
		}
	}
}

func TestTargetRowUsesClosestLowerTail(t *testing.T) {
	rows := [][]int{{12, 18, 23}, {30, 44}, {61, 64}, {79, 90}}
	if got := targetRow(rows, 68); got != 2 {
		t.Fatalf("targetRow(68) = %d, want 2", got)
	}
	if got := targetRow(rows, 7); got != -1 {
		t.Fatalf("targetRow(7) = %d, want -1", got)
	}
}

func TestSixthCardCollectsOriginalFive(t *testing.T) {
	state := testPlayingState(2)
	state.Rows = [][]int{
		{2},
		{30, 35, 39, 41, 44},
		{61},
		{79},
	}
	state.Queue = []Selection{{
		PlayerID:       "p1",
		Card:           46,
		AutomationKind: AutomationTimeout,
	}}
	state.QueueCursor = 0
	state.LastResolution = &TurnResolution{
		ResolutionID: "test",
		RoundNo:      1,
		TurnNo:       1,
		RowsBefore:   rowViews(state.Rows),
		Selections:   append([]Selection(nil), state.Queue...),
	}
	if err := resolveQueue(state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.Rows[1], []int{46}) {
		t.Fatalf("row = %v, want [46]", state.Rows[1])
	}
	if state.Roster["p1"].RoundPenalty != 12 {
		t.Fatalf(
			"round penalty = %d, want 12",
			state.Roster["p1"].RoundPenalty,
		)
	}
}

func TestLowCardWaitsForHumanChoice(t *testing.T) {
	state := testPlayingState(2)
	state.Rows = [][]int{{12}, {30}, {61}, {79}}
	state.Queue = []Selection{{PlayerID: "p1", Card: 7}}
	state.LastResolution = &TurnResolution{
		ResolutionID: "test",
		RoundNo:      1,
		TurnNo:       1,
		RowsBefore:   rowViews(state.Rows),
		Selections:   append([]Selection(nil), state.Queue...),
	}
	if err := resolveQueue(state); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusChooseRow || state.PendingChoice == nil {
		t.Fatalf("state = %s/%+v, want chooseRow", state.Status, state.PendingChoice)
	}
	if err := chooseRow(state, "p1", 0, false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.Rows[0], []int{7}) {
		t.Fatalf("row = %v, want [7]", state.Rows[0])
	}
	if state.Roster["p1"].RoundPenalty != 1 {
		t.Fatalf("round penalty = %d, want 1", state.Roster["p1"].RoundPenalty)
	}
}

func TestCompleteTenTurnsConservesCards(t *testing.T) {
	for _, players := range []int{2, 4, 10} {
		t.Run(string(rune('0'+players))+" players", func(t *testing.T) {
			completeTenTurns(t, players)
		})
	}
}

func completeTenTurns(t *testing.T, players int) {
	t.Helper()
	state := testPlayingState(players)
	if err := setupRound(state, 1); err != nil {
		t.Fatal(err)
	}
	for turn := 1; turn <= cardsPerPlayer; turn++ {
		for _, playerID := range state.PlayerOrder {
			card := state.Roster[playerID].Hand[0]
			if err := lockCard(
				state,
				playerID,
				card,
				AutomationTimeout,
			); err != nil {
				t.Fatalf("turn %d player %s: %v", turn, playerID, err)
			}
		}
		if err := validateCardConservation(state); err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
	}
	if state.Status != StatusRoundEnd && state.Status != StatusEnd {
		t.Fatalf("status = %s, want roundEnd or end", state.Status)
	}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if len(player.Hand) != 0 ||
			player.RoundPenalty != rowPenalty(player.Taken) {
			t.Fatalf("player %s = %+v", playerID, player)
		}
	}
}

func TestFinishRoundUsesCompetitionRanksAndSharedWinners(t *testing.T) {
	state := testPlayingState(3)
	state.TurnNo = cardsPerPlayer
	state.Roster["p1"].TotalPenalty = targetPenalty
	state.Roster["p2"].TotalPenalty = 5
	state.Roster["p3"].TotalPenalty = 5
	if err := finishRound(state); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusEnd || state.Result == nil {
		t.Fatalf("state = %s/%+v", state.Status, state.Result)
	}
	if !reflect.DeepEqual(state.Result.WinnerIDs, []string{"p2", "p3"}) ||
		state.Result.FinalRanks["p2"] != 1 ||
		state.Result.FinalRanks["p3"] != 1 ||
		state.Result.FinalRanks["p1"] != 3 {
		t.Fatalf("result = %+v", state.Result)
	}
}

func testPlayingState(players int) *State {
	state := &State{
		RoomID:      "room",
		OwnerID:     "p1",
		Status:      StatusSelecting,
		MaxPlayers:  defaultMaxPlayers,
		RandomSeed:  42,
		RoundNo:     1,
		TurnNo:      1,
		Roster:      map[string]*Player{},
		PlayerOrder: []string{},
		Rows:        [][]int{{1}, {2}, {3}, {4}},
		UnusedCards: []int{},
		Queue:       []Selection{},
		Log:         []Action{},
	}
	for index := 0; index < players; index++ {
		playerID := "p" + string(rune('1'+index))
		state.PlayerOrder = append(state.PlayerOrder, playerID)
		state.Roster[playerID] = &Player{
			PlayerID:  playerID,
			SeatIndex: index,
			Online:    true,
			Ready:     true,
			Hand:      []int{},
			Taken:     []int{},
		}
	}
	return state
}
