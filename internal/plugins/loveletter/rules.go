package loveletter

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

var roleCounts = [...]int{2, 6, 2, 2, 2, 2, 2, 1, 1, 1}

func goalFor(playerCount int) int {
	switch playerCount {
	case 2:
		return 6
	case 3:
		return 5
	case 4:
		return 4
	default:
		return 3
	}
}

func freshDeck(roundNo int) []Card {
	result := make([]Card, 0, 21)
	for rank, count := range roleCounts {
		for copyIndex := 0; copyIndex < count; copyIndex++ {
			result = append(result, Card{
				CardID: fmt.Sprintf("r%d-%d-%d", roundNo, rank, copyIndex),
				Rank:   rank,
			})
		}
	}
	return result
}

func nextRandom(state *State, limit int) int {
	if limit <= 1 {
		return 0
	}
	state.RandomCounter++
	value := state.RandomSeed + state.RandomCounter*0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	value ^= value >> 31
	return int(value % uint64(limit))
}

func shuffle(state *State, cards []Card) {
	for index := len(cards) - 1; index > 0; index-- {
		other := nextRandom(state, index+1)
		cards[index], cards[other] = cards[other], cards[index]
	}
}

func drawTop(cards *[]Card) (Card, bool) {
	if len(*cards) == 0 {
		return Card{}, false
	}
	value := (*cards)[0]
	*cards = (*cards)[1:]
	return value, true
}

func startRound(state *State, startPlayerID string) error {
	if len(state.PlayerOrder) < 2 || len(state.PlayerOrder) > 6 {
		return errors.New("loveletter requires two to six players")
	}
	if state.Roster[startPlayerID] == nil {
		return errors.New("round starter is not seated")
	}
	state.RoundNo++
	state.Deck = freshDeck(state.RoundNo)
	shuffle(state, state.Deck)
	reserve, ok := drawTop(&state.Deck)
	if !ok {
		return errors.New("failed to reserve a card")
	}
	state.Reserve = &reserve
	state.Burn = []Card{}
	if len(state.PlayerOrder) == 2 {
		for len(state.Burn) < 3 {
			card, available := drawTop(&state.Deck)
			if !available {
				return errors.New("failed to reveal two-player burn cards")
			}
			state.Burn = append(state.Burn, card)
		}
	}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			return errors.New("player order contains an empty seat")
		}
		card, available := drawTop(&state.Deck)
		if !available {
			return errors.New("failed to deal player hand")
		}
		player.Hand = []Card{card}
		player.Discards = []Card{}
		player.Alive = true
		player.Protected = false
		player.SpyUsed = false
		player.Private = nil
		player.KnownCards = map[string]KnownCard{}
	}
	state.CurrentPlayer = startPlayerID
	state.Status = StatusDraw
	state.RoundResult = nil
	state.Result = nil
	state.RoundTransitionDeadline = time.Time{}
	appendAction(state, Action{
		Kind:     "roundStart",
		PlayerID: startPlayerID,
		Summary:  fmt.Sprintf("第 %d 轮开始。", state.RoundNo),
	})
	return nil
}

func drawCard(state *State, playerID string, automatic bool) error {
	if state.Status != StatusDraw || state.CurrentPlayer != playerID {
		return errors.New("not in the active draw phase")
	}
	player := state.Roster[playerID]
	if player == nil || !player.Alive {
		return errors.New("active player is unavailable")
	}
	card, ok := drawTop(&state.Deck)
	if !ok {
		finishRound(state)
		return nil
	}
	player.Hand = append(player.Hand, card)
	state.Status = StatusPlay
	appendAction(state, Action{
		Kind:      "draw",
		PlayerID:  playerID,
		Automatic: automatic,
		Summary:   "摸取一张牌。",
	})
	return nil
}

func forcedCountess(state *State, playerID string) bool {
	player := state.Roster[playerID]
	if player == nil {
		return false
	}
	hasCountess := false
	hasKingOrPrince := false
	for _, card := range player.Hand {
		if card.Rank == 8 {
			hasCountess = true
		}
		if card.Rank == 5 || card.Rank == 7 {
			hasKingOrPrince = true
		}
	}
	return hasCountess && hasKingOrPrince
}

func legalCardIDs(state *State, playerID string) []string {
	player := state.Roster[playerID]
	if player == nil || state.Status != StatusPlay || state.CurrentPlayer != playerID {
		return []string{}
	}
	result := make([]string, 0, len(player.Hand))
	for _, card := range player.Hand {
		if forcedCountess(state, playerID) && card.Rank != 8 {
			continue
		}
		result = append(result, card.CardID)
	}
	sort.Strings(result)
	return result
}

