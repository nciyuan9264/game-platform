package loveletter

import (
	"reflect"
	"testing"
)

func TestDeckSetupGoalsAndConservation(t *testing.T) {
	for players := 2; players <= 6; players++ {
		state := ruleState(t, players)
		if state.Goal != goalFor(players) {
			t.Fatalf("%d players goal = %d", players, state.Goal)
		}
		wantBurn := 0
		if players == 2 {
			wantBurn = 3
		}
		if len(state.Burn) != wantBurn || state.Reserve == nil {
			t.Fatalf("%d players burn/reserve = %d/%v", players, len(state.Burn), state.Reserve)
		}
		if len(state.Deck) != 20-wantBurn-players {
			t.Fatalf("%d players deck = %d", players, len(state.Deck))
		}
		if err := validateCardConservation(state); err != nil {
			t.Fatal(err)
		}
	}
	for rank, count := range roleCounts {
		found := 0
		for _, card := range freshDeck(1) {
			if card.Rank == rank {
				found++
			}
		}
		if found != count {
			t.Fatalf("rank %d count = %d, want %d", rank, found, count)
		}
	}
}

func TestCountessGuardHandmaidAndPrincessRules(t *testing.T) {
	state := scenarioState(t, []int{8, 7}, []int{5}, []int{3}, []int{4})
	if !forcedCountess(state, "p0") ||
		!reflect.DeepEqual(legalCardIDs(state, "p0"), []string{"c-8-0"}) {
		t.Fatalf("countess legality = %v", legalCardIDs(state, "p0"))
	}
	if err := playCard(state, "p0", "c-7-0", "p1", nil, false); err == nil {
		t.Fatal("king was accepted while countess was forced")
	}

	state = scenarioState(t, []int{1, 2}, []int{5}, []int{3}, []int{4})
	state.Roster["p2"].Protected = true
	if containsString(legalTargets(state, "p0", 1), "p2") {
		t.Fatal("protected target is legal")
	}
	if err := playCard(state, "p0", "c-1-0", "p1", intPointer(1), false); err == nil {
		t.Fatal("guard guessed guard")
	}
	if err := playCard(state, "p0", "c-1-0", "p1", intPointer(5), false); err != nil {
		t.Fatal(err)
	}
	if state.Roster["p1"].Alive {
		t.Fatal("guard did not eliminate the matching target")
	}

	state = scenarioState(t, []int{9, 1}, []int{5}, []int{3}, []int{4})
	if err := playCard(state, "p0", "c-9-0", "", nil, false); err != nil {
		t.Fatal(err)
	}
	if state.Roster["p0"].Alive || len(state.Roster["p0"].Hand) != 0 {
		t.Fatalf("princess player = %+v", state.Roster["p0"])
	}
	if err := validateCardConservation(state); err != nil {
		t.Fatal(err)
	}
}

func TestPriestBaronKingAndPrincePrivateEffects(t *testing.T) {
	state := scenarioState(t, []int{2, 4}, []int{9}, []int{3}, []int{5})
	if err := playCard(state, "p0", "c-2-0", "p1", nil, false); err != nil {
		t.Fatal(err)
	}
	effect := state.Roster["p0"].Private
	if effect == nil || effect.Kind != "priest" ||
		effect.TargetRank == nil || *effect.TargetRank != 9 {
		t.Fatalf("priest effect = %+v", effect)
	}
	for _, action := range state.Log {
		if action.Summary == "9" {
			t.Fatal("private rank leaked into public log")
		}
	}

	state = scenarioState(t, []int{3, 4}, []int{9}, []int{2}, []int{5})
	if err := playCard(state, "p0", "c-3-0", "p1", nil, false); err != nil {
		t.Fatal(err)
	}
	if state.Roster["p0"].Alive || !state.Roster["p1"].Alive {
		t.Fatalf("baron result = %v/%v", state.Roster["p0"].Alive, state.Roster["p1"].Alive)
	}

	state = scenarioState(t, []int{7, 4}, []int{9}, []int{2}, []int{5})
	if err := playCard(state, "p0", "c-7-0", "p1", nil, false); err != nil {
		t.Fatal(err)
	}
	if state.Roster["p0"].Hand[0].Rank != 9 || !state.Roster["p0"].Alive {
		t.Fatalf("king exchange = %+v", state.Roster["p0"])
	}

	state = scenarioState(t, []int{5, 4}, []int{9}, []int{2}, []int{3})
	if err := playCard(state, "p0", "c-5-0", "p1", nil, false); err != nil {
		t.Fatal(err)
	}
	if state.Roster["p1"].Alive || len(state.Roster["p1"].Hand) != 0 {
		t.Fatalf("prince princess result = %+v", state.Roster["p1"])
	}
}

