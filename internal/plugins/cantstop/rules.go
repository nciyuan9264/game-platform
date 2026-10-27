package cantstop

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	minPlayers    = 2
	maxPlayers    = 4
	targetColumns = 3
	dicePerRoll   = 4
	diceSides     = 6
	totalOutcomes = 1296
)

var columnHeights = map[int]int{
	2: 3, 3: 5, 4: 7, 5: 9, 6: 11, 7: 13,
	8: 11, 9: 9, 10: 7, 11: 5, 12: 3,
}

var pairingIndexes = [][4]int{
	{0, 1, 2, 3},
	{0, 2, 1, 3},
	{0, 3, 1, 2},
}

type pairing struct {
	index       int
	sums        []int
	diceIndexes []int
}

func setupGame(state *State, sentAt time.Time) error {
	if len(state.PlayerOrder) < minPlayers || len(state.PlayerOrder) > maxPlayers {
		return errors.New("can't stop requires two to four players")
	}
	sortPlayerOrder(state)
	starter := firstStarter(state)
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			return errors.New("player order contains an empty seat")
		}
		player.Ready = player.AI
		player.GameReady = player.AI
		player.Saved = map[int]int{}
		player.Busts = 0
	}
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}
	state.GameStartTime = sentAt.UTC()
	state.Status = StatusRoll
	state.CurrentPlayer = starter
	state.FirstPlayer = starter
	state.NextPlayer = nextPlayer(state, starter)
	state.TurnNumber = 1
	state.RollNumber = 0
	state.TotalRolls = 0
	state.Dice = []int{}
	state.PairingOptions = []PairingOption{}
	state.Runners = map[int]int{}
	state.ClaimedBy = map[int]string{}
	state.LostGain = 0
	state.Log = []Action{}
	state.LastAction = nil
	state.Result = nil
	return nil
}

func firstStarter(state *State) string {
	if len(state.PlayerOrder) == 0 {
		return ""
	}
	base := int(state.RandomSeed % uint64(len(state.PlayerOrder)))
	offset := state.MatchRoundNo - 1
	if offset < 0 {
		offset = 0
	}
	return state.PlayerOrder[(base+offset)%len(state.PlayerOrder)]
}

func rollDice(state *State, playerID, source string) error {
	if state.CurrentPlayer != playerID {
		return errors.New("not the active player")
	}
	if state.Status != StatusRoll && state.Status != StatusDecide {
		return errors.New("dice cannot be rolled now")
	}
	dice := make([]int, dicePerRoll)
	for index := range dice {
		dice[index] = nextRandom(state, diceSides) + 1
	}
	state.Dice = dice
	state.RollNumber++
	state.TotalRolls++
	state.PairingOptions = legalPairingOptionsForDice(state, dice)
	appendAction(state, Action{
		Kind:       "roll",
		PlayerID:   playerID,
		Dice:       dice,
		Source:     source,
		TurnNumber: state.TurnNumber,
		RollNumber: state.RollNumber,
		Summary:    fmt.Sprintf("%s 掷出 %s。", playerID, diceSummary(dice)),
	})
	if len(state.PairingOptions) == 0 {
		return bust(state, playerID, source)
	}
	state.Status = StatusChoose
	return nil
}

func advance(state *State, playerID, actionID, source string) error {
	if state.Status != StatusChoose || state.CurrentPlayer != playerID {
		return errors.New("not choosing a pairing")
	}
	for _, option := range state.PairingOptions {
		if option.ActionID != actionID {
			continue
		}
		state.Runners = cloneProgress(option.RunnerPositions)
		state.PairingOptions = []PairingOption{}
		columns := sortedKeys(option.Advances)
		appendAction(state, Action{
			Kind:       "advance",
			PlayerID:   playerID,
			Dice:       append([]int(nil), state.Dice...),
			ActionID:   actionID,
			Columns:    columns,
			Gain:       option.StepCount,
			Source:     source,
			TurnNumber: state.TurnNumber,
			RollNumber: state.RollNumber,
			Summary: fmt.Sprintf(
				"%s 推进 %s。",
				playerID,
				columnList(columns),
			),
		})
		state.Status = StatusDecide
		return nil
	}
	return errors.New("actionID is stale or invalid")
}