func legalTargets(state *State, playerID string, rank int) []string {
	if rank != 1 && rank != 2 && rank != 3 && rank != 5 && rank != 7 {
		return []string{}
	}
	result := []string{}
	for _, targetID := range state.PlayerOrder {
		target := state.Roster[targetID]
		if target == nil || !target.Alive {
			continue
		}
		if rank == 5 && targetID == playerID {
			result = append(result, targetID)
			continue
		}
		if targetID == playerID || target.Protected {
			continue
		}
		result = append(result, targetID)
	}
	return result
}

func cardByID(player *Player, cardID string) (Card, bool) {
	for _, card := range player.Hand {
		if card.CardID == cardID {
			return card, true
		}
	}
	return Card{}, false
}

func removeCard(cards []Card, cardID string) ([]Card, Card, bool) {
	for index, card := range cards {
		if card.CardID != cardID {
			continue
		}
		result := append([]Card(nil), cards[:index]...)
		result = append(result, cards[index+1:]...)
		return result, card, true
	}
	return cards, Card{}, false
}

func discardCard(player *Player, card Card) {
	player.Discards = append(player.Discards, card)
	if card.Rank == 0 {
		player.SpyUsed = true
	}
}

func eliminate(state *State, playerID string) {
	player := state.Roster[playerID]
	if player == nil || !player.Alive {
		return
	}
	for _, card := range player.Hand {
		discardCard(player, card)
	}
	player.Hand = []Card{}
	player.Alive = false
	player.Protected = false
	invalidateKnowledge(state, playerID)
}

func invalidateKnowledge(state *State, targetID string) {
	for _, player := range state.Roster {
		if player != nil {
			delete(player.KnownCards, targetID)
		}
	}
}

func setPrivateEffect(player *Player, effect PrivateEffect) {
	copyEffect := effect
	player.Private = &copyEffect
}

func playCard(
	state *State,
	playerID,
	cardID,
	targetPlayerID string,
	guessRank *int,
	automatic bool,
) error {
	if state.Status != StatusPlay || state.CurrentPlayer != playerID {
		return errors.New("not in the active play phase")
	}
	player := state.Roster[playerID]
	if player == nil || !player.Alive {
		return errors.New("active player is unavailable")
	}
	card, ok := cardByID(player, cardID)
	if !ok {
		return errors.New("card is not in the active hand")
	}
	if forcedCountess(state, playerID) && card.Rank != 8 {
		return errors.New("countess must be played with king or prince")
	}
	targets := legalTargets(state, playerID, card.Rank)
	requiresTarget := card.Rank == 1 || card.Rank == 2 ||
		card.Rank == 3 || card.Rank == 5 || card.Rank == 7
	if requiresTarget && len(targets) > 0 && !containsString(targets, targetPlayerID) {
		return errors.New("a legal target is required")
	}
	if card.Rank == 1 && len(targets) > 0 {
		if guessRank == nil || *guessRank < 0 || *guessRank > 9 || *guessRank == 1 {
			return errors.New("guard guess must name a non-guard role")
		}
	}

	player.Hand, card, _ = removeCard(player.Hand, cardID)
	discardCard(player, card)
	rank := card.Rank
	action := Action{
		Kind:         "play",
		PlayerID:     playerID,
		CardRank:     &rank,
		TargetPlayer: targetPlayerID,
		Automatic:    automatic,
		Summary:      fmt.Sprintf("打出 %d 点角色牌。", card.Rank),
	}
	if guessRank != nil {
		guess := *guessRank
		action.GuessRank = &guess
	}
	appendAction(state, action)

	if requiresTarget && len(targets) == 0 {
		appendAction(state, Action{
			Kind:      "noTarget",
			PlayerID:  playerID,
			CardRank:  &rank,
			Automatic: automatic,
			Summary:   "没有合法目标，效果不生效。",
		})
		return advanceTurn(state)
	}
	target := state.Roster[targetPlayerID]
	switch card.Rank {
	case 0:
	case 1:
		success := len(target.Hand) == 1 && target.Hand[0].Rank == *guessRank
		state.LastAction.Success = &success
		state.Log[len(state.Log)-1].Success = &success
		if success {
			eliminate(state, targetPlayerID)
		}
	case 2:
		targetRank := target.Hand[0].Rank
		setPrivateEffect(player, PrivateEffect{
			EffectID:     fmt.Sprintf("%d-priest-%s", len(state.Log), playerID),
			Kind:         "priest",
			TargetPlayer: targetPlayerID,
			TargetRank:   &targetRank,
			Summary:      "秘密查看了目标手牌。",
		})
		player.KnownCards[targetPlayerID] = KnownCard{Rank: targetRank, Valid: true}
	case 3:
		actorRank := player.Hand[0].Rank
		targetRank := target.Hand[0].Rank
		setPrivateEffect(player, PrivateEffect{
			EffectID:     fmt.Sprintf("%d-baron-%s", len(state.Log), playerID),
			Kind:         "baron",
			TargetPlayer: targetPlayerID,
			TargetRank:   &targetRank,
			ActorRank:    &actorRank,
			Summary:      "秘密比较了双方手牌。",
		})
		setPrivateEffect(target, PrivateEffect{
			EffectID:     fmt.Sprintf("%d-baron-%s", len(state.Log), targetPlayerID),
			Kind:         "baron",
			TargetPlayer: playerID,
			TargetRank:   &actorRank,
			ActorRank:    &targetRank,
			Summary:      "秘密比较了双方手牌。",
		})
		if actorRank < targetRank {
			eliminate(state, playerID)
		} else if targetRank < actorRank {
			eliminate(state, targetPlayerID)
		}
	case 4:
		player.Protected = true
	case 5:
		dropped := target.Hand[0]
		target.Hand = target.Hand[1:]
		discardCard(target, dropped)
		invalidateKnowledge(state, targetPlayerID)
		if dropped.Rank == 9 {
			eliminate(state, targetPlayerID)
		} else if replacement, available := drawTop(&state.Deck); available {
			target.Hand = []Card{replacement}
		} else if state.Reserve != nil {
			target.Hand = []Card{*state.Reserve}
			state.Reserve = nil
		}
	case 6:
		for drawCount := 0; drawCount < 2; drawCount++ {
			value, available := drawTop(&state.Deck)
			if !available {
				break
			}
			player.Hand = append(player.Hand, value)
		}
		if len(player.Hand) > 1 {
			state.Status = StatusChancellor
			return nil
		}
	case 7:
		player.Hand, target.Hand = target.Hand, player.Hand
		invalidateKnowledge(state, playerID)
		invalidateKnowledge(state, targetPlayerID)
	case 8:
	case 9:
		eliminate(state, playerID)
	default:
		return errors.New("unknown loveletter card rank")
	}
	return advanceTurn(state)
}

