package game

import (
	"sort"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
)

// CurrentMergeSettlePlayer returns the first pending shareholder clockwise from
// the player who triggered the merger.
func CurrentMergeSettlePlayer(room *domain.Room) string {
	if room == nil || room.State == nil || room.State.RoomStatus != domain.RoomStatusMergingSettle {
		return ""
	}

	pending := make(map[string]struct{})
	for _, settlement := range room.State.MergeSettleData {
		for _, playerID := range settlement.Hoders {
			if playerID != "" {
				pending[playerID] = struct{}{}
			}
		}
	}
	if len(pending) == 0 {
		return ""
	}

	if len(room.PlayerSeq) > 0 {
		start := 0
		for index, playerID := range room.PlayerSeq {
			if playerID == room.State.CurrentPlayer {
				start = index
				break
			}
		}
		for offset := 0; offset < len(room.PlayerSeq); offset++ {
			playerID := room.PlayerSeq[(start+offset)%len(room.PlayerSeq)]
			if _, ok := pending[playerID]; ok {
				return playerID
			}
		}
	}

	// Legacy snapshots can lack PlayerSeq. Keep recovery deterministic.
	players := make([]string, 0, len(pending))
	for playerID := range pending {
		players = append(players, playerID)
	}
	sort.Strings(players)
	return players[0]
}

func IsPendingMergeSettlePlayer(room *domain.Room, playerID string) bool {
	if room == nil || room.State == nil || room.State.RoomStatus != domain.RoomStatusMergingSettle {
		return false
	}
	for _, settlement := range room.State.MergeSettleData {
		for _, pendingPlayerID := range settlement.Hoders {
			if pendingPlayerID == playerID {
				return true
			}
		}
	}
	return false
}
