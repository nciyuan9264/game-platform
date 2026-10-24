package playerprofile

import (
	"context"
	"errors"
	"strconv"
	"testing"
)

type fakePublicProfiles struct {
	profiles  map[string]string
	batches   [][]string
	failBatch int
}

func (f *fakePublicProfiles) BatchGetPublicProfiles(
	_ context.Context,
	userIDs []string,
) (map[string]string, error) {
	f.batches = append(f.batches, append([]string(nil), userIDs...))
	if f.failBatch > 0 && len(f.batches) == f.failBatch {
		return nil, errors.New("auth unavailable")
	}
	result := make(map[string]string)
	for _, userID := range userIDs {
		if displayName, ok := f.profiles[userID]; ok {
			result[userID] = displayName
		}
	}
	return result, nil
}

func TestResolverUsesCurrentProfilesAndExplicitFallbacks(t *testing.T) {
	directory := &fakePublicProfiles{profiles: map[string]string{
		"7": "Alice-Cooper-7",
	}}
	resolver := NewResolver(directory)

	displayNames := resolver.Resolve(context.Background(), []Player{
		{UserID: "7", PlayerID: "7", DisplayName: "Old Alice"},
		{UserID: "9", PlayerID: "9", DisplayName: "Old Bob"},
		{UserID: "11", PlayerID: "11"},
		{PlayerID: "ai_001", DisplayName: "HAL", IsAI: true},
		{PlayerID: "ai_002", IsAI: true},
	})

	if displayNames["7"] != "Alice-Cooper-7" {
		t.Fatalf("current display name = %q", displayNames["7"])
	}
	if displayNames["9"] != DeletedUserDisplayName || displayNames["11"] != DeletedUserDisplayName {
		t.Fatalf("deleted user display names = %q, %q", displayNames["9"], displayNames["11"])
	}
	if displayNames["ai_001"] != "HAL" || displayNames["ai_002"] != "AI" {
		t.Fatalf("AI display names = %q, %q", displayNames["ai_001"], displayNames["ai_002"])
	}
	if len(directory.batches) != 1 || len(directory.batches[0]) != 3 {
		t.Fatalf("Auth batches = %v", directory.batches)
	}
}

func TestResolverFallsBackToPersistedNamesWhenAuthFails(t *testing.T) {
	directory := &fakePublicProfiles{failBatch: 1}
	resolver := NewResolver(directory)

	displayNames := resolver.Resolve(context.Background(), []Player{
		{UserID: "7", PlayerID: "7", DisplayName: "Persisted Alice"},
		{UserID: "9", PlayerID: "9"},
	})

	if displayNames["7"] != "Persisted Alice" || displayNames["9"] != "玩家 9" {
		t.Fatalf("fallback display names = %v", displayNames)
	}
}

func TestResolverDeduplicatesAndChunksAuthRequests(t *testing.T) {
	directory := &fakePublicProfiles{profiles: map[string]string{}}
	players := make([]Player, 0, maxBatchSize+2)
	for i := 1; i <= maxBatchSize+1; i++ {
		userID := strconv.Itoa(i)
		directory.profiles[userID] = "Player " + userID
		players = append(players, Player{UserID: userID, PlayerID: userID})
	}
	players = append(players, Player{UserID: "1", PlayerID: "guest-key"})

	displayNames := NewResolver(directory).Resolve(context.Background(), players)

	if len(directory.batches) != 2 ||
		len(directory.batches[0]) != maxBatchSize ||
		len(directory.batches[1]) != 1 {
		t.Fatalf("Auth batch sizes = %v", batchSizes(directory.batches))
	}
	if displayNames["1"] != "Player 1" || displayNames["guest-key"] != "Player 1" {
		t.Fatalf("duplicate user display names = %v", displayNames)
	}
}

func batchSizes(batches [][]string) []int {
	sizes := make([]int, len(batches))
	for i := range batches {
		sizes[i] = len(batches[i])
	}
	return sizes
}
