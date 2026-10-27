package forsale

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	minPlayers = 3
	maxPlayers = 6
)

func startingCoins(players int) (int, bool) {
	switch players {
	case 3:
		return 28, true
	case 4:
		return 21, true
	case 5:
		return 16, true
	case 6:
		return 14, true
	default:
		return 0, false
	}
}

func removedCardCount(players int) (int, bool) {
	switch players {
	case 3:
		return 6, true
	case 4:
		return 2, true
	case 5, 6:
		return 0, true
	default:
		return 0, false
	}
}

func roundCount(players int) (int, bool) {
	removed, ok := removedCardCount(players)
	if !ok {
		return 0, false
	}
	return (30 - removed) / players, true
}

func refund(bid int) int {
	if bid <= 0 {
		return 0
	}
	return (bid + 1) / 2
}

func chequeDeck() []int {
	result := []int{0, 0}
	for value := 2; value <= 15; value++ {
		result = append(result, value, value)
	}
	return result
}

func propertyDeck() []int {
	result := make([]int, 30)
	for index := range result {
		result[index] = index + 1
	}
	return result
}

func shuffle(values []int, state *State) {
	for index := len(values) - 1; index > 0; index-- {
		next := nextRandom(state)
		target := int(next % uint64(index+1))
		values[index], values[target] = values[target], values[index]
	}
}

func nextRandom(state *State) uint64 {
	value := state.RandomSeed + state.RandomCounter*0x9e3779b97f4a7c15
	state.RandomCounter++
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func prepareMatch(state *State) error {
	playerCount := len(state.PlayerOrder)
	coins, ok := startingCoins(playerCount)
	if !ok {
		return errors.New("for sale requires three to six players")
	}
	rounds, _ := roundCount(playerCount)
	removed, _ := removedCardCount(playerCount)
	properties := propertyDeck()
	cheques := chequeDeck()
	shuffle(properties, state)
	shuffle(cheques, state)
	state.RemovedProperties = append([]int(nil), properties[:removed]...)
	state.PropertyDeck = append([]int(nil), properties[removed:]...)
	state.RemovedCheques = append([]int(nil), cheques[:removed]...)
	state.ChequeDeck = append([]int(nil), cheques[removed:]...)
	state.TotalRounds = rounds
	state.Round = 1
	state.Purchases = []RoundRecord{}
	state.Sales = []RoundRecord{}
	state.ActionLog = []Action{}
	state.LastAction = nil
	state.ActionCounter = 0
	state.Result = map[string]PlayerResult{}
	state.EndReason = ""
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		player.Coins = coins
		player.Bid = 0
		player.Passed = false
		player.Properties = []int{}
		player.Cheques = []int{}
		player.LockedProperty = 0
		player.WonProperty = 0
		player.RoundCost = 0
		player.ActionDeadline = zeroTime()
	}
	starterIndex := int(nextRandom(state) % uint64(playerCount))
	state.StarterPlayer = state.PlayerOrder[starterIndex]
	if err := drawPropertyMarket(state); err != nil {
		return err
	}
	resetAuction(state)
	return nil
}

func drawPropertyMarket(state *State) error {
	count := len(state.PlayerOrder)
	if count == 0 || len(state.PropertyDeck) < count {
		return errors.New("property deck is exhausted")
	}
	state.PropertyMarket = append([]int(nil), state.PropertyDeck[:count]...)
	state.PropertyDeck = append([]int(nil), state.PropertyDeck[count:]...)
	sort.Ints(state.PropertyMarket)
	return nil
}

func drawChequeMarket(state *State) error {
	count := len(state.PlayerOrder)
	if count == 0 || len(state.ChequeDeck) < count {
		return errors.New("cheque deck is exhausted")
	}
	state.ChequeMarket = append([]int(nil), state.ChequeDeck[:count]...)
	state.ChequeDeck = append([]int(nil), state.ChequeDeck[count:]...)
	sort.Ints(state.ChequeMarket)
	return nil
}

func resetAuction(state *State) {
	state.Status = StatusAuction
	state.HighestBid = 0
	state.CurrentPlayer = state.StarterPlayer
	state.ChequeMarket = []int{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		player.Bid = 0
		player.Passed = false
		player.WonProperty = 0
		player.RoundCost = 0
		player.LockedProperty = 0
		player.ActionDeadline = zeroTime()
	}
	clearTransitionDeadline(state)
}

