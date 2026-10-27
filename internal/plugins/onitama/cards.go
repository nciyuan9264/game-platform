package onitama

import (
	"errors"
	"math/rand"
	"sort"
)

var cardCatalog = []Card{
	{ID: "tiger", Name: "虎", Mark: SideBlue, Moves: []Offset{{0, -2}, {0, 1}}},
	{ID: "dragon", Name: "龙", Mark: SideRed, Moves: []Offset{{-2, -1}, {2, -1}, {-1, 1}, {1, 1}}},
	{ID: "frog", Name: "蛙", Mark: SideRed, Moves: []Offset{{-2, 0}, {-1, -1}, {1, 1}}},
	{ID: "rabbit", Name: "兔", Mark: SideBlue, Moves: []Offset{{2, 0}, {1, -1}, {-1, 1}}},
	{ID: "crab", Name: "蟹", Mark: SideBlue, Moves: []Offset{{-2, 0}, {0, -1}, {2, 0}}},
	{ID: "elephant", Name: "象", Mark: SideRed, Moves: []Offset{{-1, -1}, {1, -1}, {-1, 0}, {1, 0}}},
	{ID: "goose", Name: "鹅", Mark: SideBlue, Moves: []Offset{{-1, -1}, {-1, 0}, {1, 0}, {1, 1}}},
	{ID: "rooster", Name: "鸡", Mark: SideRed, Moves: []Offset{{-1, 0}, {-1, 1}, {1, -1}, {1, 0}}},
	{ID: "monkey", Name: "猴", Mark: SideBlue, Moves: []Offset{{-1, -1}, {1, -1}, {-1, 1}, {1, 1}}},
	{ID: "mantis", Name: "螳螂", Mark: SideRed, Moves: []Offset{{-1, -1}, {1, -1}, {0, 1}}},
	{ID: "horse", Name: "马", Mark: SideRed, Moves: []Offset{{-1, 0}, {0, -1}, {0, 1}}},
	{ID: "ox", Name: "牛", Mark: SideBlue, Moves: []Offset{{1, 0}, {0, -1}, {0, 1}}},
	{ID: "crane", Name: "鹤", Mark: SideBlue, Moves: []Offset{{0, -1}, {-1, 1}, {1, 1}}},
	{ID: "boar", Name: "野猪", Mark: SideRed, Moves: []Offset{{-1, 0}, {0, -1}, {1, 0}}},
	{ID: "eel", Name: "鳗", Mark: SideBlue, Moves: []Offset{{-1, -1}, {-1, 1}, {1, 0}}},
	{ID: "cobra", Name: "蛇", Mark: SideRed, Moves: []Offset{{-1, 0}, {1, -1}, {1, 1}}},
}

var cardsByID = buildCardIndex()

func buildCardIndex() map[string]Card {
	result := make(map[string]Card, len(cardCatalog))
	for _, card := range cardCatalog {
		copyCard := card
		copyCard.Moves = append([]Offset(nil), card.Moves...)
		result[card.ID] = copyCard
	}
	return result
}

func cardByID(id string) (Card, bool) {
	card, ok := cardsByID[id]
	if !ok {
		return Card{}, false
	}
	card.Moves = append([]Offset(nil), card.Moves...)
	return card, true
}

func allCards() []Card {
	result := make([]Card, len(cardCatalog))
	for index, card := range cardCatalog {
		result[index] = card
		result[index].Moves = append([]Offset(nil), card.Moves...)
	}
	return result
}

func drawCards(seed uint64, roundNo int) []string {
	ids := make([]string, len(cardCatalog))
	for index, card := range cardCatalog {
		ids[index] = card.ID
	}
	sourceSeed := int64(seed ^ uint64(roundNo)*0x9e3779b97f4a7c15)
	random := rand.New(rand.NewSource(sourceSeed))
	random.Shuffle(len(ids), func(i, j int) {
		ids[i], ids[j] = ids[j], ids[i]
	})
	return append([]string(nil), ids[:5]...)
}

func validateCardCatalog() error {
	if len(cardCatalog) != 16 || len(cardsByID) != 16 {
		return errors.New("onitama requires 16 unique cards")
	}
	for _, card := range cardCatalog {
		if card.ID == "" || card.Name == "" ||
			(card.Mark != SideBlue && card.Mark != SideRed) ||
			len(card.Moves) < 2 || len(card.Moves) > 4 {
			return errors.New("invalid onitama card catalog")
		}
		seen := map[Offset]bool{}
		for _, offset := range card.Moves {
			if (offset.X == 0 && offset.Y == 0) || seen[offset] {
				return errors.New("invalid onitama card move")
			}
			seen[offset] = true
		}
	}
	return nil
}

func sortedCardIDs(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
