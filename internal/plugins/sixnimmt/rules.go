package sixnimmt

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	cardCount      = 104
	cardsPerPlayer = 10
	rowCount       = 4
	rowCapacity    = 5
	targetPenalty  = 66
)

func bullheads(card int) int {
	switch {
	case card == 55:
		return 7
	case card%11 == 0:
		return 5
	case card%10 == 0:
		return 3
	case card%5 == 0:
		return 2
	default:
		return 1
	}
}

func rowPenalty(row []int) int {
	total := 0
	for _, card := range row {
		total += bullheads(card)
	}
	return total
}

func rowViews(rows [][]int) []RowView {
	result := make([]RowView, len(rows))
	for index, row := range rows {
		result[index] = RowView{
			Index:     index,
			Cards:     append([]int(nil), row...),
			Bullheads: rowPenalty(row),
		}
	}
	return result
}

func targetRow(rows [][]int, card int) int {
	bestIndex := -1
	bestTail := -1
	for index, row := range rows {
		if len(row) == 0 {
			continue
		}
		tail := row[len(row)-1]
		if tail < card && tail > bestTail {
			bestIndex = index
			bestTail = tail
		}
	}
	return bestIndex
}

func cheapestRow(rows [][]int) int {
	best := 0
	for index := 1; index < len(rows); index++ {
		if rowPenalty(rows[index]) < rowPenalty(rows[best]) {
			best = index
		}
	}
	return best
}

func nextRandom(state *State, limit int) int {
	if limit <= 1 {
		return 0
	}
	state.RandomCounter++
	value := state.RandomSeed + state.RandomCounter*0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	value ^= value >> 31
	return int(value % uint64(limit))
}

func shuffle(state *State, cards []int) {
	for index := len(cards) - 1; index > 0; index-- {
		other := nextRandom(state, index+1)
		cards[index], cards[other] = cards[other], cards[index]
	}
}

func setupRound(state *State, roundNo int) error {
	if len(state.PlayerOrder) < 2 || len(state.PlayerOrder) > defaultMaxPlayers {
		return errors.New("sixnimmt requires two to ten players")
	}
	deck := make([]int, cardCount)
	for index := range deck {
		deck[index] = index + 1
	}
	shuffle(state, deck)
	cursor := 0
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			return errors.New("player order contains an empty seat")
		}
		player.Hand = append([]int(nil), deck[cursor:cursor+cardsPerPlayer]...)
		sort.Ints(player.Hand)
		cursor += cardsPerPlayer
		player.Taken = []int{}
		player.RoundPenalty = 0
		player.LockedCard = nil
		player.AutomationKind = ""
	}
	initial := append([]int(nil), deck[cursor:cursor+rowCount]...)
	cursor += rowCount
	sort.Ints(initial)
	state.Rows = make([][]int, rowCount)
	for index, card := range initial {
		state.Rows[index] = []int{card}
	}
	state.UnusedCards = append([]int(nil), deck[cursor:]...)
	state.RoundNo = roundNo
	state.TurnNo = 1
	state.Queue = []Selection{}
	state.QueueCursor = 0
	state.LastResolution = nil
	state.PendingChoice = nil
	state.RoundResult = nil
	state.Result = nil
	state.Status = StatusSelecting
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	state.RoundTransitionDeadline = time.Time{}
	appendAction(state, Action{
		Kind:    "roundStart",
		Summary: fmt.Sprintf("第 %d 局开始。", roundNo),
	})
	return nil
}

func lockCard(
	state *State,
	playerID string,
	card int,
	automationKind string,
) error {
	if state.Status != StatusSelecting {
		return errors.New("cards cannot be locked in the current phase")
	}
	player := state.Roster[playerID]
	if player == nil {
		return errors.New("player not found")
	}
	if player.LockedCard != nil {
		return errors.New("player already locked a card")
	}
	if !containsInt(player.Hand, card) {
		return errors.New("card is not in player hand")
	}
	value := card
	player.LockedCard = &value
	player.AutomationKind = automationKind
	if !allPlayersLocked(state) {
		return nil
	}
	return revealAndResolve(state)
}