func applyBid(state *State, playerID string, amount int, source string) error {
	if state.Status != StatusAuction || state.CurrentPlayer != playerID {
		return errors.New("it is not this player's auction turn")
	}
	player := state.Roster[playerID]
	if player == nil || player.Passed {
		return errors.New("player cannot bid")
	}
	maximum := player.Coins + player.Bid
	if amount <= state.HighestBid || amount > maximum {
		return errors.New("bid must exceed the current bid and available coins")
	}
	delta := amount - player.Bid
	player.Coins -= delta
	player.Bid = amount
	state.HighestBid = amount
	appendAction(state, Action{
		Round: state.Round, Phase: StatusAuction, Kind: "bid",
		PlayerID: playerID, Source: source, Amount: intPointer(amount),
	})
	state.CurrentPlayer = nextActivePlayer(state, playerID)
	return nil
}

func applyPass(state *State, playerID, source string) error {
	if state.Status != StatusAuction || state.CurrentPlayer != playerID {
		return errors.New("it is not this player's auction turn")
	}
	player := state.Roster[playerID]
	if player == nil || player.Passed || len(state.PropertyMarket) == 0 {
		return errors.New("player cannot pass")
	}
	property := state.PropertyMarket[0]
	state.PropertyMarket = append([]int(nil), state.PropertyMarket[1:]...)
	returned := refund(player.Bid)
	player.Coins += returned
	player.RoundCost = player.Bid - returned
	player.WonProperty = property
	player.Passed = true
	player.Properties = append(player.Properties, property)
	sort.Ints(player.Properties)
	appendAction(state, Action{
		Round: state.Round, Phase: StatusAuction, Kind: "pass",
		PlayerID: playerID, Source: source, Property: intPointer(property),
		Paid: intPointer(player.RoundCost),
	})
	active := activePlayerIDs(state)
	if len(active) > 1 {
		state.CurrentPlayer = nextActivePlayer(state, playerID)
		return nil
	}
	if len(active) != 1 || len(state.PropertyMarket) != 1 {
		return errors.New("auction state is inconsistent")
	}
	winnerID := active[0]
	winner := state.Roster[winnerID]
	winner.WonProperty = state.PropertyMarket[0]
	winner.RoundCost = winner.Bid
	winner.Properties = append(winner.Properties, winner.WonProperty)
	sort.Ints(winner.Properties)
	appendAction(state, Action{
		Round: state.Round, Phase: StatusAuction, Kind: "winAuction",
		PlayerID: winnerID, Source: SourceSystem,
		Property: intPointer(winner.WonProperty), Paid: intPointer(winner.RoundCost),
	})
	state.PropertyMarket = []int{}
	state.Purchases = append(state.Purchases, purchaseRecord(state))
	state.StarterPlayer = winnerID
	state.CurrentPlayer = ""
	state.Status = StatusBuyResult
	clearTurnDeadlines(state)
	return nil
}

func purchaseRecord(state *State) RoundRecord {
	trades := make([]Trade, 0, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		trades = append(trades, Trade{
			PlayerID: playerID,
			Property: player.WonProperty,
			Amount:   player.RoundCost,
		})
	}
	return RoundRecord{Round: state.Round, Trades: trades}
}

func advanceBuyRound(state *State) error {
	if state.Status != StatusBuyResult {
		return errors.New("buy result is not active")
	}
	if len(state.PropertyDeck) == 0 {
		state.Status = StatusBridge
		state.CurrentPlayer = ""
		clearTurnDeadlines(state)
		return nil
	}
	state.Round++
	if err := drawPropertyMarket(state); err != nil {
		return err
	}
	resetAuction(state)
	return nil
}

func startSelling(state *State) error {
	if state.Status != StatusBridge {
		return errors.New("selling cannot start in this phase")
	}
	state.Round = 1
	return prepareSaleRound(state)
}

func prepareSaleRound(state *State) error {
	if err := drawChequeMarket(state); err != nil {
		return err
	}
	state.Status = StatusSellSelect
	state.CurrentPlayer = ""
	state.PropertyMarket = []int{}
	state.HighestBid = 0
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		player.LockedProperty = 0
		player.ActionDeadline = zeroTime()
	}
	clearTransitionDeadline(state)
	return nil
}

func lockSale(state *State, playerID string, property int, source string) error {
	if state.Status != StatusSellSelect {
		return errors.New("sale selection is not active")
	}
	player := state.Roster[playerID]
	if player == nil || player.LockedProperty != 0 {
		return errors.New("player already locked a property")
	}
	if !containsInt(player.Properties, property) {
		return errors.New("property is not in the player's hand")
	}
	player.LockedProperty = property
	player.ActionDeadline = zeroTime()
	appendAction(state, Action{
		Round: state.Round, Phase: StatusSellSelect, Kind: "lockSale",
		PlayerID: playerID, Source: source,
	})
	if lockedPlayerCount(state) == len(state.PlayerOrder) {
		state.Status = StatusSellLocked
		clearTurnDeadlines(state)
	}
	return nil
}

