package game

import (
	"errors"
	"testing"
)

func TestDecodeAddAIRequest(t *testing.T) {
	seat := 4
	tests := []struct {
		name    string
		payload string
		want    AddAIRequest
		wantErr bool
	}{
		{name: "empty"},
		{name: "null", payload: "null"},
		{name: "object", payload: `{"seatIndex":4,"restorePlayerID":"ai_002"}`, want: AddAIRequest{
			SeatIndex:       &seat,
			RestorePlayerID: "ai_002",
		}},
		{name: "invalid", payload: `{`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeAddAIRequest([]byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Fatalf("DecodeAddAIRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got.RestorePlayerID != tt.want.RestorePlayerID {
				t.Fatalf("RestorePlayerID = %q, want %q", got.RestorePlayerID, tt.want.RestorePlayerID)
			}
			if (got.SeatIndex == nil) != (tt.want.SeatIndex == nil) {
				t.Fatalf("SeatIndex = %v, want %v", got.SeatIndex, tt.want.SeatIndex)
			}
			if got.SeatIndex != nil && *got.SeatIndex != *tt.want.SeatIndex {
				t.Fatalf("SeatIndex = %d, want %d", *got.SeatIndex, *tt.want.SeatIndex)
			}
		})
	}
}

func TestNextAIIdentityUsesSmallestAvailableOrdinal(t *testing.T) {
	inUse := map[string]bool{"ai_001": true, "ai_003": true}
	identity := NextAIIdentity(func(playerID string) bool {
		return inUse[playerID]
	}, []string{"摩根", "诺兰", "薇拉"})

	if identity.PlayerID != "ai_002" || identity.DisplayName != "诺兰" || identity.Ordinal != 2 {
		t.Fatalf("NextAIIdentity() = %+v", identity)
	}
}

func TestResolveAIIdentity(t *testing.T) {
	names := []string{"摩根", "诺兰"}

	identity, err := ResolveAIIdentity("ai_002", nil, names)
	if err != nil {
		t.Fatal(err)
	}
	if identity.PlayerID != "ai_002" || identity.DisplayName != "诺兰" || identity.Ordinal != 2 {
		t.Fatalf("ResolveAIIdentity() = %+v", identity)
	}

	_, err = ResolveAIIdentity("ai_2", nil, names)
	if !errors.Is(err, ErrInvalidAIPlayerID) {
		t.Fatalf("legacy ID error = %v, want ErrInvalidAIPlayerID", err)
	}

	_, err = ResolveAIIdentity("ai_000", nil, names)
	if !errors.Is(err, ErrInvalidAIPlayerID) {
		t.Fatalf("zero ordinal error = %v, want ErrInvalidAIPlayerID", err)
	}

	_, err = ResolveAIIdentity("ai_002", func(playerID string) bool {
		return playerID == "ai_002"
	}, names)
	if !errors.Is(err, ErrAIPlayerIDInUse) {
		t.Fatalf("duplicate ID error = %v, want ErrAIPlayerIDInUse", err)
	}
}

func TestAIName(t *testing.T) {
	if name, ok := AIName("ai_001", []string{"青岸"}); !ok || name != "青岸" {
		t.Fatalf("AIName(ai_001) = %q, %v", name, ok)
	}
	if name, ok := AIName("ai_002", []string{"青岸"}); !ok || name != "AI 002" {
		t.Fatalf("AIName(ai_002) = %q, %v", name, ok)
	}
	for _, playerID := range []string{"", "ai_1", "AI_001", "ai_000", "ai_1000"} {
		if name, ok := AIName(playerID, nil); ok || name != "" {
			t.Fatalf("AIName(%q) = %q, %v", playerID, name, ok)
		}
	}
}
