package fugitive

import (
	"errors"
	"fmt"
	"sort"
)

var deckRanges = [][2]int{{4, 14}, {15, 28}, {29, 41}}

func card(number int) Card {
	sprint := 0
	if number > 0 && number < 42 {
		sprint = 2 - number%2
	}
	return Card{Number: number, Sprint: sprint}
}

func freshDecks(state *State) [][]Card {
	decks := make([][]Card, len(deckRanges))
	for deckIndex, bounds := range deckRanges {
		for number := bounds[0]; number <= bounds[1]; number++ {
			decks[deckIndex] = append(decks[deckIndex], card(number))
		}
		shuffle(state, decks[deckIndex])
	}
	return decks
}

func shuffle(state *State, cards []Card) {
	for index := len(cards) - 1; index > 0; index-- {
		swap := int(nextRandom(state) % uint64(index+1))
		cards[index], cards[swap] = cards[swap], cards[index]
	}
}

func nextRandom(state *State) uint64 {
	state.RandomCounter++
	value := state.RandomSeed + state.RandomCounter*0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func drawFromDeck(state *State, deckIndex int) (Card, error) {
	if deckIndex < 0 || deckIndex >= len(state.Decks) {
		return Card{}, errors.New("invalid deck")
	}
	if len(state.Decks[deckIndex]) == 0 {
		return Card{}, errors.New("deck is empty")
	}
	value := state.Decks[deckIndex][0]
	state.Decks[deckIndex] = state.Decks[deckIndex][1:]
	return value, nil
}

func lowestNonEmptyDeck(state *State) int {
	for index, cards := range state.Decks {
		if len(cards) > 0 {
			return index
		}
	}
	return -1
}

func sortHand(cards []Card) {
	sort.Slice(cards, func(i, j int) bool {
		return cards[i].Number < cards[j].Number
	})
}

func validateCardConservation(state *State) error {
	if state.Status == StatusMatch || state.Status == StatusWaiting && len(state.Route) == 0 {
		return nil
	}
	counts := make(map[int]int, 43)
	add := func(value Card) {
		counts[value.Number]++
	}
	for _, hideout := range state.Route {
		add(hideout.Card)
		for _, sprint := range hideout.Sprints {
			add(sprint)
		}
	}
	for _, player := range state.Roster {
		if player == nil {
			continue
		}
		for _, value := range player.Hand {
			add(value)
		}
	}
	for _, deck := range state.Decks {
		for _, value := range deck {
			add(value)
		}
	}
	for number := 0; number <= 42; number++ {
		if counts[number] != 1 {
			return fmt.Errorf("card %d count = %d, want 1", number, counts[number])
		}
	}
	if len(counts) != 43 {
		return fmt.Errorf("deck contains %d unique cards, want 43", len(counts))
	}
	return nil
}
