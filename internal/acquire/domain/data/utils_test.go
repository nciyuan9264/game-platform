package data

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/acquire/roomcore"
)

func TestUtilsUsesMathRandV2InsteadOfExpRand(t *testing.T) {
	src, err := os.ReadFile("utils.go")
	if err != nil {
		t.Fatalf("read utils.go: %v", err)
	}
	if strings.Contains(string(src), "golang.org/x/exp/rand") {
		t.Fatal("utils.go should use math/rand/v2 instead of golang.org/x/exp/rand")
	}
}

func TestInitPlayerDataDealsSixTiles(t *testing.T) {
	room := &domain.Room{
		Base: roomcore.NewBase("six-tiles", 1),
		State: &domain.GameState{
			RandomSeed: 1,
			BoardTiles: make(map[string]*domain.Tile, 108),
			Players:    map[string]*domain.PlayerState{},
		},
	}
	for column := 1; column <= 12; column++ {
		for row := 'A'; row <= 'I'; row++ {
			id := fmt.Sprintf("%d%c", column, row)
			room.State.BoardTiles[id] = &domain.Tile{ID: id}
		}
	}

	if err := InitPlayerData(room, "player"); err != nil {
		t.Fatal(err)
	}
	if got := len(room.State.Players["player"].Tiles); got != 6 {
		t.Fatalf("initial tile count = %d, want 6", got)
	}
}