func bank(state *State, playerID, source string) error {
	if state.Status != StatusDecide || state.CurrentPlayer != playerID {
		return errors.New("bank is not available")
	}
	player := state.Roster[playerID]
	if player == nil {
		return errors.New("player not found")
	}
	gain := turnGain(state)
	if gain <= 0 {
		return errors.New("nothing to bank")
	}
	claimed := []int{}
	for column, position := range state.Runners {
		player.Saved[column] = position
		if position >= columnHeight(column) {
			state.ClaimedBy[column] = playerID
			claimed = append(claimed, column)
			for otherID, other := range state.Roster {
				if other == nil || otherID == playerID {
					continue
				}
				delete(other.Saved, column)
			}
		}
	}
	sort.Ints(claimed)
	state.Runners = map[int]int{}
	state.Dice = []int{}
	state.PairingOptions = []PairingOption{}
	appendAction(state, Action{
		Kind:       "bank",
		PlayerID:   playerID,
		Columns:    claimed,
		Gain:       gain,
		Source:     source,
		TurnNumber: state.TurnNumber,
		RollNumber: state.RollNumber,
		Summary: fmt.Sprintf(
			"%s 收手，保存 %d 步。",
			playerID,
			gain,
		),
	})
	if claimedColumnCount(state, playerID) >= targetColumns {
		finish(state, playerID, "占领三条路线，率先登顶。")
		return nil
	}
	state.Status = StatusBanked
	state.NextPlayer = nextPlayer(state, playerID)
	state.LostGain = 0
	return nil
}

func bust(state *State, playerID, source string) error {
	if state.CurrentPlayer != playerID {
		return errors.New("not the active player")
	}
	lost := turnGain(state)
	if player := state.Roster[playerID]; player != nil {
		player.Busts++
	}
	state.LostGain = lost
	state.Runners = map[int]int{}
	state.PairingOptions = []PairingOption{}
	appendAction(state, Action{
		Kind:       "bust",
		PlayerID:   playerID,
		Gain:       -lost,
		Source:     source,
		TurnNumber: state.TurnNumber,
		RollNumber: state.RollNumber,
		Summary: fmt.Sprintf(
			"%s 爆掉，失去本回合 %d 步。",
			playerID,
			lost,
		),
	})
	state.Status = StatusBust
	state.NextPlayer = nextPlayer(state, playerID)
	return nil
}

func nextTurn(state *State, actorID, source string) error {
	if state.Status != StatusBust && state.Status != StatusBanked {
		return errors.New("next turn is not available")
	}
	if state.Roster[actorID] == nil {
		return errors.New("player is not in room")
	}
	next := state.NextPlayer
	if next == "" {
		next = nextPlayer(state, state.CurrentPlayer)
	}
	appendAction(state, Action{
		Kind:       "nextTurn",
		PlayerID:   actorID,
		Source:     source,
		TurnNumber: state.TurnNumber,
		RollNumber: state.RollNumber,
		Summary:    fmt.Sprintf("%s 开始下一回合。", next),
	})
	state.Status = StatusRoll
	state.CurrentPlayer = next
	state.NextPlayer = nextPlayer(state, next)
	state.TurnNumber++
	state.RollNumber = 0
	state.Dice = []int{}
	state.PairingOptions = []PairingOption{}
	state.Runners = map[int]int{}
	state.LostGain = 0
	return nil
}

func finish(state *State, winnerID, reason string) {
	claimed := claimedColumnsFor(state, winnerID)
	state.Status = StatusEnd
	state.Result = &Result{
		WinnerID:       winnerID,
		ClaimedColumns: claimed,
		TurnCount:      state.TurnNumber,
		RollCount:      state.TotalRolls,
		BustCounts:     bustCounts(state),
		Reason:         reason,
	}
	state.CurrentPlayer = ""
	state.NextPlayer = ""
	state.Dice = []int{}
	state.PairingOptions = []PairingOption{}
	state.Runners = map[int]int{}
	state.LostGain = 0
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	state.TransitionDeadline = time.Time{}
	state.TransitionTimeout = 0
}