func TestChancellorKeepsOneAndReturnsOrderedCards(t *testing.T) {
	state := scenarioState(t, []int{6, 4}, []int{5}, []int{3}, []int{8})
	princess := takeDeckRank(t, state, 9)
	guard := takeDeckRank(t, state, 1)
	princess.CardID = "extra-9"
	guard.CardID = "extra-1"
	state.Deck = append([]Card{princess, guard}, state.Deck...)
	if err := playCard(state, "p0", "c-6-0", "", nil, false); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusChancellor || len(state.Roster["p0"].Hand) != 3 {
		t.Fatalf("chancellor state = %s/%v", state.Status, state.Roster["p0"].Hand)
	}
	if err := settleChancellor(
		state,
		"p0",
		"extra-9",
		[]string{"c-4-0", "extra-1"},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if state.Roster["p0"].Hand[0].Rank != 9 {
		t.Fatalf("kept hand = %+v", state.Roster["p0"].Hand)
	}
	last := state.Deck[len(state.Deck)-2:]
	if last[0].CardID != "c-4-0" || last[1].CardID != "extra-1" {
		t.Fatalf("bottom order = %+v", last)
	}
	if err := validateCardConservation(state); err != nil {
		t.Fatal(err)
	}
}

func TestRoundScoringSpyTieAndMatchWinners(t *testing.T) {
	state := scenarioState(t, []int{4}, []int{6}, []int{6}, []int{3})
	state.Status = StatusPlay
	state.Roster["p0"].SpyUsed = true
	state.Roster["p0"].Favors = 3
	state.Roster["p1"].Favors = 3
	state.Roster["p2"].Favors = 3
	state.Deck = []Card{}
	finishRound(state)
	if !reflect.DeepEqual(state.RoundResult.WinnerIDs, []string{"p1", "p2"}) ||
		state.RoundResult.SpyPlayerID != "p0" {
		t.Fatalf("round result = %+v", state.RoundResult)
	}
	if state.Status != StatusEnd ||
		!reflect.DeepEqual(state.Result.WinnerIDs, []string{"p0", "p1", "p2"}) {
		t.Fatalf("match result = %+v", state.Result)
	}
	for _, winnerID := range state.Result.WinnerIDs {
		if state.Result.FinalRank[winnerID] != 1 {
			t.Fatalf("%s rank = %d", winnerID, state.Result.FinalRank[winnerID])
		}
	}
}

func ruleState(t *testing.T, playerCount int) *State {
	t.Helper()
	state := &State{
		Status:      StatusWaiting,
		RandomSeed:  42,
		MaxPlayers:  6,
		Visibility:  "public",
		Roster:      map[string]*Player{},
		PlayerOrder: []string{},
		Deck:        []Card{},
		Burn:        []Card{},
		Log:         []Action{},
		Goal:        goalFor(playerCount),
	}
	for index := 0; index < playerCount; index++ {
		playerID := "p" + string(rune('0'+index))
		state.PlayerOrder = append(state.PlayerOrder, playerID)
		state.Roster[playerID] = &Player{
			PlayerID: playerID, SeatIndex: index, Alive: true,
			Hand: []Card{}, Discards: []Card{},
			KnownCards: map[string]KnownCard{},
		}
	}
	if err := startRound(state, "p0"); err != nil {
		t.Fatal(err)
	}
	return state
}

func takeDeckRank(t *testing.T, state *State, rank int) Card {
	t.Helper()
	for index, card := range state.Deck {
		if card.Rank == rank {
			state.Deck = append(state.Deck[:index], state.Deck[index+1:]...)
			return card
		}
	}
	t.Fatalf("deck is missing rank %d", rank)
	return Card{}
}

func scenarioState(t *testing.T, hands ...[]int) *State {
	t.Helper()
	state := ruleState(t, len(hands))
	all := freshDeck(state.RoundNo)
	take := func(rank int) Card {
		for index, card := range all {
			if card.Rank == rank {
				all = append(all[:index], all[index+1:]...)
				return card
			}
		}
		t.Fatalf("missing rank %d", rank)
		return Card{}
	}
	for playerIndex, ranks := range hands {
		playerID := state.PlayerOrder[playerIndex]
		state.Roster[playerID].Hand = []Card{}
		rankCopies := map[int]int{}
		for _, rank := range ranks {
			card := take(rank)
			copyIndex := rankCopies[rank]
			rankCopies[rank]++
			card.CardID = "c-" + string(rune('0'+rank)) + "-" + string(rune('0'+copyIndex))
			state.Roster[playerID].Hand = append(state.Roster[playerID].Hand, card)
		}
		state.Roster[playerID].Discards = []Card{}
	}
	state.Reserve = nil
	state.Burn = []Card{}
	state.Deck = all
	state.Status = StatusPlay
	state.CurrentPlayer = "p0"
	state.Goal = goalFor(len(hands))
	return state
}