func settleChancellor(
	state *State,
	playerID,
	keepCardID string,
	bottomCardIDs []string,
	automatic bool,
) error {
	if state.Status != StatusChancellor || state.CurrentPlayer != playerID {
		return errors.New("not in the chancellor phase")
	}
	player := state.Roster[playerID]
	if player == nil || !player.Alive {
		return errors.New("active player is unavailable")
	}
	keep, ok := cardByID(player, keepCardID)
	if !ok || len(bottomCardIDs) != len(player.Hand)-1 {
		return errors.New("invalid chancellor selection")
	}
	seen := map[string]bool{keepCardID: true}
	bottom := make([]Card, 0, len(bottomCardIDs))
	for _, cardID := range bottomCardIDs {
		if seen[cardID] {
			return errors.New("chancellor cards must be unique")
		}
		card, exists := cardByID(player, cardID)
		if !exists {
			return errors.New("chancellor card is not in hand")
		}
		seen[cardID] = true
		bottom = append(bottom, card)
	}
	state.Deck = append(state.Deck, bottom...)
	player.Hand = []Card{keep}
	invalidateKnowledge(state, playerID)
	appendAction(state, Action{
		Kind:      "settleChancellor",
		PlayerID:  playerID,
		CardRank:  intPointer(6),
		Automatic: automatic,
		Private:   true,
		Summary:   "保留一张手牌，其余牌秘密放回牌堆底。",
	})
	return advanceTurn(state)
}

func advanceTurn(state *State) error {
	if livingPlayers(state) <= 1 || len(state.Deck) == 0 {
		finishRound(state)
		return nil
	}
	currentIndex := indexOf(state.PlayerOrder, state.CurrentPlayer)
	if currentIndex < 0 {
		return errors.New("current player is not seated")
	}
	for offset := 1; offset <= len(state.PlayerOrder); offset++ {
		nextID := state.PlayerOrder[(currentIndex+offset)%len(state.PlayerOrder)]
		next := state.Roster[nextID]
		if next == nil || !next.Alive {
			continue
		}
		next.Protected = false
		next.Private = nil
		invalidateKnowledge(state, nextID)
		state.CurrentPlayer = nextID
		state.Status = StatusDraw
		return nil
	}
	return errors.New("no living player found")
}