func legalPairingOptions(state *State) []PairingOption {
	return legalPairingOptionsForDice(state, state.Dice)
}

func legalPairingOptionsForDice(state *State, dice []int) []PairingOption {
	if len(dice) != dicePerRoll {
		return nil
	}
	pairings := canonicalPairings(dice)
	options := []PairingOption{}
	for _, value := range pairings {
		variants := variantsForPairing(state, value)
		options = append(options, variants...)
	}
	sort.SliceStable(options, func(i, j int) bool {
		if options[i].PairingIndex != options[j].PairingIndex {
			return options[i].PairingIndex < options[j].PairingIndex
		}
		if options[i].VariantIndex != options[j].VariantIndex {
			return options[i].VariantIndex < options[j].VariantIndex
		}
		return options[i].ActionID < options[j].ActionID
	})
	return options
}

func canonicalPairings(dice []int) []pairing {
	result := []pairing{}
	seen := map[string]bool{}
	for index, split := range pairingIndexes {
		first := dice[split[0]] + dice[split[1]]
		second := dice[split[2]] + dice[split[3]]
		sums := []int{first, second}
		indexes := []int{split[0], split[1], split[2], split[3]}
		if first > second {
			sums[0], sums[1] = sums[1], sums[0]
			indexes = []int{split[2], split[3], split[0], split[1]}
		}
		signature := fmt.Sprintf("%d/%d", sums[0], sums[1])
		if seen[signature] {
			continue
		}
		seen[signature] = true
		result = append(result, pairing{
			index:       index,
			sums:        sums,
			diceIndexes: indexes,
		})
	}
	return result
}

func variantsForPairing(state *State, value pairing) []PairingOption {
	orders := [][]int{{0, 1}}
	if value.sums[0] != value.sums[1] {
		orders = append(orders, []int{1, 0})
	}
	type candidate struct {
		advances map[int]int
		runners  map[int]int
		steps    int
	}
	candidates := []candidate{}
	maxSteps := 0
	for _, order := range orders {
		runners := cloneProgress(state.Runners)
		advances := map[int]int{}
		steps := 0
		for _, sumIndex := range order {
			column := value.sums[sumIndex]
			position, ok := advanceRunner(state, runners, column)
			if !ok {
				continue
			}
			runners[column] = position
			advances[column]++
			steps++
		}
		if steps == 0 {
			continue
		}
		if steps > maxSteps {
			maxSteps = steps
			candidates = candidates[:0]
		}
		if steps == maxSteps {
			candidates = append(candidates, candidate{
				advances: advances,
				runners:  runners,
				steps:    steps,
			})
		}
	}
	result := []PairingOption{}
	seen := map[string]bool{}
	for _, item := range candidates {
		signature := progressSignature(item.runners) + "|" +
			progressSignature(item.advances)
		if seen[signature] {
			continue
		}
		seen[signature] = true
		variantIndex := len(result)
		option := PairingOption{
			ActionID: fmt.Sprintf(
				"t%d-r%d-p%d-v%d-%s",
				state.TurnNumber,
				state.RollNumber,
				value.index,
				variantIndex,
				progressSignature(item.runners),
			),
			PairingIndex:    value.index,
			VariantIndex:    variantIndex,
			Sums:            append([]int(nil), value.sums...),
			DiceIndexes:     append([]int(nil), value.diceIndexes...),
			Advances:        cloneProgress(item.advances),
			RunnerPositions: cloneProgress(item.runners),
			StepCount:       item.steps,
		}
		result = append(result, option)
	}
	return result
}

func advanceRunner(
	state *State,
	runners map[int]int,
	column int,
) (int, bool) {
	height := columnHeight(column)
	if height == 0 || state.ClaimedBy[column] != "" {
		return 0, false
	}
	if position := runners[column]; position > 0 {
		if position >= height {
			return 0, false
		}
		return position + 1, true
	}
	if len(runners) >= targetColumns {
		return 0, false
	}
	player := state.Roster[state.CurrentPlayer]
	saved := 0
	if player != nil {
		saved = player.Saved[column]
	}
	if saved >= height {
		return 0, false
	}
	return saved + 1, true
}

