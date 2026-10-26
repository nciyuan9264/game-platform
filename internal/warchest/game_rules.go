package warchest

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

func beginMatch(state *State, actorID string) error {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return errors.New("only the owner can start the match")
	}
	if !setupComplete(state) {
		return errors.New("warchest setup is incomplete")
	}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil || !player.Ready || !player.Online {
			return errors.New("all players must be ready and online")
		}
		player.Ready = player.AI
		player.GameReady = player.AI
	}
	state.Status = StatusWaiting
	state.TurnDeadline = time.Time{}
	return nil
}

func gameReady(state *State, playerID string, sentAt time.Time) error {
	if state.Status != StatusWaiting {
		return nil
	}
	player := state.Roster[playerID]
	if player == nil {
		return errors.New("player not found")
	}
	player.Ready = true
	player.GameReady = true
	for _, currentID := range state.PlayerOrder {
		current := state.Roster[currentID]
		if current == nil || !current.GameReady {
			return nil
		}
	}
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}
	setupGame(state, sentAt.UTC())
	return nil
}

func setupGame(state *State, startedAt time.Time) {
	state.Coins = map[string]UnitType{}
	state.CoinOwners = map[string]string{}
	state.Board = map[string]*UnitStack{}
	state.Controls = map[string]Side{}
	for side, locations := range startingLocations {
		for _, location := range locations {
			state.Controls[location] = side
		}
	}
	state.RandomCounter = 0
	state.RoundNo = 1
	state.CurrentPlayer = ""
	state.InitiativePlayer = state.InitialPlayerID
	state.InitiativeChanged = false
	state.Pending = nil
	state.Log = []ActionEntry{}
	state.LastAction = nil
	state.WinnerID = ""
	state.WinnerSide = ""
	state.EndReason = ""
	state.GameStartTime = startedAt
	state.TurnStartedAt = time.Time{}
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	state.ActionCounter = 0
	state.UnitSerial = 0

	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		player.Bag = []string{}
		player.Hand = []string{}
		player.Discard = []Discard{}
		player.Supply = UnitCoins{}
		player.Lost = UnitCoins{}
		for _, unit := range player.Roster {
			total := unitTotals[unit]
			for index := 1; index <= total; index++ {
				coinID := fmt.Sprintf(
					"s%d-%s-%d",
					player.SeatIndex,
					unit,
					index,
				)
				state.Coins[coinID] = unit
				state.CoinOwners[coinID] = player.PlayerID
				if index <= 2 {
					player.Bag = append(player.Bag, coinID)
				} else {
					player.Supply[unit] = append(
						player.Supply[unit],
						coinID,
					)
				}
			}
		}
		royalID := fmt.Sprintf("s%d-%s-1", player.SeatIndex, UnitRoyal)
		state.Coins[royalID] = UnitRoyal
		state.CoinOwners[royalID] = player.PlayerID
		player.Bag = append(player.Bag, royalID)
		shuffleCoins(state, player.Bag)
	}
	state.Status = StatusPlaying
	drawRound(state, false, startedAt)
}

func drawRound(state *State, advance bool, now time.Time) {
	if advance {
		state.RoundNo++
	}
	state.InitiativeChanged = false
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		for len(player.Hand) < 3 {
			if len(player.Bag) == 0 {
				if len(player.Discard) == 0 {
					break
				}
				for _, discard := range player.Discard {
					player.Bag = append(player.Bag, discard.CoinID)
				}
				player.Discard = []Discard{}
				shuffleCoins(state, player.Bag)
			}
			coinID := player.Bag[0]
			player.Bag = player.Bag[1:]
			player.Hand = append(player.Hand, coinID)
		}
	}
	initiative := state.Roster[state.InitiativePlayer]
	if initiative != nil && len(initiative.Hand) > 0 {
		state.CurrentPlayer = initiative.PlayerID
	} else {
		state.CurrentPlayer = otherPlayerID(state, state.InitiativePlayer)
	}
	state.Pending = nil
	state.Status = StatusPlaying
	startDeadline(state, now)
}

func drawOne(state *State, player *Player) string {
	if player == nil {
		return ""
	}
	if len(player.Bag) == 0 && len(player.Discard) > 0 {
		for _, discard := range player.Discard {
			player.Bag = append(player.Bag, discard.CoinID)
		}
		player.Discard = []Discard{}
		shuffleCoins(state, player.Bag)
	}
	if len(player.Bag) == 0 {
		return ""
	}
	coinID := player.Bag[0]
	player.Bag = player.Bag[1:]
	player.Hand = append(player.Hand, coinID)
	return coinID
}

