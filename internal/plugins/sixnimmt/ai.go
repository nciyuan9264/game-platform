package sixnimmt

import (
	"context"
	"errors"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

type aiObservation struct {
	Hand []int
	Rows [][]int
}

func aiObservationFor(state *State, playerID string) aiObservation {
	player := state.Roster[playerID]
	hand := []int{}
	if player != nil {
		hand = append(hand, player.Hand...)
	}
	return aiObservation{
		Hand: hand,
		Rows: cloneRows(state.Rows),
	}
}

func (p *Plugin) Decide(
	_ context.Context,
	request game.AIRequest,
) (contracts.RoomCommand, error) {
	state, err := asState(request.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	player := state.Roster[request.PlayerID]
	if player == nil || !player.AI {
		return contracts.RoomCommand{}, errors.New("AI player is unavailable")
	}
	if state.Status != StatusSelecting || player.LockedCard != nil {
		return contracts.RoomCommand{}, errors.New(
			"AI has no action in current phase",
		)
	}
	card, err := bestCard(aiObservationFor(state, request.PlayerID))
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	return contracts.RoomCommand{
		Type:     CommandLockCard,
		PlayerID: request.PlayerID,
		Payload:  map[string]any{"card": card},
	}, nil
}

func bestCard(view aiObservation) (int, error) {
	if len(view.Hand) == 0 {
		return 0, errors.New("no card is available")
	}
	type candidate struct {
		card  int
		score float64
	}
	candidates := make([]candidate, 0, len(view.Hand))
	for _, card := range view.Hand {
		rowIndex := targetRow(view.Rows, card)
		score := float64(bullheads(card)) * 0.08
		if rowIndex < 0 {
			score += float64(rowPenalty(view.Rows[cheapestRow(view.Rows)])) + 4
		} else {
			row := view.Rows[rowIndex]
			gap := card - row[len(row)-1]
			score += float64(gap) / 45
			switch len(row) {
			case 5:
				score += float64(rowPenalty(row)) + 3
			case 4:
				score += 4
			case 3:
				score += 1.5
			}
		}
		candidates = append(candidates, candidate{card: card, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}
		return candidates[i].card < candidates[j].card
	})
	return candidates[0].card, nil
}
