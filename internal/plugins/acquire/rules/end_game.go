package game

import (
	"encoding/json"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/plugins/acquire/domain/domain"
)

const (
	EndConditionCompanySize      = "company_size"
	EndConditionAllCompaniesSafe = "all_companies_safe"

	acquireBoardTileCount = 108
)

type EndVotePayload struct {
	Allow *bool `json:"allow"`
}

type endCompanyCandidate struct {
	Name  string
	Tiles int
}

func ParseEndVotePayload(payload []byte) (bool, bool) {
	var request EndVotePayload
	if json.Unmarshal(payload, &request) != nil || request.Allow == nil {
		return false, false
	}
	return *request.Allow, true
}

func HandleEndVoteMessage(room *domain.Room, command domain.Command) {
	if room == nil || room.State == nil || !room.State.EndGame.Eligible {
		return
	}
	allow, ok := ParseEndVotePayload(command.Payload)
	if !ok {
		return
	}
	ensureEndVotes(room)
	room.State.EndGame.Votes[command.PlayerID] = allow
}

// RefreshEndGameEligibility latches the first attained end condition.
func RefreshEndGameEligibility(room *domain.Room, companyTiles map[string]int) {
	if room == nil || room.State == nil {
		return
	}
	if room.State.EndGame.Eligible {
		ensureEndVotes(room)
		return
	}

	candidates := make([]endCompanyCandidate, 0, len(companyTiles))
	allEstablishedSafe := len(companyTiles) > 0
	for name, tiles := range companyTiles {
		if tiles >= 41 {
			candidates = append(candidates, endCompanyCandidate{Name: name, Tiles: tiles})
		}
		if tiles < 11 {
			allEstablishedSafe = false
		}
	}

	end := domain.EndGameState{}
	if len(candidates) > 0 {
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].Tiles != candidates[j].Tiles {
				return candidates[i].Tiles > candidates[j].Tiles
			}
			return candidates[i].Name < candidates[j].Name
		})
		end.Eligible = true
		end.Condition = EndConditionCompanySize
		end.Company = candidates[0].Name
		end.TileCount = candidates[0].Tiles
	} else if allEstablishedSafe {
		end.Eligible = true
		end.Condition = EndConditionAllCompaniesSafe
	}
	if !end.Eligible {
		return
	}

	end.Votes = make(map[string]bool, len(room.Connections))
	for playerID, player := range room.Connections {
		end.Votes[playerID] = player != nil && player.AI
	}
	room.State.EndGame = end
}

func AllEndVotesApproved(room *domain.Room) bool {
	if room == nil || room.State == nil || !room.State.EndGame.Eligible {
		return false
	}
	for playerID, player := range room.Connections {
		if player != nil && !player.AI && !room.State.EndGame.Votes[playerID] {
			return false
		}
	}
	return true
}

func IsBoardFull(room *domain.Room) bool {
	if room == nil || room.State == nil || len(room.State.BoardTiles) < acquireBoardTileCount {
		return false
	}
	placed := 0
	for _, tile := range room.State.BoardTiles {
		if tile != nil && tile.Belong != "" {
			placed++
		}
	}
	return placed >= acquireBoardTileCount
}

func ShouldFinalizeEnd(room *domain.Room, turnCompleted bool) bool {
	if !turnCompleted || room == nil || room.State == nil || room.State.RoomStatus == domain.RoomStatusEnd {
		return false
	}
	return IsBoardFull(room) || AllEndVotesApproved(room)
}

func ensureEndVotes(room *domain.Room) {
	if room.State.EndGame.Votes == nil {
		room.State.EndGame.Votes = make(map[string]bool, len(room.Connections))
	}
	for playerID, player := range room.Connections {
		if _, exists := room.State.EndGame.Votes[playerID]; !exists {
			room.State.EndGame.Votes[playerID] = player != nil && player.AI
		} else if player != nil && player.AI {
			room.State.EndGame.Votes[playerID] = true
		}
	}
}
