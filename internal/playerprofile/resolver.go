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
	IsAI        bool
}

type Resolver struct {
	profiles authclient.PublicProfileDirectory
}

func NewResolver(profiles authclient.PublicProfileDirectory) *Resolver {
	return &Resolver{profiles: profiles}
}

func (r *Resolver) Resolve(ctx context.Context, players []Player) map[string]string {
	displayNames := make(map[string]string, len(players))
	playerIDsByUserID := make(map[string][]string)
	userIDs := make([]string, 0, len(players))
	seenUserIDs := make(map[string]struct{}, len(players))

	for _, player := range players {
		if player.PlayerID == "" {
			continue
		}
		displayNames[player.PlayerID] = fallbackDisplayName(player)
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
		return displayNames
	}
	for start := 0; start < len(userIDs); start += maxBatchSize {
		end := min(start+maxBatchSize, len(userIDs))
		batch := userIDs[start:end]
		profiles, err := r.profiles.BatchGetPublicProfiles(ctx, batch)
		if err != nil {
			continue
		}
		for _, userID := range batch {
			displayName := strings.TrimSpace(profiles[userID])
			if displayName == "" {
				displayName = DeletedUserDisplayName
			}
			for _, playerID := range playerIDsByUserID[userID] {
				displayNames[playerID] = displayName
			}
		}
	}
	return displayNames
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