func revealSale(state *State, source string) error {
	if state.Status != StatusSellLocked {
		return errors.New("sale reveal is not active")
	}
	type selection struct {
		playerID string
		property int
	}
	selections := make([]selection, 0, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		property := state.Roster[playerID].LockedProperty
		if property == 0 {
			return errors.New("not all players locked a property")
		}
		selections = append(selections, selection{playerID: playerID, property: property})
	}
	sort.Slice(selections, func(i, j int) bool {
		return selections[i].property < selections[j].property
	})
	cheques := append([]int(nil), state.ChequeMarket...)
	sort.Ints(cheques)
	trades := make([]Trade, 0, len(selections))
	for index, selected := range selections {
		player := state.Roster[selected.playerID]
		player.Properties = removeInt(player.Properties, selected.property)
		player.Cheques = append(player.Cheques, cheques[index])
		trades = append(trades, Trade{
			PlayerID: selected.playerID,
			Property: selected.property,
			Amount:   cheques[index],
		})
		property := selected.property
		cheque := cheques[index]
		appendAction(state, Action{
			Round: state.Round, Phase: StatusSellReveal, Kind: "sell",
			PlayerID: selected.playerID, Source: source,
			Property: &property, Cheque: &cheque,
		})
	}
	state.Sales = append(state.Sales, RoundRecord{Round: state.Round, Trades: trades})
	state.ChequeMarket = []int{}
	state.Status = StatusSellReveal
	return nil
}

func advanceSaleRound(state *State) error {
	if state.Status != StatusSellReveal {
		return errors.New("sale result is not active")
	}
	if allPropertiesSold(state) {
		finishGame(state)
		return nil
	}
	state.Round++
	return prepareSaleRound(state)
}

func finishGame(state *State) {
	state.Status = StatusEnd
	state.CurrentPlayer = ""
	state.ChequeMarket = []int{}
	clearTurnDeadlines(state)
	clearTransitionDeadline(state)
	state.Result = rankPlayers(state)
	state.EndReason = "completed"
}

func rankPlayers(state *State) map[string]PlayerResult {
	type score struct {
		playerID    string
		chequeTotal int
		coins       int
		total       int
	}
	scores := make([]score, 0, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		chequeTotal := sumInts(player.Cheques)
		scores = append(scores, score{
			playerID: playerID, chequeTotal: chequeTotal,
			coins: player.Coins, total: chequeTotal + player.Coins,
		})
	}
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].total != scores[j].total {
			return scores[i].total > scores[j].total
		}
		if scores[i].coins != scores[j].coins {
			return scores[i].coins > scores[j].coins
		}
		return playerSeat(state, scores[i].playerID) < playerSeat(state, scores[j].playerID)
	})
	result := make(map[string]PlayerResult, len(scores))
	for index, current := range scores {
		rank := index + 1
		if index > 0 &&
			current.total == scores[index-1].total &&
			current.coins == scores[index-1].coins {
			rank = result[scores[index-1].playerID].Rank
		}
		result[current.playerID] = PlayerResult{
			ChequeTotal: current.chequeTotal,
			Coins:       current.coins,
			Total:       current.total,
			Rank:        rank,
			Winner:      rank == 1,
		}
	}
	return result
}

func activePlayerIDs(state *State) []string {
	result := make([]string, 0, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player != nil && !player.Passed {
			result = append(result, playerID)
		}
	}
	return result
}

func nextActivePlayer(state *State, current string) string {
	index := indexOf(state.PlayerOrder, current)
	for offset := 1; offset <= len(state.PlayerOrder); offset++ {
		playerID := state.PlayerOrder[(index+offset)%len(state.PlayerOrder)]
		if player := state.Roster[playerID]; player != nil && !player.Passed {
			return playerID
		}
	}
	return current
}

func lockedPlayerCount(state *State) int {
	count := 0
	for _, playerID := range state.PlayerOrder {
		if player := state.Roster[playerID]; player != nil && player.LockedProperty != 0 {
			count++
		}
	}
	return count
}

func allPropertiesSold(state *State) bool {
	for _, playerID := range state.PlayerOrder {
		if len(state.Roster[playerID].Properties) != 0 {
			return false
		}
	}
	return true
}

