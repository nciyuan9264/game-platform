package loveletter

import (
	"context"
	"errors"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) Decide(
	_ context.Context,
	request game.AIRequest,
) (contracts.RoomCommand, error) {
	state, err := asState(request.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	if state.CurrentPlayer != request.PlayerID {
		return contracts.RoomCommand{}, errors.New("AI is not the active player")
	}
	switch state.Status {
	case StatusDraw:
		return contracts.RoomCommand{
			Type:     CommandDrawCard,
			PlayerID: request.PlayerID,
		}, nil
	case StatusPlay:
		cardID, targetID, guess, err := choosePlay(state, request.PlayerID)
		if err != nil {
			return contracts.RoomCommand{}, err
		}
		payload := map[string]any{"cardID": cardID}
		if targetID != "" {
			payload["targetPlayerID"] = targetID
		}
		if guess != nil {
			payload["guessRank"] = *guess
		}
		return contracts.RoomCommand{
			Type:     CommandPlayCard,
			PlayerID: request.PlayerID,
			Payload:  payload,
		}, nil
	case StatusChancellor:
		keep, bottom, err := chooseChancellor(state, request.PlayerID)
		if err != nil {
			return contracts.RoomCommand{}, err
		}
		return contracts.RoomCommand{
			Type:     CommandSettleChancellor,
			PlayerID: request.PlayerID,
			Payload: map[string]any{
				"keepCardID":    keep,
				"bottomCardIDs": bottom,
			},
		}, nil
	default:
		return contracts.RoomCommand{}, errors.New("AI has no action in current phase")
	}
}

func choosePlay(state *State, playerID string) (string, string, *int, error) {
	player := state.Roster[playerID]
	if player == nil {
		return "", "", nil, errors.New("AI player is unavailable")
	}
	legalIDs := legalCardIDs(state, playerID)
	if len(legalIDs) == 0 {
		return "", "", nil, errors.New("AI has no legal card")
	}
	cards := make([]Card, 0, len(legalIDs))
	for _, cardID := range legalIDs {
		card, ok := cardByID(player, cardID)
		if ok {
			cards = append(cards, card)
		}
	}
	sort.SliceStable(cards, func(i, j int) bool {
		left, right := playPriority(cards[i].Rank), playPriority(cards[j].Rank)
		if left != right {
			return left < right
		}
		return cards[i].CardID < cards[j].CardID
	})
	card := cards[0]
	targets := legalTargets(state, playerID, card.Rank)
	targetID := chooseTarget(state, playerID, card.Rank, targets)
	var guess *int
	if card.Rank == 1 && targetID != "" {
		if known := player.KnownCards[targetID]; known.Valid && known.Rank != 1 {
			value := known.Rank
			guess = &value
		} else {
			value := likelyGuardGuess(state, playerID)
			guess = &value
		}
	}
	return card.CardID, targetID, guess, nil
}

func playPriority(rank int) int {
	switch rank {
	case 8:
		return 0
	case 0:
		return 1
	case 1:
		return 2
	case 2:
		return 3
	case 3:
		return 4
	case 5:
		return 5
	case 6:
		return 6
	case 7:
		return 7
	case 4:
		return 8
	case 9:
		return 100
	default:
		return 50
	}
}

func chooseTarget(state *State, playerID string, rank int, targets []string) string {
	if len(targets) == 0 {
		return ""
	}
	if rank == 5 {
		self := state.Roster[playerID]
		if self != nil && len(self.Hand) == 1 && self.Hand[0].Rank <= 1 &&
			containsString(targets, playerID) {
			return playerID
		}
	}
	ordered := append([]string(nil), targets...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := state.Roster[ordered[i]]
		right := state.Roster[ordered[j]]
		if left.Favors != right.Favors {
			return left.Favors > right.Favors
		}
		return left.SeatIndex < right.SeatIndex
	})
	for _, targetID := range ordered {
		if targetID != playerID {
			return targetID
		}
	}
	return ordered[0]
}

func likelyGuardGuess(state *State, playerID string) int {
	remaining := append([]int(nil), roleCounts[:]...)
	for _, card := range state.Burn {
		remaining[card.Rank]--
	}
	for _, player := range state.Roster {
		if player == nil {
			continue
		}
		for _, card := range player.Discards {
			remaining[card.Rank]--
		}
	}
	if self := state.Roster[playerID]; self != nil {
		for _, card := range self.Hand {
			remaining[card.Rank]--
		}
	}
	bestRank, bestCount := 9, -1
	for rank, count := range remaining {
		if rank == 1 {
			continue
		}
		if count > bestCount || count == bestCount && rank > bestRank {
			bestRank, bestCount = rank, count
		}
	}
	return bestRank
}

func chooseChancellor(state *State, playerID string) (string, []string, error) {
	player := state.Roster[playerID]
	if player == nil || len(player.Hand) < 2 {
		return "", nil, errors.New("AI has no chancellor choice")
	}
	cards := append([]Card(nil), player.Hand...)
	sort.SliceStable(cards, func(i, j int) bool {
		if cards[i].Rank != cards[j].Rank {
			return cards[i].Rank > cards[j].Rank
		}
		return cards[i].CardID < cards[j].CardID
	})
	bottom := make([]string, 0, len(cards)-1)
	for _, card := range cards[1:] {
		bottom = append(bottom, card.CardID)
	}
	return cards[0].CardID, bottom, nil
}