func shuffleCoins(state *State, values []string) {
	for index := len(values) - 1; index > 0; index-- {
		state.RandomCounter++
		random := state.RandomSeed +
			state.RandomCounter*0x9e3779b97f4a7c15 +
			uint64(state.MatchRoundNo)*0x517cc1b727220a95
		random = random*1664525 + 1013904223
		target := int(random % uint64(index+1))
		values[index], values[target] = values[target], values[index]
	}
}

func nextTurn(state *State, actingPlayerID string, now time.Time) {
	if state.Status == StatusEnd {
		state.Pending = nil
		state.CurrentPlayer = ""
		state.TurnDeadline = time.Time{}
		state.TurnTimeout = 0
		return
	}
	if state.Pending != nil {
		state.Status = StatusPending
		state.CurrentPlayer = state.Pending.PlayerID
		startDeadline(state, now)
		return
	}
	otherID := otherPlayerID(state, actingPlayerID)
	other := state.Roster[otherID]
	acting := state.Roster[actingPlayerID]
	switch {
	case other != nil && len(other.Hand) > 0:
		state.CurrentPlayer = otherID
		state.Status = StatusPlaying
		startDeadline(state, now)
	case acting != nil && len(acting.Hand) > 0:
		state.CurrentPlayer = actingPlayerID
		state.Status = StatusPlaying
		startDeadline(state, now)
	default:
		drawRound(state, true, now)
	}
}

func startDeadline(state *State, now time.Time) {
	player := state.Roster[state.CurrentPlayer]
	if player == nil || player.AI || state.Status == StatusEnd {
		state.TurnStartedAt = time.Time{}
		state.TurnDeadline = time.Time{}
		state.TurnTimeout = 0
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	timeout := time.Duration(defaultTurnTimeout) * time.Second
	state.TurnStartedAt = now.UTC()
	state.TurnTimeout = timeout
	state.TurnDeadline = state.TurnStartedAt.Add(timeout)
}

func resign(state *State, playerID string) error {
	if state.Status != StatusPlaying && state.Status != StatusPending {
		return errors.New("game is not active")
	}
	if state.Roster[playerID] == nil {
		return errors.New("player not found")
	}
	winnerID := otherPlayerID(state, playerID)
	finishGame(
		state,
		winnerID,
		state.sideForPlayer(winnerID),
		"resign",
	)
	return nil
}

func finishGame(state *State, winnerID string, side Side, reason string) {
	state.Status = StatusEnd
	state.WinnerID = winnerID
	state.WinnerSide = side
	state.EndReason = reason
	state.CurrentPlayer = ""
	state.Pending = nil
	state.TurnStartedAt = time.Time{}
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
}

func controlledLocations(state *State, side Side) int {
	total := 0
	for _, current := range state.Controls {
		if current == side {
			total++
		}
	}
	return total
}

func validateCoinConservation(state *State) error {
	if state.Status == StatusMatch || state.Status == StatusWaiting {
		return nil
	}
	if len(state.CoinOwners) != len(state.Coins) {
		return errors.New("coin owner index does not match coin inventory")
	}
	seen := map[string]string{}
	add := func(coinID, location string) error {
		unit, exists := state.Coins[coinID]
		if !exists || unit == "" {
			return fmt.Errorf("unknown coin %q in %s", coinID, location)
		}
		if previous := seen[coinID]; previous != "" {
			return fmt.Errorf(
				"coin %q appears in %s and %s",
				coinID,
				previous,
				location,
			)
		}
		seen[coinID] = location
		return nil
	}
	checkOwner := func(coinID, playerID, location string) error {
		if state.CoinOwners[coinID] != playerID {
			return fmt.Errorf(
				"coin %q in %s belongs to %q, not %q",
				coinID,
				location,
				state.CoinOwners[coinID],
				playerID,
			)
		}
		return nil
	}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			return fmt.Errorf("player %q is missing", playerID)
		}
		allowed := map[UnitType]bool{UnitRoyal: true}
		for _, unit := range player.Roster {
			allowed[unit] = true
		}
		for _, coinID := range player.Bag {
			if !allowed[state.Coins[coinID]] {
				return fmt.Errorf("foreign coin %q in bag", coinID)
			}
			if err := checkOwner(coinID, playerID, "bag"); err != nil {
				return err
			}
			if err := add(coinID, playerID+" bag"); err != nil {
				return err
			}
		}
		for _, coinID := range player.Hand {
			if !allowed[state.Coins[coinID]] {
				return fmt.Errorf("foreign coin %q in hand", coinID)
			}
			if err := checkOwner(coinID, playerID, "hand"); err != nil {
				return err
			}
			if err := add(coinID, playerID+" hand"); err != nil {
				return err
			}
		}
		for _, discard := range player.Discard {
			if !allowed[state.Coins[discard.CoinID]] {
				return fmt.Errorf("foreign coin %q in discard", discard.CoinID)
			}
			if err := checkOwner(
				discard.CoinID,
				playerID,
				"discard",
			); err != nil {
				return err
			}
			if err := add(
				discard.CoinID,
				playerID+" discard",
			); err != nil {
				return err
			}
		}
		for unit, coinIDs := range player.Supply {
			if unit == UnitRoyal || !allowed[unit] {
				return fmt.Errorf("invalid supply unit %q", unit)
			}
			for _, coinID := range coinIDs {
				if state.Coins[coinID] != unit {
					return fmt.Errorf("supply coin %q has wrong type", coinID)
				}
				if err := checkOwner(
					coinID,
					playerID,
					"supply",
				); err != nil {
					return err
				}
				if err := add(coinID, playerID+" supply"); err != nil {
					return err
				}
			}
		}
		for unit, coinIDs := range player.Lost {
			if !allowed[unit] {
				return fmt.Errorf("invalid lost unit %q", unit)
			}
			for _, coinID := range coinIDs {
				if state.Coins[coinID] != unit {
					return fmt.Errorf("lost coin %q has wrong type", coinID)
				}
				if err := checkOwner(
					coinID,
					playerID,
					"lost",
				); err != nil {
					return err
				}
				if err := add(coinID, playerID+" lost"); err != nil {
					return err
				}
			}
		}
	}
	unitIDs := map[string]bool{}
	for cellKey, unit := range state.Board {
		if boardCells[cellKey] == (cell{}) && cellKey != "0,0" {
			return fmt.Errorf("unit occupies invalid cell %q", cellKey)
		}
		if unit == nil ||
			unit.UnitID == "" ||
			unit.Cell != cellKey ||
			!containsUnit(
				state.playerForSide(unit.Side).Roster,
				unit.UnitType,
			) ||
			len(unit.CoinIDs) == 0 {
			return fmt.Errorf("invalid unit at %q", cellKey)
		}
		if unitIDs[unit.UnitID] {
			return fmt.Errorf("duplicate unit id %q", unit.UnitID)
		}
		unitIDs[unit.UnitID] = true
		for _, coinID := range unit.CoinIDs {
			if state.Coins[coinID] != unit.UnitType {
				return fmt.Errorf("board coin %q has wrong type", coinID)
			}
			if err := checkOwner(
				coinID,
				state.playerIDForSide(unit.Side),
				"board "+cellKey,
			); err != nil {
				return err
			}
			if err := add(coinID, "board "+cellKey); err != nil {
				return err
			}
		}
	}
	if len(seen) != len(state.Coins) {
		missing := make([]string, 0, len(state.Coins)-len(seen))
		for coinID := range state.Coins {
			if seen[coinID] == "" {
				missing = append(missing, coinID)
			}
		}
		sort.Strings(missing)
		return fmt.Errorf("coins are missing: %v", missing)
	}
	return validateUnitLimits(state)
}