func appendAction(state *State, action Action) {
	state.ActionCounter++
	action.ID = state.ActionCounter
	state.ActionLog = append(state.ActionLog, action)
	copyAction := action
	state.LastAction = &copyAction
}

func validateState(state *State) error {
	if state == nil {
		return errors.New("for sale state is nil")
	}
	if state.MaxPlayers != maxPlayers {
		return errors.New("for sale requires six room seats")
	}
	if state.RandomSeed == 0 {
		return errors.New("random seed is required")
	}
	if state.MatchRoundNo <= 0 {
		return errors.New("match round number must be positive")
	}
	if state.Roster == nil || state.Result == nil {
		return errors.New("for sale state has nil collections")
	}
	switch state.Status {
	case StatusMatch, StatusWaiting, StatusAuction, StatusBuyResult,
		StatusBridge, StatusSellSelect, StatusSellLocked, StatusSellReveal, StatusEnd:
	default:
		return fmt.Errorf("invalid for sale status %q", state.Status)
	}
	if len(state.PlayerOrder) > state.MaxPlayers {
		return errors.New("player order exceeds room capacity")
	}
	seenPlayers := map[string]bool{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil || player.PlayerID != playerID || seenPlayers[playerID] {
			return errors.New("roster and player order are inconsistent")
		}
		seenPlayers[playerID] = true
		if player.Coins < 0 || player.Bid < 0 || player.RoundCost < 0 {
			return errors.New("player has negative economic values")
		}
	}
	if state.TotalRounds == 0 {
		return nil
	}
	if len(state.PlayerOrder) < minPlayers || len(state.PlayerOrder) > maxPlayers {
		return errors.New("active match requires three to six players")
	}
	if err := validatePropertyConservation(state); err != nil {
		return err
	}
	if err := validateChequeConservation(state); err != nil {
		return err
	}
	if state.Status == StatusAuction {
		player := state.Roster[state.CurrentPlayer]
		if player == nil || player.Passed {
			return errors.New("auction current player is invalid")
		}
		if len(state.PropertyMarket) != len(activePlayerIDs(state)) {
			return errors.New("auction market does not match active players")
		}
	}
	if state.Status == StatusSellLocked &&
		lockedPlayerCount(state) != len(state.PlayerOrder) {
		return errors.New("sell locked state is incomplete")
	}
	if state.Status == StatusEnd {
		if !allPropertiesSold(state) || len(state.Result) != len(state.PlayerOrder) {
			return errors.New("finished game has incomplete results")
		}
	}
	return nil
}

func validatePropertyConservation(state *State) error {
	counts := map[int]int{}
	add := func(values []int) {
		for _, value := range values {
			counts[value]++
		}
	}
	add(state.RemovedProperties)
	add(state.PropertyDeck)
	add(state.PropertyMarket)
	for _, playerID := range state.PlayerOrder {
		add(state.Roster[playerID].Properties)
	}
	for _, record := range state.Sales {
		for _, trade := range record.Trades {
			counts[trade.Property]++
		}
	}
	for value := 1; value <= 30; value++ {
		if counts[value] != 1 {
			return fmt.Errorf("property %d conservation failed: %d", value, counts[value])
		}
	}
	if len(counts) != 30 {
		return errors.New("property set contains invalid values")
	}
	return nil
}

func validateChequeConservation(state *State) error {
	expected := map[int]int{0: 2}
	for value := 2; value <= 15; value++ {
		expected[value] = 2
	}
	actual := map[int]int{}
	add := func(values []int) {
		for _, value := range values {
			actual[value]++
		}
	}
	add(state.RemovedCheques)
	add(state.ChequeDeck)
	add(state.ChequeMarket)
	for _, playerID := range state.PlayerOrder {
		add(state.Roster[playerID].Cheques)
	}
	if len(actual) != len(expected) {
		return errors.New("cheque set contains invalid values")
	}
	for value, count := range expected {
		if actual[value] != count {
			return fmt.Errorf("cheque %d conservation failed: %d", value, actual[value])
		}
	}
	return nil
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func removeInt(values []int, target int) []int {
	for index, value := range values {
		if value == target {
			result := append([]int(nil), values[:index]...)
			return append(result, values[index+1:]...)
		}
	}
	return append([]int(nil), values...)
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return 0
}

func playerSeat(state *State, playerID string) int {
	if player := state.Roster[playerID]; player != nil {
		return player.SeatIndex
	}
	return maxPlayers
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func intPointer(value int) *int { return &value }

func zeroTime() time.Time { return time.Time{} }
