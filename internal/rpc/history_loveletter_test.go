package rpc

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestBuildReplaySnapshotSupportsLoveLetter(t *testing.T) {
	payload, err := history.EncodeReplayFrame(1, map[string]any{
		"roomData": map[string]any{
			"currentPlayer": "alice",
			"gameStatus":    "play",
			"roundNo":       2,
			"goal":          4,
			"deckCount":     10,
			"reserveCount":  1,
			"burnCards":     []any{},
			"activityLog":   []any{},
			"players":       map[string]any{},
			"playerOrder":   []string{"alice", "bob"},
			"turnTimeoutMs": 60000,
		},
		"playersData": map[string]any{
			"alice": map[string]any{
				"favors": 1, "alive": true, "protected": false,
				"spyUsed": false, "handCount": 1, "discards": []any{},
			},
		},
		"privateData": map[string]any{
			"alice": map[string]any{
				"hand":            []any{map[string]any{"cardID": "c9", "rank": 9}},
				"legalCardIDs":    []string{"c9"},
				"legalTargets":    map[string]any{"c9": []string{}},
				"guardGuessRanks": []int{0, 2, 3, 4, 5, 6, 7, 8, 9},
			},
		},
		"deck":    []any{map[string]any{"cardID": "c1", "rank": 1}},
		"reserve": map[string]any{"cardID": "c0", "rank": 0},
		"result":  nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := (&Handler{}).buildReplaySnapshot(
		context.Background(),
		history.Game{
			GameType:             contracts.GameLoveLetter,
			ReplaySchemaVersion:  1,
			InitialReplayPayload: payload,
		},
		nil,
		nil,
		-1,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Loveletter == nil ||
		snapshot.Loveletter.Seq != -1 ||
		snapshot.Loveletter.RoomData.RoundNo != 2 ||
		snapshot.Loveletter.RoomData.Goal != 4 ||
		len(snapshot.Loveletter.Deck) != 1 ||
		snapshot.Loveletter.Reserve == nil ||
		snapshot.Loveletter.PrivateData["alice"].Hand[0].Rank != 9 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
