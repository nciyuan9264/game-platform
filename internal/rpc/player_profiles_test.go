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
			PlayerID:    "7",
			DisplayName: "玩家 7",
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
}