func unlockCard(state *State, playerID string) error {
	if state.Status != StatusSelecting {
		return errors.New("locked card can no longer be changed")
	}
	player := state.Roster[playerID]
	if player == nil || player.LockedCard == nil {
		return errors.New("player has no locked card")
	}
	player.LockedCard = nil
	player.AutomationKind = ""
	return nil
}

func allPlayersLocked(state *State) bool {
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil || player.LockedCard == nil {
			return false
		}
	}
	return len(state.PlayerOrder) > 0
}

func revealAndResolve(state *State) error {
	state.Queue = make([]Selection, 0, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil || player.LockedCard == nil {
			return errors.New("all players must lock a card")
		}
		card := *player.LockedCard
		player.Hand = removeInt(player.Hand, card)
		state.Queue = append(state.Queue, Selection{
			PlayerID:       playerID,
			Card:           card,
			AutomationKind: player.AutomationKind,
		})
		player.LockedCard = nil
		player.AutomationKind = ""
	}
	sort.SliceStable(state.Queue, func(i, j int) bool {
		return state.Queue[i].Card < state.Queue[j].Card
	})
	state.QueueCursor = 0
	state.LastResolution = &TurnResolution{
		ResolutionID: fmt.Sprintf(
			"r%d-t%d-%d",
			state.RoundNo,
			state.TurnNo,
			len(state.Log)+1,
		),
		RoundNo:    state.RoundNo,
		TurnNo:     state.TurnNo,
		RowsBefore: rowViews(state.Rows),
		Selections: append([]Selection(nil), state.Queue...),
		Placements: []Placement{},
		RowsAfter:  rowViews(state.Rows),
	}
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	return resolveQueue(state)
}

func resolveQueue(state *State) error {
	for state.QueueCursor < len(state.Queue) {
		selection := state.Queue[state.QueueCursor]
		rowIndex := targetRow(state.Rows, selection.Card)
		if rowIndex < 0 && selection.AutomationKind == "" {
			state.Status = StatusChooseRow
			state.PendingChoice = &PendingChoice{
				PlayerID:     selection.PlayerID,
				Card:         selection.Card,
				RowPenalties: rowPenaltyList(state.Rows),
			}
			state.LastResolution.RowsAfter = rowViews(state.Rows)
			state.LastResolution.Complete = false
			state.TurnDeadline = time.Time{}
			state.TurnTimeout = 0
			return nil
		}
		kind := "place"
		if rowIndex < 0 {
			rowIndex = cheapestRow(state.Rows)
			kind = "low"
		} else if len(state.Rows[rowIndex]) == rowCapacity {
			kind = "sixth"
		}
		applyPlacement(state, selection, rowIndex, kind)
	}
	return finishTurn(state)
}

func chooseRow(
	state *State,
	playerID string,
	rowIndex int,
	automatic bool,
) error {
	if state.Status != StatusChooseRow ||
		state.PendingChoice == nil ||
		state.PendingChoice.PlayerID != playerID {
		return errors.New("player is not choosing a row")
	}
	if rowIndex < 0 || rowIndex >= len(state.Rows) {
		return errors.New("rowIndex is out of range")
	}
	selection := state.Queue[state.QueueCursor]
	if automatic {
		selection.AutomationKind = AutomationTimeout
		state.Queue[state.QueueCursor].AutomationKind = AutomationTimeout
	}
	applyPlacement(state, selection, rowIndex, "low")
	state.PendingChoice = nil
	state.Status = StatusSelecting
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	return resolveQueue(state)
}

func applyPlacement(
	state *State,
	selection Selection,
	rowIndex int,
	kind string,
) {
	collected := []int{}
	penalty := 0
	if kind == "place" {
		state.Rows[rowIndex] = append(state.Rows[rowIndex], selection.Card)
	} else {
		collected = append([]int(nil), state.Rows[rowIndex]...)
		penalty = rowPenalty(collected)
		state.Rows[rowIndex] = []int{selection.Card}
		player := state.Roster[selection.PlayerID]
		player.Taken = append(player.Taken, collected...)
		player.RoundPenalty += penalty
		player.TotalPenalty += penalty
	}
	placement := Placement{
		PlayerID:       selection.PlayerID,
		Card:           selection.Card,
		RowIndex:       rowIndex,
		Kind:           kind,
		CollectedCards: collected,
		Penalty:        penalty,
		AutomationKind: selection.AutomationKind,
	}
	state.LastResolution.Placements = append(
		state.LastResolution.Placements,
		placement,
	)
	state.QueueCursor++
	card := selection.Card
	row := rowIndex
	points := penalty
	appendAction(state, Action{
		Kind:           kind,
		PlayerID:       selection.PlayerID,
		Card:           &card,
		RowIndex:       &row,
		Penalty:        &points,
		AutomationKind: selection.AutomationKind,
		Summary: fmt.Sprintf(
			"%s 打出 %d，进入第 %d 行。",
			selection.PlayerID,
			selection.Card,
			rowIndex+1,
		),
	})
	state.LastResolution.RowsAfter = rowViews(state.Rows)
}