func finishRound(state *State) {
	if state.Status == StatusRoundEnd || state.Status == StatusEnd {
		return
	}
	living := make([]string, 0, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		if player := state.Roster[playerID]; player != nil && player.Alive {
			living = append(living, playerID)
		}
	}
	winners := append([]string(nil), living...)
	reason := "last"
	if len(living) > 1 {
		reason = "deck"
		highest := -1
		winners = []string{}
		for _, playerID := range living {
			player := state.Roster[playerID]
			rank := -1
			if len(player.Hand) == 1 {
				rank = player.Hand[0].Rank
			}
			if rank > highest {
				highest = rank
				winners = []string{playerID}
			} else if rank == highest {
				winners = append(winners, playerID)
			}
		}
	}
	before := map[string]int{}
	for _, playerID := range state.PlayerOrder {
		before[playerID] = state.Roster[playerID].Favors
	}
	for _, playerID := range winners {
		state.Roster[playerID].Favors++
	}
	spyID := ""
	for _, playerID := range living {
		if !state.Roster[playerID].SpyUsed {
			continue
		}
		if spyID != "" {
			spyID = "-"
			break
		}
		spyID = playerID
	}
	if spyID != "" && spyID != "-" {
		state.Roster[spyID].Favors++
	} else {
		spyID = ""
	}
	after := map[string]int{}
	matchWinners := []string{}
	revealed := map[string]Card{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		after[playerID] = player.Favors
		if player.Favors >= state.Goal {
			matchWinners = append(matchWinners, playerID)
		}
		if player.Alive && len(player.Hand) == 1 {
			revealed[playerID] = player.Hand[0]
		}
	}
	state.RoundResult = &RoundResult{
		WinnerIDs:      winners,
		SpyPlayerID:    spyID,
		Reason:         reason,
		FavorsBefore:   before,
		FavorsAfter:    after,
		RevealedHands:  revealed,
		MatchWinnerIDs: matchWinners,
	}
	state.CurrentPlayer = ""
	if len(matchWinners) == 0 {
		state.Status = StatusRoundEnd
	} else {
		state.Status = StatusEnd
		state.Result = &Result{
			WinnerIDs: matchWinners,
			Favors:    after,
			FinalRank: finalRanks(state, matchWinners),
			RoundNo:   state.RoundNo,
			Reason:    "favor",
		}
	}
	appendAction(state, Action{
		Kind:     "roundEnd",
		PlayerID: firstString(winners),
		Summary:  fmt.Sprintf("第 %d 轮结束。", state.RoundNo),
	})
}

func finalRanks(state *State, winners []string) map[string]int {
	result := map[string]int{}
	winnerSet := map[string]bool{}
	for _, playerID := range winners {
		result[playerID] = 1
		winnerSet[playerID] = true
	}
	remaining := make([]string, 0, len(state.PlayerOrder)-len(winners))
	for _, playerID := range state.PlayerOrder {
		if !winnerSet[playerID] {
			remaining = append(remaining, playerID)
		}
	}
	sort.SliceStable(remaining, func(i, j int) bool {
		left := state.Roster[remaining[i]]
		right := state.Roster[remaining[j]]
		if left.Favors != right.Favors {
			return left.Favors > right.Favors
		}
		return left.SeatIndex < right.SeatIndex
	})
	rank := len(winners) + 1
	for index, playerID := range remaining {
		if index > 0 &&
			state.Roster[playerID].Favors < state.Roster[remaining[index-1]].Favors {
			rank = len(winners) + index + 1
		}
		result[playerID] = rank
	}
	return result
}

func nextRound(state *State, actorID string, automatic bool) error {
	if state.Status != StatusRoundEnd || state.RoundResult == nil {
		return errors.New("round is not ready to advance")
	}
	if !automatic && actorID != state.OwnerID {
		return errors.New("only the owner can advance the round")
	}
	winners := state.RoundResult.WinnerIDs
	if len(winners) == 0 {
		return errors.New("round has no winner")
	}
	startPlayerID := winners[nextRandom(state, len(winners))]
	return startRound(state, startPlayerID)
}

func appendAction(state *State, action Action) {
	action.ID = len(state.Log) + 1
	action.RoundNo = state.RoundNo
	copyAction := cloneAction(action)
	state.Log = append(state.Log, copyAction)
	state.LastAction = &copyAction
}

func livingPlayers(state *State) int {
	total := 0
	for _, player := range state.Roster {
		if player != nil && player.Alive {
			total++
		}
	}
	return total
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}

func intPointer(value int) *int { return &value }

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func validateCardConservation(state *State) error {
	if state.Status == StatusMatch || state.Status == StatusWaiting {
		return nil
	}
	cards := append([]Card(nil), state.Deck...)
	cards = append(cards, state.Burn...)
	if state.Reserve != nil {
		cards = append(cards, *state.Reserve)
	}
	for _, player := range state.Roster {
		if player == nil {
			continue
		}
		cards = append(cards, player.Hand...)
		cards = append(cards, player.Discards...)
	}
	if len(cards) != 21 {
		return fmt.Errorf("card conservation failed: got %d cards", len(cards))
	}
	seen := map[string]bool{}
	for _, card := range cards {
		if card.Rank < 0 || card.Rank > 9 || card.CardID == "" || seen[card.CardID] {
			return errors.New("card conservation contains invalid or duplicate cards")
		}
		seen[card.CardID] = true
	}
	return nil
}
