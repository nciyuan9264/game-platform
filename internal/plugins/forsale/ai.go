package forsale

import (
	"context"
	"errors"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

type auctionObservation struct {
	PlayerID        string
	PlayerCount     int
	Round           int
	TotalRounds     int
	Market          []int
	HighestBid      int
	Coins           int
	ExistingBid     int
	OwnedProperties []int
}

type saleObservation struct {
	PlayerID    string
	Round       int
	TotalRounds int
	Cheques     []int
	Properties  []int
}

func (p *Plugin) Decide(
	ctx context.Context,
	request game.AIRequest,
) (contracts.RoomCommand, error) {
	state, err := asState(request.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	select {
	case <-ctx.Done():
		return timeoutFallback(state, request.PlayerID)
	default:
	}
	switch state.Status {
	case StatusAuction:
		if state.CurrentPlayer != request.PlayerID {
			return contracts.RoomCommand{}, errors.New("AI is not the active bidder")
		}
		commandType, amount := decideAuction(auctionView(state, request.PlayerID))
		command := contracts.RoomCommand{
			Type:     commandType,
			PlayerID: request.PlayerID,
		}
		if commandType == CommandBid {
			command.Payload = map[string]any{"amount": amount}
		} else {
			command.Payload = map[string]any{}
		}
		return command, nil
	case StatusSellSelect:
		player := state.Roster[request.PlayerID]
		if player == nil || player.LockedProperty != 0 {
			return contracts.RoomCommand{}, errors.New("AI cannot lock a property")
		}
		property := decideSale(saleView(state, request.PlayerID))
		return contracts.RoomCommand{
			Type:     CommandLockSale,
			PlayerID: request.PlayerID,
			Payload:  map[string]any{"property": property},
		}, nil
	default:
		return contracts.RoomCommand{}, errors.New("AI has no action in this phase")
	}
}

func auctionView(state *State, playerID string) auctionObservation {
	player := state.Roster[playerID]
	return auctionObservation{
		PlayerID:        playerID,
		PlayerCount:     len(state.PlayerOrder),
		Round:           state.Round,
		TotalRounds:     state.TotalRounds,
		Market:          append([]int(nil), state.PropertyMarket...),
		HighestBid:      state.HighestBid,
		Coins:           player.Coins,
		ExistingBid:     player.Bid,
		OwnedProperties: append([]int(nil), player.Properties...),
	}
}

func saleView(state *State, playerID string) saleObservation {
	player := state.Roster[playerID]
	return saleObservation{
		PlayerID:    playerID,
		Round:       state.Round,
		TotalRounds: state.TotalRounds,
		Cheques:     append([]int(nil), state.ChequeMarket...),
		Properties:  append([]int(nil), player.Properties...),
	}
}

func decideAuction(view auctionObservation) (string, int) {
	if len(view.Market) == 0 {
		return CommandPass, 0
	}
	market := append([]int(nil), view.Market...)
	sort.Ints(market)
	available := view.Coins + view.ExistingBid
	minimum := view.HighestBid + 1
	if available < minimum {
		return CommandPass, 0
	}
	remainingRounds := view.TotalRounds - view.Round + 1
	if remainingRounds < 1 {
		remainingRounds = 1
	}
	highest := market[len(market)-1]
	lowest := market[0]
	spread := highest - lowest
	budgetPerRound := available / remainingRounds
	target := highest/view.PlayerCount + spread/4
	if view.Round <= view.TotalRounds/2 {
		target = minInt(target, budgetPerRound+1)
	} else {
		target = minInt(target+1, budgetPerRound+2)
	}
	if target < 1 && highest >= 20 {
		target = 1
	}
	target = minInt(target, available)
	if minimum <= target {
		return CommandBid, minimum
	}
	return CommandPass, 0
}

func decideSale(view saleObservation) int {
	properties := append([]int(nil), view.Properties...)
	sort.Ints(properties)
	if len(properties) == 0 {
		return 0
	}
	cheques := append([]int(nil), view.Cheques...)
	sort.Ints(cheques)
	if len(cheques) == 0 {
		return properties[0]
	}
	low, high := cheques[0], cheques[len(cheques)-1]
	spread := high - low
	index := (high * (len(properties) - 1)) / 15
	if spread <= 3 && len(properties) > 2 {
		index = (len(properties) - 1) / 2
	}
	if high >= 13 {
		index = maxInt(index, len(properties)-2)
	}
	if index < 0 {
		index = 0
	}
	if index >= len(properties) {
		index = len(properties) - 1
	}
	return properties[index]
}

func timeoutFallback(state *State, playerID string) (contracts.RoomCommand, error) {
	switch state.Status {
	case StatusAuction:
		if state.CurrentPlayer != playerID {
			return contracts.RoomCommand{}, errors.New("timeout player is no longer active")
		}
		return contracts.RoomCommand{
			Type: CommandPass, PlayerID: playerID, Payload: map[string]any{},
		}, nil
	case StatusSellSelect:
		player := state.Roster[playerID]
		if player == nil || player.LockedProperty != 0 || len(player.Properties) == 0 {
			return contracts.RoomCommand{}, errors.New("timeout player cannot lock a property")
		}
		properties := append([]int(nil), player.Properties...)
		sort.Ints(properties)
		return contracts.RoomCommand{
			Type: CommandLockSale, PlayerID: playerID,
			Payload: map[string]any{"property": properties[0]},
		}, nil
	default:
		return contracts.RoomCommand{}, errors.New("timeout has no fallback")
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