func finishTurn(state *State) error {
	if state.LastResolution != nil {
		state.LastResolution.RowsAfter = rowViews(state.Rows)
		state.LastResolution.Complete = true
	}
	state.Queue = []Selection{}
	state.QueueCursor = 0
	state.PendingChoice = nil
	if state.TurnNo >= cardsPerPlayer {
		return finishRound(state)
	}
	state.TurnNo++
	state.Status = StatusSelecting
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	return nil
}

func finishRound(state *State) error {
	roundPenalties := map[string]int{}
	totalPenalties := map[string]int{}
	thresholdReached := false
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		roundPenalties[playerID] = player.RoundPenalty
		totalPenalties[playerID] = player.TotalPenalty
		if player.TotalPenalty >= targetPenalty {
			thresholdReached = true
		}
	}
	state.RoundResult = &RoundResult{
		RoundNo:          state.RoundNo,
		RoundPenalties:   roundPenalties,
		TotalPenalties:   totalPenalties,
		ThresholdReached: thresholdReached,
	}
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	if !thresholdReached {
		state.Status = StatusRoundEnd
		appendAction(state, Action{
			Kind:    "roundEnd",
			Summary: fmt.Sprintf("第 %d 局结束。", state.RoundNo),
		})
		return nil
	}
	state.Status = StatusEnd
	winners, ranks := finalRanks(state)
	state.Result = &Result{
		WinnerIDs:      winners,
		TotalPenalties: totalPenalties,
		FinalRanks:     ranks,
		RoundNo:        state.RoundNo,
		Reason:         "penalty",
	}
	appendAction(state, Action{
		Kind:     "gameEnd",
		PlayerID: firstString(winners),
		Summary:  "累计牛头达到终局条件。",
	})
	return nil
}

func finalRanks(state *State) ([]string, map[string]int) {
	order := append([]string(nil), state.PlayerOrder...)
	sort.SliceStable(order, func(i, j int) bool {
		left := state.Roster[order[i]]
		right := state.Roster[order[j]]
		if left.TotalPenalty != right.TotalPenalty {
			return left.TotalPenalty < right.TotalPenalty
		}
		return left.SeatIndex < right.SeatIndex
	})
	ranks := map[string]int{}
	rank := 1
	for index, playerID := range order {
		if index > 0 &&
			state.Roster[playerID].TotalPenalty >
				state.Roster[order[index-1]].TotalPenalty {
			rank = index + 1
		}
		ranks[playerID] = rank
	}
	winners := []string{}
	for _, playerID := range order {
		if ranks[playerID] == 1 {
			winners = append(winners, playerID)
		}
	}
	return winners, ranks
}

func nextRound(state *State, actorID string, automatic bool) error {
	if state.Status != StatusRoundEnd || state.RoundResult == nil {
		return errors.New("round is not ready to advance")
	}
	if !automatic && actorID != state.OwnerID {
		return errors.New("only the owner can advance the round")
	}
	return setupRound(state, state.RoundNo+1)
}

func rowPenaltyList(rows [][]int) []int {
	result := make([]int, len(rows))
	for index, row := range rows {
		result[index] = rowPenalty(row)
	}
	return result
}

func appendAction(state *State, action Action) {
	action.ID = len(state.Log) + 1
	action.RoundNo = state.RoundNo
	action.TurnNo = state.TurnNo
	state.Log = append(state.Log, cloneAction(action))
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func removeInt(values []int, target int) []int {
	result := make([]int, 0, len(values)-1)
	removed := false
	for _, value := range values {
		if !removed && value == target {
			removed = true
			continue
		}
		result = append(result, value)
	}
	return result
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