func validateUnitLimits(state *State) error {
	counts := map[string]int{}
	for _, unit := range state.Board {
		if unit == nil {
			continue
		}
		key := fmt.Sprintf("%s/%s", unit.Side, unit.UnitType)
		counts[key]++
		limit := 1
		if unit.UnitType == UnitFootman {
			limit = 2
		}
		if counts[key] > limit {
			return fmt.Errorf("too many deployed units for %s", key)
		}
	}
	return nil
}

func coinType(state *State, coinID string) UnitType {
	return state.Coins[coinID]
}

func removeCoin(values []string, target string) ([]string, bool) {
	for index, value := range values {
		if value == target {
			return append(values[:index], values[index+1:]...), true
		}
	}
	return values, false
}

func removeUnitCoin(values UnitCoins, unit UnitType) string {
	coinIDs := values[unit]
	if len(coinIDs) == 0 {
		return ""
	}
	coinID := coinIDs[0]
	values[unit] = coinIDs[1:]
	return coinID
}

func findUnitByID(state *State, unitID string) *UnitStack {
	for _, unit := range state.Board {
		if unit != nil && unit.UnitID == unitID {
			return unit
		}
	}
	return nil
}

func unitsOfType(state *State, side Side, unitType UnitType) []*UnitStack {
	result := []*UnitStack{}
	for _, key := range boardCellOrder {
		unit := state.Board[key]
		if unit != nil && unit.Side == side && unit.UnitType == unitType {
			result = append(result, unit)
		}
	}
	return result
}

func currentUnitCell(state *State, unitID string) string {
	if unit := findUnitByID(state, unitID); unit != nil {
		return unit.Cell
	}
	return ""
}
