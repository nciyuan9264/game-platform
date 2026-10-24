package splendor

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/splendor/domain"
	"github.com/nciyuan9264/game-platform/internal/splendor/entities"
	"github.com/nciyuan9264/game-platform/internal/splendor/rules"
)

const (
	aiWinningScore       = 1_000_000
	aiBlockingScore      = 900_000
	aiBuyBaseScore       = 40_000
	aiBuyReservedBonus   = 60_000
	aiTakeBaseScore      = 10_000
	aiReserveBaseScore   = -3_000
	aiTargetLimit        = 3
	aiMaxTokens          = 10
	aiGoldReturnPenalty  = 5_000
	aiRetakePenalty      = 1_500
	aiReserveSlotPenalty = 1_500
)

var aiGemColors = []string{"White", "Blue", "Green", "Red", "Black"}

type aiActionKind int

const (
	aiBuyReserved aiActionKind = iota
	aiBuyPublic
	aiTakeGems
	aiReserveCard
	aiPass
)

type aiCandidate struct {
	command    contracts.RoomCommand
	score      int
	kind       aiActionKind
	cardID     int
	takeKey    string
	returnsKey string
}

type aiTarget struct {
	card   entities.NormalCard
	weight int
}

type splendorAI struct {
	state      *domain.GameState
	playerID   string
	player     *domain.PlayerState
	targets    []aiTarget
	winThreats map[int]bool
}

func decideSplendorAI(state *domain.GameState, playerID string) (contracts.RoomCommand, error) {
	if state == nil {
		return contracts.RoomCommand{}, errors.New("splendor AI state is nil")
	}
	player := state.Players[playerID]
	if player == nil {
		return contracts.RoomCommand{}, errors.New("AI player state is missing")
	}

	planner := &splendorAI{
		state:      state,
		playerID:   playerID,
		player:     player,
		winThreats: opponentWinningCards(state, playerID),
	}
	planner.targets = planner.selectTargets()

	candidates := planner.generateCandidates()
	if len(candidates) == 0 {
		return contracts.RoomCommand{}, errors.New("splendor AI found no legal action")
	}
	sort.Slice(candidates, func(i, j int) bool {
		return betterAICandidate(candidates[i], candidates[j])
	})
	return candidates[0].command, nil
}

func (a *splendorAI) generateCandidates() []aiCandidate {
	candidates := make([]aiCandidate, 0, 128)

	reserved := append([]entities.NormalCard(nil), a.player.ReserveCard...)
	sort.Slice(reserved, func(i, j int) bool { return reserved[i].ID < reserved[j].ID })
	for i := range reserved {
		card := reserved[i]
		candidate := aiCandidate{
			command: contracts.RoomCommand{
				Type:     "game_buy_card",
				PlayerID: a.playerID,
				Payload:  card.ID,
			},
			score:  a.scoreBuy(&card, true),
			kind:   aiBuyReserved,
			cardID: card.ID,
		}
		a.appendIfLegal(&candidates, candidate)
	}

	publicCards := revealedCardPointers(a.state)
	sort.Slice(publicCards, func(i, j int) bool { return publicCards[i].ID < publicCards[j].ID })
	for _, card := range publicCards {
		candidate := aiCandidate{
			command: contracts.RoomCommand{
				Type:     "game_buy_card",
				PlayerID: a.playerID,
				Payload:  card.ID,
			},
			score:  a.scoreBuy(card, false),
			kind:   aiBuyPublic,
			cardID: card.ID,
		}
		a.appendIfLegal(&candidates, candidate)
	}

	for _, take := range gemTakeOptions(a.state.Gems) {
		requiredReturns := tokenCount(a.player) + countValues(take) - aiMaxTokens
		if requiredReturns < 0 {
			requiredReturns = 0
		}
		for _, returns := range tokenReturnOptions(a.player.Gem, take, requiredReturns) {
			candidate := aiCandidate{
				command: contracts.RoomCommand{
					Type:     "game_get_gem",
					PlayerID: a.playerID,
					Payload: map[string]any{
						"take":    take,
						"returns": returns,
					},
				},
				score:      a.scoreTake(take, returns),
				kind:       aiTakeGems,
				cardID:     int(^uint(0) >> 1),
				takeKey:    gemCountsKey(take),
				returnsKey: gemCountsKey(returns),
			}
			a.appendIfLegal(&candidates, candidate)
		}
	}

	if len(a.player.ReserveCard) < 3 {
		additions := map[string]int{}
		if a.state.Gems["Gold"] > 0 {
			additions["Gold"] = 1
		}
		requiredReturns := tokenCount(a.player) + countValues(additions) - aiMaxTokens
		if requiredReturns < 0 {
			requiredReturns = 0
		}
		returnOptions := tokenReturnOptions(a.player.Gem, additions, requiredReturns)
		for _, card := range publicCards {
			for _, returns := range returnOptions {
				candidate := aiCandidate{
					command: contracts.RoomCommand{
						Type:     "game_preserve_card",
						PlayerID: a.playerID,
						Payload: map[string]any{
							"cardID":  card.ID,
							"returns": returns,
						},
					},
					score:      a.scoreReserve(card, returns),
					kind:       aiReserveCard,
					cardID:     card.ID,
					returnsKey: gemCountsKey(returns),
				}
				a.appendIfLegal(&candidates, candidate)
			}
		}
	}

	a.appendIfLegal(&candidates, aiCandidate{
		command: contracts.RoomCommand{
			Type:     "game_get_gem",
			PlayerID: a.playerID,
			Payload: map[string]any{
				"take":    map[string]int{},
				"returns": map[string]int{},
			},
		},
		kind:   aiPass,
		cardID: int(^uint(0) >> 1),
	})
	return candidates
}