func bustRisk(state *State) float64 {
	if state.Status != StatusDecide && state.Status != StatusRoll &&
		state.Status != StatusChoose {
		return 0
	}
	busts := 0
	for a := 1; a <= diceSides; a++ {
		for b := 1; b <= diceSides; b++ {
			for c := 1; c <= diceSides; c++ {
				for d := 1; d <= diceSides; d++ {
					if len(legalPairingOptionsForDice(
						state,
						[]int{a, b, c, d},
					)) == 0 {
						busts++
					}
				}
			}
		}
	}
	return float64(busts) / float64(totalOutcomes)
}

func turnGain(state *State) int {
	player := state.Roster[state.CurrentPlayer]
	total := 0
	for column, position := range state.Runners {
		saved := 0
		if player != nil {
			saved = player.Saved[column]
		}
		if position > saved {
			total += position - saved
		}
	}
	return total
}

func nextRandom(state *State, limit int) int {
	if limit <= 1 {
		return 0
	}
	value := state.RandomSeed + state.RandomCounter*0x9e3779b97f4a7c15
	state.RandomCounter++
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	return int(value % uint64(limit))
}

func appendAction(state *State, action Action) {
	action.ID = len(state.Log) + 1
	if action.TurnNumber == 0 {
		action.TurnNumber = state.TurnNumber
	}
	if action.RollNumber == 0 {
		action.RollNumber = state.RollNumber
	}
	value := cloneAction(action)
	value.Columns = sortedInts(value.Columns)
	state.Log = append(state.Log, value)
	state.LastAction = &value
}

func nextPlayer(state *State, playerID string) string {
	if len(state.PlayerOrder) == 0 {
		return ""
	}
	index := playerIndex(state, playerID)
	if index < 0 {
		return state.PlayerOrder[0]
	}
	return state.PlayerOrder[(index+1)%len(state.PlayerOrder)]
}

func playerIndex(state *State, playerID string) int {
	for index, current := range state.PlayerOrder {
		if current == playerID {
			return index
		}
	}
	return -1
}

func columnHeight(column int) int {
	return columnHeights[column]
}

func claimedColumnCount(state *State, playerID string) int {
	total := 0
	for _, owner := range state.ClaimedBy {
		if owner == playerID {
			total++
		}
	}
	return total
}

func claimedColumnsFor(state *State, playerID string) []int {
	result := []int{}
	for column, owner := range state.ClaimedBy {
		if owner == playerID {
			result = append(result, column)
		}
	}
	sort.Ints(result)
	return result
}

func bustCounts(state *State) map[string]int {
	result := make(map[string]int, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		if player := state.Roster[playerID]; player != nil {
			result[playerID] = player.Busts
		}
	}
	return result
}

func savedStepCount(player *Player) int {
	if player == nil {
		return 0
	}
	total := 0
	for _, steps := range player.Saved {
		total += steps
	}
	return total
}

func progressSignature(value map[int]int) string {
	keys := sortedKeys(value)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%d:%d", key, value[key]))
	}
	return strings.Join(parts, ",")
}

func sortedKeys(value map[int]int) []int {
	result := make([]int, 0, len(value))
	for key := range value {
		result = append(result, key)
	}
	sort.Ints(result)
	return result
}

func sortedInts(values []int) []int {
	result := append([]int(nil), values...)
	sort.Ints(result)
	return result
}

func diceSummary(dice []int) string {
	parts := make([]string, len(dice))
	for index, value := range dice {
		parts[index] = fmt.Sprintf("%d", value)
	}
	return strings.Join(parts, "-")
}

func columnList(columns []int) string {
	if len(columns) == 0 {
		return "路线"
	}
	parts := make([]string, len(columns))
	for index, column := range columns {
		parts[index] = fmt.Sprintf("%d", column)
	}
	return strings.Join(parts, "、")
}
