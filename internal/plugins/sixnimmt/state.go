package sixnimmt

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func validateState(state *State) error {
	if state.RandomSeed == 0 {
		return errors.New("sixnimmt state is missing random seed")
	}
	if state.MaxPlayers != defaultMaxPlayers {
		return errors.New("sixnimmt state must expose ten seats")
	}
	if state.Visibility != contracts.VisibilityPublic &&
		state.Visibility != contracts.VisibilityPrivate {
		return errors.New("sixnimmt state has invalid visibility")
	}
	switch state.Status {
	case StatusMatch, StatusWaiting, StatusSelecting, StatusChooseRow,
		StatusRoundEnd, StatusEnd:
	default:
		return errors.New("sixnimmt state has invalid status")
	}
	if len(state.PlayerOrder) > state.MaxPlayers {
		return errors.New("sixnimmt roster exceeds room capacity")
	}
	seenSeats := map[int]bool{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil ||
			player.PlayerID != playerID ||
			player.SeatIndex < 0 ||
			player.SeatIndex >= state.MaxPlayers ||
			seenSeats[player.SeatIndex] {
			return errors.New("sixnimmt roster is inconsistent")
		}
		seenSeats[player.SeatIndex] = true
		if player.LockedCard != nil &&
			!containsInt(player.Hand, *player.LockedCard) {
			return errors.New("locked card is not in player hand")
		}
	}
	if state.Status != StatusMatch && state.Status != StatusWaiting {
		if len(state.PlayerOrder) < 2 {
			return errors.New("sixnimmt game has too few players")
		}
		if err := validateCardConservation(state); err != nil {
			return err
		}
	}
	if state.Status == StatusChooseRow &&
		(state.PendingChoice == nil ||
			state.QueueCursor < 0 ||
			state.QueueCursor >= len(state.Queue)) {
		return errors.New("sixnimmt row choice is inconsistent")
	}
	return nil
}

func validateCardConservation(state *State) error {
	cards := append([]int(nil), state.UnusedCards...)
	for _, row := range state.Rows {
		cards = append(cards, row...)
	}
	for _, player := range state.Roster {
		if player == nil {
			continue
		}
		cards = append(cards, player.Hand...)
		cards = append(cards, player.Taken...)
	}
	if state.Status == StatusChooseRow {
		for index := state.QueueCursor; index < len(state.Queue); index++ {
			cards = append(cards, state.Queue[index].Card)
		}
	}
	if len(cards) != cardCount {
		return fmt.Errorf("card conservation failed: got %d cards", len(cards))
	}
	seen := make([]bool, cardCount+1)
	for _, card := range cards {
		if card < 1 || card > cardCount || seen[card] {
			return errors.New(
				"card conservation contains invalid or duplicate cards",
			)
		}
		seen[card] = true
	}
	return nil
}

func decodePayload(data []byte, target any) error {
	if len(data) == 0 || string(data) == "null" {
		return errors.New("payload is required")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("invalid command payload")
	}
	return nil
}

func firstFreeSeat(state *State) int {
	for index := 0; index < state.MaxPlayers; index++ {
		if !seatOccupied(state, index) {
			return index
		}
	}
	return len(state.Roster)
}

func seatOccupied(state *State, seat int) bool {
	for _, player := range state.Roster {
		if player != nil && player.SeatIndex == seat {
			return true
		}
	}
	return false
}

func sortPlayerOrder(state *State) {
	sort.SliceStable(state.PlayerOrder, func(i, j int) bool {
		return state.Roster[state.PlayerOrder[i]].SeatIndex <
			state.Roster[state.PlayerOrder[j]].SeatIndex
	})
}

func removeString(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func deadlineValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func roundResultView(value *RoundResult) any {
	if value == nil {
		return nil
	}
	result := map[string]any{
		"roundNo":          value.RoundNo,
		"roundPenalties":   cloneIntMap(value.RoundPenalties),
		"totalPenalties":   cloneIntMap(value.TotalPenalties),
		"thresholdReached": value.ThresholdReached,
	}
	if !value.NextRoundDeadline.IsZero() {
		result["nextRoundDeadline"] = value.NextRoundDeadline.UTC().
			Format(time.RFC3339Nano)
	}
	return result
}

func clonePendingChoice(value *PendingChoice) any {
	if value == nil {
		return nil
	}
	return map[string]any{
		"playerID":     value.PlayerID,
		"card":         value.Card,
		"rowPenalties": append([]int(nil), value.RowPenalties...),
	}
}

func aiName(seat int) string {
	names := []string{
		"阿麦", "栗子", "小野", "北北", "松果",
		"团团", "南风", "阿丘", "豆子", "小满",
	}
	if seat >= 0 && seat < len(names) {
		return names[seat]
	}
	return fmt.Sprintf("AI %d", seat+1)
}