func (a *splendorAI) appendIfLegal(candidates *[]aiCandidate, candidate aiCandidate) {
	payload, err := json.Marshal(candidate.command.Payload)
	if err != nil {
		return
	}
	command := domain.Command{
		Type:     candidate.command.Type,
		PlayerID: candidate.command.PlayerID,
		Payload:  payload,
	}
	if rules.ValidateAction(domain.NewRoom(a.state), command) == nil {
		*candidates = append(*candidates, candidate)
	}
}

func (a *splendorAI) scoreBuy(card *entities.NormalCard, reserved bool) int {
	noblePoints := triggeredNoblePoints(a.state, a.player, card)
	resultingScore := a.player.Score + card.Points + noblePoints
	if resultingScore >= 15 {
		return aiWinningScore + card.Points*1_000 + noblePoints*1_500
	}

	effectiveCost := discountedCardCost(a.player, card)
	goldSpent := goldRequiredToBuy(a.player, card)
	score := aiBuyBaseScore +
		card.Points*5_000 +
		noblePoints*8_000 +
		card.Points*3_000/(effectiveCost+1) -
		goldSpent*900 +
		a.bonusStrategicValue(card.Bonus)
	if reserved {
		score += aiBuyReservedBonus
	}
	if a.player.Score >= 10 || a.state.RoomStatus == domain.RoomStatusLastTurn {
		score += card.Points * 1_500
	}
	return score
}

func (a *splendorAI) scoreTake(take, returns map[string]int) int {
	after := applyGemDelta(a.player.Gem, take, returns)
	gapReduction := a.weightedTargetGap(a.player.Gem) - a.weightedTargetGap(after)
	utilityDelta := a.tokenUtility(after) - a.tokenUtility(a.player.Gem)
	netTokens := countValues(take) - countValues(returns)
	score := aiTakeBaseScore + gapReduction*1_600 + utilityDelta*250 + netTokens*250
	score -= returnPenalty(take, returns)
	return score
}

