package forsale

import (
	"fmt"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestPlayerCountConfiguration(t *testing.T) {
	tests := []struct {
		players int
		coins   int
		removed int
		rounds  int
	}{
		{players: 3, coins: 28, removed: 6, rounds: 8},
		{players: 4, coins: 21, removed: 2, rounds: 7},
		{players: 5, coins: 16, removed: 0, rounds: 6},
		{players: 6, coins: 14, removed: 0, rounds: 5},
	}
	for _, tt := range tests {
		coins, ok := startingCoins(tt.players)
		if !ok || coins != tt.coins {
			t.Fatalf("startingCoins(%d) = %d, %v", tt.players, coins, ok)
		}
		removed, ok := removedCardCount(tt.players)
		if !ok || removed != tt.removed {
			t.Fatalf("removedCardCount(%d) = %d, %v", tt.players, removed, ok)
		}
		rounds, ok := roundCount(tt.players)
		if !ok || rounds != tt.rounds {
			t.Fatalf("roundCount(%d) = %d, %v", tt.players, rounds, ok)
		}
	}
}

func TestRefundRoundsUp(t *testing.T) {
	for bid, want := range map[int]int{
		0: 0,
		1: 1,
		2: 1,
		3: 2,
		4: 2,
		5: 3,
	} {
		if got := refund(bid); got != want {
			t.Fatalf("refund(%d) = %d, want %d", bid, got, want)
		}
	}
}

func TestBidPassAndWinnerSettlement(t *testing.T) {
	state := preparedState(t, 3, 77)
	first := state.CurrentPlayer
	if err := applyBid(state, first, 3, SourceManual); err != nil {
		t.Fatal(err)
	}
	if state.Roster[first].Coins != 25 {
		t.Fatalf("coins after bid = %d", state.Roster[first].Coins)
	}
	second := state.CurrentPlayer
	if err := applyBid(state, second, 4, SourceManual); err != nil {
		t.Fatal(err)
	}
	third := state.CurrentPlayer
	if err := applyPass(state, third, SourceManual); err != nil {
		t.Fatal(err)
	}
	if state.Roster[third].RoundCost != 0 {
		t.Fatalf("zero bid pass cost = %d", state.Roster[third].RoundCost)
	}
	if err := applyPass(state, first, SourceManual); err != nil {
		t.Fatal(err)
	}
	if state.Roster[first].RoundCost != 1 {
		t.Fatalf("odd bid actual cost = %d, want 1", state.Roster[first].RoundCost)
	}
	if state.Status != StatusBuyResult || len(state.Purchases) != 1 {
		t.Fatalf("state = %s, purchases = %d", state.Status, len(state.Purchases))
	}
	if state.StarterPlayer != second || state.Roster[second].RoundCost != 4 {
		t.Fatalf("winner = %s cost=%d", state.StarterPlayer, state.Roster[second].RoundCost)
	}
	if err := validateState(state); err != nil {
		t.Fatal(err)
	}
}

func TestSalePairsSortedPropertiesAndCheques(t *testing.T) {
	state := preparedState(t, 3, 91)
	state.Status = StatusSellSelect
	state.PropertyMarket = []int{}
	state.PropertyDeck = []int{}
	state.RemovedProperties = propertyDeck()
	state.ChequeDeck = []int{}
	state.RemovedCheques = chequeDeck()
	state.ChequeMarket = []int{0, 7, 15}
	for index, playerID := range state.PlayerOrder {
		property := []int{4, 21, 12}[index]
		state.Roster[playerID].Properties = []int{property}
		state.RemovedProperties = removeInt(state.RemovedProperties, property)
	}
	for _, cheque := range state.ChequeMarket {
		state.RemovedCheques = removeInt(state.RemovedCheques, cheque)
	}
	for _, playerID := range state.PlayerOrder {
		property := state.Roster[playerID].Properties[0]
		if err := lockSale(state, playerID, property, SourceManual); err != nil {
			t.Fatal(err)
		}
	}
	if state.Status != StatusSellLocked {
		t.Fatalf("state = %s", state.Status)
	}
	if err := revealSale(state, SourceSystem); err != nil {
		t.Fatal(err)
	}
	if got := state.Roster[state.PlayerOrder[0]].Cheques[0]; got != 0 {
		t.Fatalf("property 4 cheque = %d", got)
	}
	if got := state.Roster[state.PlayerOrder[2]].Cheques[0]; got != 7 {
		t.Fatalf("property 12 cheque = %d", got)
	}
	if got := state.Roster[state.PlayerOrder[1]].Cheques[0]; got != 15 {
		t.Fatalf("property 21 cheque = %d", got)
	}
}

func TestRankingUsesCoinsThenSharesWins(t *testing.T) {
	state := preparedState(t, 3, 12)
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		player.Properties = []int{}
		player.Cheques = []int{10}
		player.Coins = 5
	}
	state.Roster[state.PlayerOrder[2]].Coins = 4
	state.Roster[state.PlayerOrder[2]].Cheques = []int{11}
	result := rankPlayers(state)
	if result[state.PlayerOrder[0]].Rank != 1 ||
		result[state.PlayerOrder[1]].Rank != 1 ||
		result[state.PlayerOrder[2]].Rank != 3 {
		t.Fatalf("ranks = %+v", result)
	}
	if !result[state.PlayerOrder[0]].Winner ||
		!result[state.PlayerOrder[1]].Winner ||
		result[state.PlayerOrder[2]].Winner {
		t.Fatalf("winners = %+v", result)
	}
}

