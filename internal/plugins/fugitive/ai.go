package fugitive

import (
	"context"
	"errors"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

type fugitiveObservation struct {
	Status          string
	LastHideout     int
	OpeningPlaced   int
	Hand            []Card
	DeckCounts      []int
	HighestRevealed int
}

type marshalRouteEntry struct {
	Revealed    bool
	Number      int
	SprintCount int
}

type marshalObservation struct {
	Status     string
	Hand       []Card
	DeckCounts []int
	Route      []marshalRouteEntry
	Known      map[int]bool
	Hidden     int
	LastSearch []int
}

func (p *Plugin) Decide(
	ctx context.Context,
	request game.AIRequest,
) (contracts.RoomCommand, error) {
	state, err := asState(request.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	if state.CurrentPlayer != request.PlayerID {
		return contracts.RoomCommand{}, errors.New("AI is not the active player")
	}
	player := state.Roster[request.PlayerID]
	if player == nil {
		return contracts.RoomCommand{}, errors.New("AI player not found")
	}
	switch player.Role {
	case RoleFugitive:
		return decideFugitive(ctx, fugitiveViewForAI(state, request.PlayerID))
	case RoleMarshal:
		return decideMarshal(ctx, marshalViewForAI(state, request.PlayerID))
	default:
		return contracts.RoomCommand{}, errors.New("AI role is unavailable")
	}
}

func fugitiveViewForAI(
	state *State,
	playerID string,
) fugitiveObservation {
	player := state.Roster[playerID]
	hand := []Card{}
	if player != nil {
		hand = append(hand, player.Hand...)
	}
	counts := make([]int, len(state.Decks))
	for index := range state.Decks {
		counts[index] = len(state.Decks[index])
	}
	return fugitiveObservation{
		Status:          state.Status,
		LastHideout:     lastHideoutNumber(state),
		OpeningPlaced:   state.OpeningHideouts,
		Hand:            hand,
		DeckCounts:      counts,
		HighestRevealed: highestRevealed(state),
	}
}

func marshalViewForAI(state *State, playerID string) marshalObservation {
	player := state.Roster[playerID]
	hand := []Card{}
	if player != nil {
		hand = append(hand, player.Hand...)
	}
	route := make([]marshalRouteEntry, len(state.Route))
	for index, hideout := range state.Route {
		route[index] = marshalRouteEntry{
			Revealed:    hideout.Revealed,
			SprintCount: len(hideout.Sprints),
		}
		if hideout.Revealed {
			route[index].Number = hideout.Card.Number
		}
	}
	var lastSearch []int
	for index := len(state.Log) - 1; index >= 0; index-- {
		action := state.Log[index]
		if action.Role == RoleMarshal && action.Kind == "search" {
			lastSearch = append([]int(nil), action.Numbers...)
			break
		}
	}
	deckCounts := make([]int, len(state.Decks))
	for index := range state.Decks {
		deckCounts[index] = len(state.Decks[index])
	}
	return marshalObservation{
		Status:     state.Status,
		Hand:       hand,
		DeckCounts: deckCounts,
		Route:      route,
		Known:      knownNumbers(state, playerID),
		Hidden:     hiddenHideoutCount(state),
		LastSearch: lastSearch,
	}
}

func decideFugitive(
	ctx context.Context,
	view fugitiveObservation,
) (contracts.RoomCommand, error) {
	switch view.Status {
	case StatusDraw:
		deck := bestDrawDeck(view.DeckCounts, view.LastHideout)
		if deck < 0 {
			return contracts.RoomCommand{}, errors.New("no deck is available")
		}
		return contracts.RoomCommand{
			Type:    CommandDrawCard,
			Payload: map[string]any{"deckIndex": deck},
		}, nil
	case StatusOpening, StatusFugitiveAction:
		destination, sprints, ok := bestHideout(ctx, view)
		if ok {
			return contracts.RoomCommand{
				Type: CommandPlayHideout,
				Payload: map[string]any{
					"hideout": destination,
					"sprints": sprints,
				},
			}, nil
		}
		if view.Status == StatusOpening && view.OpeningPlaced > 0 {
			return contracts.RoomCommand{Type: CommandFinishOpening}, nil
		}
		if view.Status == StatusFugitiveAction {
			return contracts.RoomCommand{Type: CommandLayLow}, nil
		}
	}
	return contracts.RoomCommand{}, errors.New("fugitive AI has no action")
}

func decideMarshal(
	ctx context.Context,
	view marshalObservation,
) (contracts.RoomCommand, error) {
	switch view.Status {
	case StatusDraw:
		deck := bestDrawDeck(
			view.DeckCounts,
			highestKnown(view.Route),
		)
		if deck < 0 {
			return contracts.RoomCommand{}, errors.New("no deck is available")
		}
		return contracts.RoomCommand{
			Type:    CommandDrawCard,
			Payload: map[string]any{"deckIndex": deck},
		}, nil
	case StatusMarshalSearch, StatusManhunt:
		number, ok := bestMarshalGuess(ctx, view)
		if !ok {
			return contracts.RoomCommand{}, errors.New("marshal AI found no guess")
		}
		return contracts.RoomCommand{
			Type: CommandSearchHideouts,
			Payload: map[string]any{
				"numbers": []int{number},
			},
		}, nil
	}
	return contracts.RoomCommand{}, errors.New("marshal AI has no action")
}

func bestDrawDeck(counts []int, current int) int {
	best := -1
	bestScore := -1 << 30
	for index, remaining := range counts {
		if remaining <= 0 || index >= len(deckRanges) {
			continue
		}
		bounds := deckRanges[index]
		score := remaining * 4
		switch {
		case current < bounds[0]:
			score += 30 - (bounds[0] - current)
		case current <= bounds[1]:
			score += 24
		default:
			score -= current - bounds[1]
		}
		if score > bestScore {
			best = index
			bestScore = score
		}
	}
	return best
}

func bestHideout(
	ctx context.Context,
	view fugitiveObservation,
) (int, []int, bool) {
	type candidate struct {
		destination int
		sprints     []int
		score       int
	}
	candidates := []candidate{}
	for _, target := range view.Hand {
		if ctx.Err() != nil {
			break
		}
		if target.Number <= view.LastHideout {
			continue
		}
		required := target.Number - view.LastHideout - 3
		sprints, ok := minimalSprints(view.Hand, target.Number, required)
		if !ok {
			continue
		}
		score := target.Number*20 - len(sprints)*18
		if target.Number == 42 {
			if view.HighestRevealed >= 30 {
				score += 2000
			} else {
				score += 900
			}
		}
		if view.Status == StatusOpening {
			score -= target.Number * 5
			if view.OpeningPlaced == 0 && target.Number <= 3 {
				score += 200
			}
		}
		candidates = append(candidates, candidate{
			destination: target.Number,
			sprints:     sprints,
			score:       score,
		})
	}
	if len(candidates) == 0 {
		return 0, nil, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if len(candidates[i].sprints) != len(candidates[j].sprints) {
			return len(candidates[i].sprints) < len(candidates[j].sprints)
		}
		return candidates[i].destination < candidates[j].destination
	})
	return candidates[0].destination, candidates[0].sprints, true
}

func minimalSprints(
	hand []Card,
	destination,
	required int,
) ([]int, bool) {
	if required <= 0 {
		return []int{}, true
	}
	type selection struct {
		numbers []int
		value   int
	}
	bestByValue := map[int]selection{0: {numbers: []int{}, value: 0}}
	for _, value := range hand {
		if value.Number == destination ||
			value.Number == 42 ||
			value.Sprint <= 0 {
			continue
		}
		next := make(map[int]selection, len(bestByValue)*2)
		for total, current := range bestByValue {
			next[total] = current
			candidateTotal := total + value.Sprint
			candidate := selection{
				numbers: append(
					append([]int(nil), current.numbers...),
					value.Number,
				),
				value: candidateTotal,
			}
			existing, exists := next[candidateTotal]
			if !exists ||
				len(candidate.numbers) < len(existing.numbers) {
				next[candidateTotal] = candidate
			}
		}
		bestByValue = next
	}
	best := selection{}
	found := false
	for total, candidate := range bestByValue {
		if total < required {
			continue
		}
		if !found ||
			len(candidate.numbers) < len(best.numbers) ||
			len(candidate.numbers) == len(best.numbers) &&
				total < best.value {
			best = candidate
			found = true
		}
	}
	if !found {
		return nil, false
	}
	sort.Ints(best.numbers)
	return best.numbers, true
}

func bestMarshalGuess(
	ctx context.Context,
	view marshalObservation,
) (int, bool) {
	scores := make(map[int]int)
	for index, entry := range view.Route {
		if ctx.Err() != nil {
			break
		}
		if entry.Revealed {
			continue
		}
		lower := 1
		if index > 0 {
			previous := view.Route[index-1]
			if previous.Revealed {
				lower = previous.Number + 1
			}
		}
		upper := 41
		reach := 3 + entry.SprintCount*2
		if index > 0 && view.Route[index-1].Revealed {
			upper = view.Route[index-1].Number + reach
		}
		for next := index + 1; next < len(view.Route); next++ {
			if view.Route[next].Revealed {
				if view.Route[next].Number-1 < upper {
					upper = view.Route[next].Number - 1
				}
				break
			}
		}
		if upper > 41 {
			upper = 41
		}
		for number := lower; number <= upper; number++ {
			if !view.Known[number] {
				scores[number]++
			}
		}
	}
	best := 0
	bestScore := -1
	for number := 1; number < 42; number++ {
		score := scores[number]
		if view.Known[number] || score <= 0 {
			continue
		}
		for _, previous := range view.LastSearch {
			if previous == number {
				score--
			}
		}
		if score > bestScore {
			best = number
			bestScore = score
		}
	}
	return best, bestScore >= 0
}

func highestKnown(route []marshalRouteEntry) int {
	result := 0
	for _, entry := range route {
		if entry.Revealed && entry.Number != 42 && entry.Number > result {
			result = entry.Number
		}
	}
	return result
}