func (a *splendorAI) scoreReserve(card *entities.NormalCard, returns map[string]int) int {
	goldGained := 0
	if a.state.Gems["Gold"] > 0 {
		goldGained = 1 - returns["Gold"]
	}
	gap := cardTokenGap(a.player, card, a.player.Gem)
	score := aiReserveBaseScore +
		card.Points*2_500 +
		card.Level*300 -
		gap*500 +
		goldGained*1_200 +
		a.bonusStrategicValue(card.Bonus)/2 -
		len(a.player.ReserveCard)*aiReserveSlotPenalty -
		returnPenalty(map[string]int{"Gold": max(goldGained, 0)}, returns)
	if len(a.player.ReserveCard) == 2 {
		score -= 3_000
	}
	if a.winThreats[card.ID] && !rules.CanBuyCard(a.player, card) {
		score += aiBlockingScore
	}
	return score
}

func (a *splendorAI) selectTargets() []aiTarget {
	type rankedTarget struct {
		card     entities.NormalCard
		reserved bool
		score    int
	}
	ranked := make([]rankedTarget, 0, len(a.player.ReserveCard)+12)
	for _, card := range a.player.ReserveCard {
		ranked = append(ranked, rankedTarget{
			card:     cloneNormalCard(card),
			reserved: true,
			score:    a.targetValue(&card, true),
		})
	}
	for _, card := range revealedCardPointers(a.state) {
		ranked = append(ranked, rankedTarget{
			card:  cloneNormalCard(*card),
			score: a.targetValue(card, false),
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].reserved != ranked[j].reserved {
			return ranked[i].reserved
		}
		return ranked[i].card.ID < ranked[j].card.ID
	})
	if len(ranked) > aiTargetLimit {
		ranked = ranked[:aiTargetLimit]
	}

	targets := make([]aiTarget, 0, len(ranked))
	for index, target := range ranked {
		targets = append(targets, aiTarget{
			card:   target.card,
			weight: aiTargetLimit - index,
		})
	}
	return targets
}

func (a *splendorAI) targetValue(card *entities.NormalCard, reserved bool) int {
	score := card.Points*1_000 + card.Level*150 -
		cardTokenGap(a.player, card, a.player.Gem)*250 +
		a.nobleBonusValue(card.Bonus)
	if reserved {
		score += 2_000
	}
	return score
}

func (a *splendorAI) weightedTargetGap(gems map[string]int) int {
	total := 0
	for _, target := range a.targets {
		total += cardTokenGap(a.player, &target.card, gems) * target.weight
	}
	return total
}

func (a *splendorAI) tokenUtility(gems map[string]int) int {
	discounts := cardDiscounts(a.player)
	total := gems["Gold"] * 3
	for _, target := range a.targets {
		for _, color := range aiGemColors {
			need := target.card.Cost[color] - discounts[color]
			if need <= 0 {
				continue
			}
			held := gems[color]
			if held > need {
				held = need
			}
			total += held * target.weight
		}
	}
	return total
}

func (a *splendorAI) bonusStrategicValue(color string) int {
	value := a.nobleBonusValue(color)
	discounts := cardDiscounts(a.player)
	for _, target := range a.targets {
		if target.card.Cost[color] > discounts[color] {
			value += target.weight * 300
		}
	}
	return value
}

func (a *splendorAI) nobleBonusValue(color string) int {
	value := 0
	discounts := cardDiscounts(a.player)
	for _, noble := range a.state.NobleCards {
		if noble != nil && noble.State == entities.CardStateRevealed &&
			noble.Cost[color] > discounts[color] {
			value += 350
		}
	}
	return value
}

func betterAICandidate(left, right aiCandidate) bool {
	if left.score != right.score {
		return left.score > right.score
	}
	if left.kind != right.kind {
		return left.kind < right.kind
	}
	if left.cardID != right.cardID {
		return left.cardID < right.cardID
	}
	if left.takeKey != right.takeKey {
		return left.takeKey < right.takeKey
	}
	return left.returnsKey < right.returnsKey
}