func TestCompleteGamesForEveryPlayerCount(t *testing.T) {
	for players := 3; players <= 6; players++ {
		t.Run(fmt.Sprintf("%d-players", players), func(t *testing.T) {
			for seed := uint64(1); seed <= 20; seed++ {
				state := preparedState(t, players, seed+100)
				guard := 0
				for state.Status != StatusEnd && guard < 2000 {
					guard++
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
								if err := lockSale(state, playerID, player.Properties[0], SourceAI); err != nil {
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
					default:
						t.Fatalf("unexpected state %s", state.Status)
					}
					if err := validateState(state); err != nil {
						t.Fatalf("seed %d step %d: %v", seed, guard, err)
					}
				}
				if state.Status != StatusEnd {
					t.Fatalf("seed %d did not finish", seed)
				}
				rounds, _ := roundCount(players)
				for _, playerID := range state.PlayerOrder {
					player := state.Roster[playerID]
					if player.Coins < 0 || len(player.Properties) != 0 ||
						len(player.Cheques) != rounds {
						t.Fatalf("player %s = %+v", playerID, player)
					}
				}
			}
		})
	}
}

func preparedState(t *testing.T, players int, seed uint64) *State {
	t.Helper()
	state := &State{
		RoomID: "room", OwnerID: "p0", Status: StatusMatch,
		MaxPlayers: maxPlayers, Visibility: contracts.VisibilityPrivate,
		RandomSeed: seed, MatchRoundNo: 1,
		Roster: map[string]*Player{}, PlayerOrder: []string{},
		PropertyDeck: []int{}, ChequeDeck: []int{},
		RemovedProperties: []int{}, RemovedCheques: []int{},
		PropertyMarket: []int{}, ChequeMarket: []int{},
		Purchases: []RoundRecord{}, Sales: []RoundRecord{},
		ActionLog: []Action{}, Result: map[string]PlayerResult{},
	}
	for index := 0; index < players; index++ {
		playerID := fmt.Sprintf("p%d", index)
		state.Roster[playerID] = &Player{
			PlayerID: playerID, Online: true, Ready: true, GameReady: true,
			AI: true, SeatIndex: index, Properties: []int{}, Cheques: []int{},
		}
		state.PlayerOrder = append(state.PlayerOrder, playerID)
	}
	if err := prepareMatch(state); err != nil {
		t.Fatal(err)
	}
	state.Status = StatusAuction
	if err := validateState(state); err != nil {
		t.Fatal(err)
	}
	return state
}
