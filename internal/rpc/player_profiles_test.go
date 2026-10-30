package rpc

import (
	"context"
	"testing"

	platform "github.com/nciyuan9264/game-platform-client/kitex_gen/pam/game/platform/v1"
	"github.com/nciyuan9264/game-platform/internal/authclient"
	"github.com/nciyuan9264/game-platform/internal/playerprofile"
)

type fakeRoomProfileDirectory struct {
	profiles  map[string]authclient.PublicProfile
	requested []string
}

func (f *fakeRoomProfileDirectory) BatchGetPublicProfiles(
	_ context.Context,
	userIDs []string,
) (map[string]authclient.PublicProfile, error) {
	f.requested = append(f.requested, userIDs...)
	return f.profiles, nil
}

func TestResolveRoomProfilesUsesRoomOwnerIdentityForLegacyPlayers(t *testing.T) {
	directory := &fakeRoomProfileDirectory{
		profiles: map[string]authclient.PublicProfile{
			"7": {
				DisplayName: "Alice",
				Avatar:      "https://cdn.example.com/alice.png",
			},
		},
	}
	handler := &Handler{profiles: playerprofile.NewResolver(directory)}
	room := &platform.RoomInfo{
		OwnerUserID:   "7",
		OwnerPlayerID: "7",
		Players: []*platform.RoomPlayer{{
			UserID:      stringPointer("legacy-owner"),
			PlayerID:    "7",
			DisplayName: "玩家 7",
			Online:      true,
		}, {
			UserID:      stringPointer("legacy-guest"),
			PlayerID:    "legacy-guest",
			DisplayName: "Persisted Guest",
			Online:      true,
		}},
	}

	handler.resolveRoomProfiles(context.Background(), []*platform.RoomInfo{room})

	owner := room.GetPlayers()[0]
	if owner.GetUserID() != "7" ||
		owner.GetDisplayName() != "Alice" ||
		owner.GetAvatar() != "https://cdn.example.com/alice.png" {
		t.Fatalf("owner profile = %+v", owner)
	}
	if len(directory.requested) != 1 || directory.requested[0] != "7" {
		t.Fatalf("requested user IDs = %v", directory.requested)
	}
	if room.GetPlayers()[1].GetDisplayName() != "Persisted Guest" {
		t.Fatalf("legacy guest profile = %+v", room.GetPlayers()[1])
	}
}

func TestResolveRoomProfilesAddsMissingOwnerToUnstartedRoom(t *testing.T) {
	directory := &fakeRoomProfileDirectory{
		profiles: map[string]authclient.PublicProfile{
			"7": {DisplayName: "Alice"},
		},
	}
	handler := &Handler{profiles: playerprofile.NewResolver(directory)}
	room := &platform.RoomInfo{
		OwnerUserID:   "7",
		OwnerPlayerID: "7",
	}

	handler.resolveRoomProfiles(context.Background(), []*platform.RoomInfo{room})

	if len(room.GetPlayers()) != 1 || room.GetPlayerCount() != 1 {
		t.Fatalf("players = %+v, player count = %d", room.GetPlayers(), room.GetPlayerCount())
	}
	owner := room.GetPlayers()[0]
	if owner.GetUserID() != "7" ||
		owner.GetPlayerID() != "7" ||
		owner.GetDisplayName() != "Alice" {
		t.Fatalf("owner profile = %+v", owner)
	}
}
