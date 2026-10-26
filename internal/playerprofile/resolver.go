package playerprofile

import (
	"context"
	"fmt"
	"strings"

	"github.com/nciyuan9264/game-platform/internal/authclient"
)

const (
	maxBatchSize           = 200
	DeletedUserDisplayName = "已注销用户"
)

type Player struct {
	UserID      string
	PlayerID    string
	DisplayName string
	Avatar      string
	IsAI        bool
}

type ResolvedProfile struct {
	DisplayName string
	Avatar      string
}

type Resolver struct {
	profiles authclient.PublicProfileDirectory
}

func NewResolver(profiles authclient.PublicProfileDirectory) *Resolver {
	return &Resolver{profiles: profiles}
}

func (r *Resolver) Resolve(ctx context.Context, players []Player) map[string]string {
	profiles := r.ResolveProfiles(ctx, players)
	displayNames := make(map[string]string, len(profiles))
	for playerID, profile := range profiles {
		displayNames[playerID] = profile.DisplayName
	}
	return displayNames
}

func (r *Resolver) ResolveProfiles(ctx context.Context, players []Player) map[string]ResolvedProfile {
	resolved := make(map[string]ResolvedProfile, len(players))
	playerIDsByUserID := make(map[string][]string)
	userIDs := make([]string, 0, len(players))
	seenUserIDs := make(map[string]struct{}, len(players))

	for _, player := range players {
		if player.PlayerID == "" {
			continue
		}
		resolved[player.PlayerID] = ResolvedProfile{
			DisplayName: fallbackDisplayName(player),
			Avatar:      strings.TrimSpace(player.Avatar),
		}
		if player.IsAI {
			continue
		}
		userID := strings.TrimSpace(player.UserID)
		if userID == "" {
			continue
		}
		playerIDsByUserID[userID] = append(playerIDsByUserID[userID], player.PlayerID)
		if _, seen := seenUserIDs[userID]; seen {
			continue
		}
		seenUserIDs[userID] = struct{}{}
		userIDs = append(userIDs, userID)
	}

	if r == nil || r.profiles == nil {
		return resolved
	}
	for start := 0; start < len(userIDs); start += maxBatchSize {
		end := min(start+maxBatchSize, len(userIDs))
		batch := userIDs[start:end]
		profiles, err := r.profiles.BatchGetPublicProfiles(ctx, batch)
		if err != nil {
			continue
		}
		for _, userID := range batch {
			profile := profiles[userID]
			displayName := strings.TrimSpace(profile.DisplayName)
			if displayName == "" {
				displayName = DeletedUserDisplayName
			}
			for _, playerID := range playerIDsByUserID[userID] {
				resolved[playerID] = ResolvedProfile{
					DisplayName: displayName,
					Avatar:      strings.TrimSpace(profile.Avatar),
				}
			}
		}
	}
	return resolved
}

func fallbackDisplayName(player Player) string {
	if displayName := strings.TrimSpace(player.DisplayName); displayName != "" {
		return displayName
	}
	if player.IsAI {
		return "AI"
	}
	identity := strings.TrimSpace(player.UserID)
	if identity == "" {
		identity = strings.TrimSpace(player.PlayerID)
	}
	if identity == "" {
		return "玩家"
	}
	return fmt.Sprintf("玩家 %s", identity)
}