func gemTakeOptions(bank map[string]int) []map[string]int {
	options := make([]map[string]int, 0, 30)
	var combinations func(start, remaining int, colors []string)
	combinations = func(start, remaining int, colors []string) {
		if remaining == 0 {
			take := make(map[string]int, len(colors))
			for _, color := range colors {
				take[color] = 1
			}
			options = append(options, take)
			return
		}
		for index := start; index <= len(aiGemColors)-remaining; index++ {
			color := aiGemColors[index]
			if bank[color] <= 0 {
				continue
			}
			combinations(index+1, remaining-1, append(colors, color))
		}
	}
	for size := 1; size <= 3; size++ {
		combinations(0, size, nil)
	}
	for _, color := range aiGemColors {
		if bank[color] >= 4 {
			options = append(options, map[string]int{color: 2})
		}
	}
	sort.Slice(options, func(i, j int) bool {
		return gemCountsKey(options[i]) < gemCountsKey(options[j])
	})
	return options
}

func tokenReturnOptions(
	held map[string]int,
	additions map[string]int,
	required int,
) []map[string]int {
	if required < 0 {
		return nil
	}
	if required == 0 {
		return []map[string]int{{}}
	}
	available := make(map[string]int, len(orderedGemColors))
	for _, color := range orderedGemColors {
		available[color] = held[color] + additions[color]
	}

	options := make([]map[string]int, 0, 32)
	current := make(map[string]int)
	var enumerate func(index, remaining int)
	enumerate = func(index, remaining int) {
		if index == len(orderedGemColors) {
			if remaining == 0 {
				option := make(map[string]int, len(current))
				for color, count := range current {
					if count > 0 {
						option[color] = count
					}
				}
				options = append(options, option)
			}
			return
		}
		color := orderedGemColors[index]
		limit := available[color]
		if limit > remaining {
			limit = remaining
		}
		for count := 0; count <= limit; count++ {
			if count > 0 {
				current[color] = count
			} else {
				delete(current, color)
			}
			enumerate(index+1, remaining-count)
		}
		delete(current, color)
	}
	enumerate(0, required)
	sort.Slice(options, func(i, j int) bool {
		return gemCountsKey(options[i]) < gemCountsKey(options[j])
	})
	return options
}

func opponentWinningCards(state *domain.GameState, playerID string) map[int]bool {
	result := make(map[int]bool)
	for _, card := range revealedCardPointers(state) {
		for opponentID, opponent := range state.Players {
			if opponentID == playerID || opponent == nil || !rules.CanBuyCard(opponent, card) {
				continue
			}
			if opponent.Score+card.Points+triggeredNoblePoints(state, opponent, card) >= 15 {
				result[card.ID] = true
				break
			}
		}
	}
	return result
}

func triggeredNoblePoints(
	state *domain.GameState,
	player *domain.PlayerState,
	card *entities.NormalCard,
) int {
	discounts := cardDiscounts(player)
	discounts[card.Bonus]++
	var selected *entities.NobleCard
	for _, noble := range state.NobleCards {
		if noble == nil || noble.State != entities.CardStateRevealed {
			continue
		}
		qualified := true
		for color, cost := range noble.Cost {
			if discounts[color] < cost {
				qualified = false
				break
			}
		}
		if qualified && (selected == nil || nobleIDLess(noble.ID, selected.ID)) {
			selected = noble
		}
	}
	if selected == nil {
		return 0
	}
	return selected.Points
}

func cardDiscounts(player *domain.PlayerState) map[string]int {
	discounts := make(map[string]int, len(aiGemColors))
	for _, card := range player.NormalCard {
		discounts[card.Bonus]++
	}
	return discounts
}

func discountedCardCost(player *domain.PlayerState, card *entities.NormalCard) int {
	discounts := cardDiscounts(player)
	total := 0
	for color, cost := range card.Cost {
		if remaining := cost - discounts[color]; remaining > 0 {
			total += remaining
		}
	}
	return total
}

func goldRequiredToBuy(player *domain.PlayerState, card *entities.NormalCard) int {
	discounts := cardDiscounts(player)
	total := 0
	for color, cost := range card.Cost {
		if missing := cost - discounts[color] - player.Gem[color]; missing > 0 {
			total += missing
		}
	}
	return total
}

