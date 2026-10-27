package onitama

import (
	"reflect"
	"testing"
)

func TestCardCatalogMatchesBaseGame(t *testing.T) {
	expected := []Card{
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
	if err := validateCardCatalog(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(allCards(), expected) {
		t.Fatalf("catalog = %+v", allCards())
	}
}

func TestDrawCardsIsDeterministicAndRoundScoped(t *testing.T) {
	first := drawCards(9264, 1)
	if !reflect.DeepEqual(first, drawCards(9264, 1)) {
		t.Fatal("same seed and round produced different cards")
	}
	if reflect.DeepEqual(first, drawCards(9264, 2)) {
		t.Fatal("different rounds produced identical ordered draw")
	}
	seen := map[string]bool{}
	for _, cardID := range first {
		if seen[cardID] {
			t.Fatalf("duplicate card %q", cardID)
		}
		if _, ok := cardByID(cardID); !ok {
			t.Fatalf("unknown card %q", cardID)
		}
		seen[cardID] = true
	}
}

func TestOffsetsRotateForRed(t *testing.T) {
	blue := Piece{Side: SideBlue, Cell: Cell{Row: 2, Column: 2}}
	red := Piece{Side: SideRed, Cell: Cell{Row: 2, Column: 2}}
	offset := Offset{X: 1, Y: -1}
	if targetFor(blue, offset) != (Cell{Row: 1, Column: 3}) {
		t.Fatalf("blue target = %+v", targetFor(blue, offset))
	}
	if targetFor(red, offset) != (Cell{Row: 3, Column: 1}) {
		t.Fatalf("red target = %+v", targetFor(red, offset))
	}
}
