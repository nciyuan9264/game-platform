package platformapi

import (
	"encoding/json"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestCompletedReplayIncludesAllPlayersPrivateState(t *testing.T) {
	state := domain.GameState{
		Players: map[string]*domain.PlayerState{
			"alice": {Tiles: []string{"1A"}, Money: 6000},
			"bob":   {Tiles: []string{"2B"}, Money: 5000},
		},
	}
	initialState, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := acquireSnapshot(history.Game{InitialState: initialState}, nil, nil, -1)
	if err != nil {
		t.Fatalf("acquireSnapshot() error = %v", err)
	}
	snapshot := raw.(map[string]any)
	players := snapshot["playersData"].(map[string]any)
	bob := players["bob"].(*domain.PlayerState)
	if len(bob.Tiles) != 1 || bob.Tiles[0] != "2B" {
		t.Fatalf("completed replay hid another participant's state: %+v", bob)
	}
}