func cardTokenGap(
	player *domain.PlayerState,
	card *entities.NormalCard,
	gems map[string]int,
) int {
	discounts := cardDiscounts(player)
	gap := 0
	for color, cost := range card.Cost {
		if missing := cost - discounts[color] - gems[color]; missing > 0 {
			gap += missing
		}
	}
	gap -= gems["Gold"]
	if gap < 0 {
		return 0
	}
	return gap
}

func applyGemDelta(
	held map[string]int,
	additions map[string]int,
	returns map[string]int,
) map[string]int {
	result := make(map[string]int, len(orderedGemColors))
	for _, color := range orderedGemColors {
		result[color] = held[color] + additions[color] - returns[color]
	}
	return result
}

func returnPenalty(take, returns map[string]int) int {
	penalty := returns["Gold"] * aiGoldReturnPenalty
	for color, count := range returns {
		if color == "Gold" {
			continue
		}
		retaken := count
		if retaken > take[color] {
			retaken = take[color]
		}
		penalty += retaken * aiRetakePenalty
	}
	return penalty
}

func countValues(values map[string]int) int {
	total := 0
	for _, count := range values {
		total += count
	}
	return total
}

func gemCountsKey(counts map[string]int) string {
	parts := make([]string, 0, len(counts))
	for _, color := range orderedGemColors {
		if counts[color] > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", color, counts[color]))
		}
	}
	return strings.Join(parts, ",")
}

func cloneGameState(state *domain.GameState) *domain.GameState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.PlayerOrder = append([]string(nil), state.PlayerOrder...)
	cloned.Roster = make(map[string]*domain.Player, len(state.Roster))
	for playerID, player := range state.Roster {
		if player == nil {
			cloned.Roster[playerID] = nil
			continue
		}
		playerCopy := *player
		cloned.Roster[playerID] = &playerCopy
	}
	cloned.NormalCards = make(map[string]*entities.NormalCard, len(state.NormalCards))
	for cardID, card := range state.NormalCards {
		if card == nil {
			cloned.NormalCards[cardID] = nil
			continue
		}
		cardCopy := cloneNormalCard(*card)
		cloned.NormalCards[cardID] = &cardCopy
	}
	cloned.NobleCards = make(map[string]*entities.NobleCard, len(state.NobleCards))
	for nobleID, noble := range state.NobleCards {
		if noble == nil {
			cloned.NobleCards[nobleID] = nil
			continue
		}
		nobleCopy := cloneNobleCard(*noble)
		cloned.NobleCards[nobleID] = &nobleCopy
	}
	cloned.Gems = cloneIntMap(state.Gems)
	cloned.Players = make(map[string]*domain.PlayerState, len(state.Players))
	for playerID, player := range state.Players {
		cloned.Players[playerID] = clonePlayerState(player)
	}
	if state.LastData != nil {
		lastData := *state.LastData
		lastData.Payload = append(json.RawMessage(nil), state.LastData.Payload...)
		cloned.LastData = &lastData
	}
	ensureState(&cloned)
	return &cloned
}

func clonePlayerState(player *domain.PlayerState) *domain.PlayerState {
	if player == nil {
		return nil
	}
	cloned := *player
	cloned.Gem = cloneIntMap(player.Gem)
	cloned.NormalCard = make([]entities.NormalCard, len(player.NormalCard))
	for index, card := range player.NormalCard {
		cloned.NormalCard[index] = cloneNormalCard(card)
	}
	cloned.ReserveCard = make([]entities.NormalCard, len(player.ReserveCard))
	for index, card := range player.ReserveCard {
		cloned.ReserveCard[index] = cloneNormalCard(card)
	}
	cloned.NobleCard = make([]entities.NobleCard, len(player.NobleCard))
	for index, noble := range player.NobleCard {
		cloned.NobleCard[index] = cloneNobleCard(noble)
	}
	return &cloned
}

func cloneNormalCard(card entities.NormalCard) entities.NormalCard {
	card.Cost = cloneIntMap(card.Cost)
	return card
}

func cloneNobleCard(noble entities.NobleCard) entities.NobleCard {
	noble.Cost = cloneIntMap(noble.Cost)
	return noble
}

func cloneIntMap(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
