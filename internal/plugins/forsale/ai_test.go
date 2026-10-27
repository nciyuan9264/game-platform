package forsale

import (
	"context"
	"reflect"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestAuctionObservationDoesNotReadOpponentSecrets(t *testing.T) {
	state := preparedState(t, 4, 201)
	playerID := state.CurrentPlayer
	before := auctionView(state, playerID)
	for _, otherID := range state.PlayerOrder {
		if otherID == playerID {
			continue
		}
		other := state.Roster[otherID]
		other.Coins = 999
		other.Properties = []int{1, 30}
		other.Cheques = []int{15, 15}
		other.LockedProperty = 27
	}
	after := auctionView(state, playerID)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("observation changed with opponent secrets:\n%+v\n%+v", before, after)
	}
}

func TestSaleObservationDoesNotReadOpponentSecrets(t *testing.T) {
	state := preparedState(t, 3, 202)
	state.Status = StatusSellSelect
	state.ChequeMarket = []int{0, 8, 15}
	playerID := state.PlayerOrder[0]
	state.Roster[playerID].Properties = []int{3, 14, 27}
	before := saleView(state, playerID)
	for _, otherID := range state.PlayerOrder[1:] {
		state.Roster[otherID].Properties = []int{1, 2, 30}
		state.Roster[otherID].Coins = 999
	}
	after := saleView(state, playerID)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("observation changed with opponent secrets")
	}
}

func TestAISelectsLegalStableActions(t *testing.T) {
	state := preparedState(t, 3, 203)
	playerID := state.CurrentPlayer
	plugin := &Plugin{}
	first, err := plugin.Decide(context.Background(), game.AIRequest{
		PlayerID: playerID,
		State:    cloneState(state),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := plugin.Decide(context.Background(), game.AIRequest{
		PlayerID: playerID,
		State:    cloneState(state),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("AI is not stable:\n%+v\n%+v", first, second)
	}
	if first.Type != CommandBid && first.Type != CommandPass {
		t.Fatalf("unexpected auction command %s", first.Type)
	}
}

func TestSaleAIUsesHighPropertyForHighChequeMarket(t *testing.T) {
	property := decideSale(saleObservation{
		Cheques: []int{0, 13, 15}, Properties: []int{2, 9, 18, 29},
	})
	if property != 29 && property != 18 {
		t.Fatalf("selected property = %d", property)
	}
}
