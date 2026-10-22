package davinci

import (
	"context"
	"errors"
	"sort"

	"github.com/nciyuan9264/game-platform-server/internal/contracts"
	"github.com/nciyuan9264/game-platform-server/internal/game"
)

type AIPlanner struct{}

func NewAIPlanner() *AIPlanner { return &AIPlanner{} }

func (p *AIPlanner) Decide(ctx context.Context, req game.AIRequest) (contracts.RoomCommand, error) {
	state, err := asState(req.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	switch state.Status {
	case StatusGetCard:
		if len(state.Deck) == 0 {
			return contracts.RoomCommand{}, errors.New("deck empty")
		}
		return contracts.RoomCommand{Type: CommandGetCard, PlayerID: req.PlayerID, Payload: map[string]any{"id": state.Deck[0].ID}}, nil
	case StatusGuessCard:
		return guessFromPublicInfo(state, req.PlayerID)
	case StatusSetCard:
		return setPendingCard(state, req.PlayerID)
	}
	return contracts.RoomCommand{}, errors.New("no ai action")
}

func guessFromPublicInfo(state *State, playerID string) (contracts.RoomCommand, error) {
	known := knownNumbers(state, playerID)
	playerIDs := make([]string, 0, len(state.Players))
	for targetID := range state.Players {
		if targetID != playerID {
			playerIDs = append(playerIDs, targetID)
		}
	}
	sort.Strings(playerIDs)

	for _, targetID := range playerIDs {
		target := state.Players[targetID]
		if target == nil {
			continue
		}
		cards := append([]Card(nil), target.Cards...)
		sort.Slice(cards, func(i, j int) bool {
			if cards[i].Index != cards[j].Index {
				return cards[i].Index < cards[j].Index
			}
			return cards[i].ID < cards[j].ID
		})
		for _, card := range cards {
			if card.IsRevealed {
				continue
			}
			guess, ok := firstAvailableNumber(card.Color, known)
			if !ok {
				continue
			}
			return contracts.RoomCommand{
				Type:     CommandGuessCard,
				PlayerID: playerID,
				Payload: map[string]any{
					"id":  card.ID,
					"num": guess,
				},
			}, nil
		}
	}
	return contracts.RoomCommand{}, errors.New("no hidden opponent card")
}

func knownNumbers(state *State, playerID string) map[int]map[int]bool {
	known := map[int]map[int]bool{
		0: {},
		1: {},
	}
	for id, player := range state.Players {
		if player == nil {
			continue
		}
		for _, card := range player.Cards {
			if id == playerID || card.IsRevealed {
				known[card.Color][card.Num] = true
			}
		}
	}
	return known
}

func firstAvailableNumber(color int, known map[int]map[int]bool) (int, bool) {
	for num := NumMinus1; num <= 11; num++ {
		if !known[color][num] {
			return num, true
		}
	}
	return 0, false
}

func setPendingCard(state *State, playerID string) (contracts.RoomCommand, error) {
	player := state.Players[playerID]
	if player == nil || len(player.Cards) == 0 {
		return contracts.RoomCommand{}, errors.New("player cards are required")
	}
	cardIndex := -1
	for i := range player.Cards {
		if player.Cards[i].Index == -1 {
			cardIndex = i
			break
		}
	}
	if cardIndex == -1 {
		cardIndex = 0
	}
	card := player.Cards[cardIndex]
	return contracts.RoomCommand{
		Type:     CommandSetCard,
		PlayerID: playerID,
		Payload: map[string]any{
			"id":    card.ID,
			"index": computeAIInsertIndex(player.Cards, cardIndex),
		},
	}, nil
}

func computeAIInsertIndex(cards []Card, newCardIndex int) int {
	newCard := cards[newCardIndex]
	count := 0
	pos := -1
	for i := range cards {
		card := cards[i]
		if i == newCardIndex || card.ID == newCard.ID {
			continue
		}
		count++
		if card.Num == NumMinus1 {
			continue
		}
		if cardOrderLess(newCard.Num, newCard.Color, card.Num, card.Color) {
			if pos == -1 || card.Index < pos {
				pos = card.Index
			}
		}
	}
	if pos == -1 {
		return count
	}
	return pos
}
