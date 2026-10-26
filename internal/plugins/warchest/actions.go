package warchest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

func legalActions(state *State, playerID string) []LegalAction {
	if state == nil ||
		state.CurrentPlayer != playerID ||
		(state.Status != StatusPlaying && state.Status != StatusPending) {
		return []LegalAction{}
	}
	player := state.Roster[playerID]
	if player == nil {
		return []LegalAction{}
	}
	actions := pendingActions(state, player)
	if state.Pending == nil {
		for _, coinID := range player.Hand {
			actions = append(
				actions,
				actionsForCoin(state, player, coinID)...,
			)
		}
	}
	result := make([]LegalAction, 0, len(actions))
	seen := map[string]bool{}
	for _, action := range actions {
		action.ActionID = stableActionID(state, action)
		if seen[action.ActionID] {
			continue
		}
		seen[action.ActionID] = true
		result = append(result, action)
	}
	return result
}

func pendingActions(state *State, player *Player) []LegalAction {
	pending := state.Pending
	if pending == nil {
		return nil
	}
	if pending.Kind == "guard" {
		result := []LegalAction{{
			Action:   ActionDefend,
			UnitType: UnitGuard,
			Defense:  "unit",
			Free:     true,
		}}
		if len(player.Supply[UnitGuard]) > 0 {
			result = append(result, LegalAction{
				Action:   ActionDefend,
				UnitType: UnitGuard,
				Defense:  "supply",
				Free:     true,
			})
		}
		return result
	}
	if pending.Kind == "priest" {
		if containsString(player.Hand, pending.CoinID) {
			return actionsForCoin(state, player, pending.CoinID)
		}
		return nil
	}

	unitType := pending.UnitType
	if unitType == "" {
		unitType = UnitType(pending.Kind)
	}
	var units []*UnitStack
	if pending.Kind == "footman" {
		for _, unitID := range pending.RemainingUnitIDs {
			if unit := findUnitByID(state, unitID); unit != nil {
				units = append(units, unit)
			}
		}
	} else if unit := findUnitByID(state, pending.ActorUnitID); unit != nil {
		units = append(units, unit)
	}
	result := []LegalAction{}
	for _, unit := range units {
		virtualCoins := len(unit.CoinIDs)
		if pending.Kind == "berserker" {
			virtualCoins--
			if virtualCoins <= 0 {
				continue
			}
		}
		current := ordinaryManeuvers(
			state,
			player,
			"",
			unitType,
			unit,
			virtualCoins,
			true,
		)
		switch pending.Kind {
		case "sword":
			for _, action := range current {
				if action.Action == ActionMove {
					result = append(result, action)
				}
			}
		default:
			result = append(result, current...)
		}
	}
	if pending.Kind != "footman" || len(result) == 0 {
		result = append(result, LegalAction{
			Action:   ActionFinish,
			UnitType: unitType,
			Free:     true,
		})
	}
	return result
}

func actionsForCoin(
	state *State,
	player *Player,
	coinID string,
) []LegalAction {
	unitType := coinType(state, coinID)
	if unitType == "" {
		return nil
	}
	base := LegalAction{
		CoinID:   coinID,
		UnitType: unitType,
	}
	result := []LegalAction{withAction(base, ActionPass)}
	if state.InitiativePlayer != player.PlayerID &&
		!state.InitiativeChanged {
		result = append(result, withAction(base, ActionInitiative))
	}
	for _, recruit := range player.Roster {
		if len(player.Supply[recruit]) == 0 {
			continue
		}
		action := withAction(base, ActionRecruit)
		action.RecruitUnit = recruit
		result = append(result, action)
	}
	if unitType == UnitRoyal {
		return append(result, guardTactics(state, player, base)...)
	}

	deployed := unitsOfType(state, player.Side, unitType)
	limit := 1
	if unitType == UnitFootman {
		limit = 2
	}
	if len(deployed) < limit {
		targets := map[string]bool{}
		for cell, side := range state.Controls {
			if side == player.Side && emptyAt(state, cell) {
				targets[cell] = true
			}
		}
		if unitType == UnitScout {
			for _, key := range boardCellOrder {
				unit := state.Board[key]
				if unit == nil || unit.Side != player.Side {
					continue
				}
				for _, target := range adjacent(key) {
					if emptyAt(state, target) {
						targets[target] = true
					}
				}
			}
		}
		for _, target := range sortedStringKeys(targets) {
			action := withAction(base, ActionDeploy)
			action.To = target
			result = append(result, action)
		}
	}
	for _, unit := range deployed {
		bolster := withAction(base, ActionBolster)
		bolster.ActorUnitID = unit.UnitID
		bolster.From = unit.Cell
		bolster.To = unit.Cell
		result = append(result, bolster)
		result = append(
			result,
			ordinaryManeuvers(
				state,
				player,
				coinID,
				unitType,
				unit,
				len(unit.CoinIDs),
				false,
			)...,
		)
		result = append(
			result,
			unitTactics(state, player, base, unit)...,
		)
	}
	return result
}

func ordinaryManeuvers(
	state *State,
	player *Player,
	coinID string,
	paymentType UnitType,
	unit *UnitStack,
	attackerCoins int,
	free bool,
) []LegalAction {
	if unit == nil || unit.Side != player.Side {
		return nil
	}
	base := LegalAction{
		CoinID:      coinID,
		UnitType:    paymentType,
		ActorUnitID: unit.UnitID,
		From:        unit.Cell,
		Free:        free,
	}
	result := []LegalAction{}
	for _, target := range adjacent(unit.Cell) {
		if emptyAt(state, target) {
			action := withAction(base, ActionMove)
			action.To = target
			action.Path = []string{target}
			result = append(result, action)
		}
	}
	if unit.UnitType != UnitArcher && unit.UnitType != UnitLancer {
		for _, target := range adjacent(unit.Cell) {
			if canAttack(state, unit, target, attackerCoins) {
				action := withAction(base, ActionAttack)
				action.TargetUnitID = state.Board[target].UnitID
				result = appendSwordFollowups(state, result, action, unit)
			}
		}
	}
	if locationSet[unit.Cell] && state.Controls[unit.Cell] != player.Side {
		action := withAction(base, ActionControl)
		action.To = unit.Cell
		result = append(result, action)
	}
	return result
}

func unitTactics(
	state *State,
	player *Player,
	base LegalAction,
	unit *UnitStack,
) []LegalAction {
	switch unit.UnitType {
	case UnitLight:
		return lightTactics(state, base, unit)
	case UnitArcher:
		return archerTactics(state, base, unit)
	case UnitCrossbow:
		return crossbowTactics(state, base, unit)
	case UnitCavalry:
		return cavalryTactics(state, base, unit)
	case UnitLancer:
		return lancerTactics(state, base, unit)
	case UnitEnsign:
		return grantedTactics(state, player, base, unit, ActionMove)
	case UnitMarshall:
		return grantedTactics(state, player, base, unit, ActionAttack)
	case UnitFootman:
		result := ordinaryManeuvers(
			state,
			player,
			base.CoinID,
			base.UnitType,
			unit,
			len(unit.CoinIDs),
			false,
		)
		for index := range result {
			result[index].Maneuver = result[index].Action
			result[index].Action = ActionTactic
		}
		return result
	default:
		return nil
	}
}

func guardTactics(
	state *State,
	player *Player,
	base LegalAction,
) []LegalAction {
	units := unitsOfType(state, player.Side, UnitGuard)
	if len(units) == 0 {
		return nil
	}
	unit := units[0]
	routes := shortRoutes(state, unit.Cell, 2)
	result := []LegalAction{}
	for _, destination := range sortedRouteDestinations(routes) {
		if state.Controls[destination] != player.Side {
			continue
		}
		action := withAction(base, ActionTactic)
		action.ActorUnitID = unit.UnitID
		action.From = unit.Cell
		action.To = destination
		action.Path = append([]string(nil), routes[destination]...)
		result = append(result, action)
	}
	return result
}

func lightTactics(
	state *State,
	base LegalAction,
	unit *UnitStack,
) []LegalAction {
	result := []LegalAction{}
	seen := map[string]bool{}
	for _, via := range adjacent(unit.Cell) {
		if !emptyAt(state, via) {
			continue
		}
		for _, destination := range adjacent(via) {
			if destination == unit.Cell ||
				seen[destination] ||
				!emptyAt(state, destination) {
				continue
			}
			seen[destination] = true
			action := withAction(base, ActionTactic)
			action.ActorUnitID = unit.UnitID
			action.From = unit.Cell
			action.To = destination
			action.Path = []string{via, destination}
			result = append(result, action)
		}
	}
	return result
}

func archerTactics(
	state *State,
	base LegalAction,
	unit *UnitStack,
) []LegalAction {
	result := []LegalAction{}
	for _, key := range boardCellOrder {
		if distance(unit.Cell, key) != 2 ||
			!canAttack(state, unit, key, len(unit.CoinIDs)) {
			continue
		}
		action := withAction(base, ActionTactic)
		action.ActorUnitID = unit.UnitID
		action.From = unit.Cell
		action.TargetUnitID = state.Board[key].UnitID
		result = append(result, action)
	}
	return result
}

func crossbowTactics(
	state *State,
	base LegalAction,
	unit *UnitStack,
) []LegalAction {
	origin := boardCells[unit.Cell]
	result := []LegalAction{}
	for _, direction := range directions {
		via := cellKey(origin.Q+direction.Q, origin.R+direction.R)
		target := cellKey(
			origin.Q+2*direction.Q,
			origin.R+2*direction.R,
		)
		if !emptyAt(state, via) ||
			!canAttack(state, unit, target, len(unit.CoinIDs)) {
			continue
		}
		action := withAction(base, ActionTactic)
		action.ActorUnitID = unit.UnitID
		action.From = unit.Cell
		action.TargetUnitID = state.Board[target].UnitID
		action.Path = []string{via}
		result = append(result, action)
	}
	return result
}

func cavalryTactics(
	state *State,
	base LegalAction,
	unit *UnitStack,
) []LegalAction {
	result := []LegalAction{}
	for _, destination := range adjacent(unit.Cell) {
		if !emptyAt(state, destination) {
			continue
		}
		for _, target := range adjacent(destination) {
			if !canAttack(state, unit, target, len(unit.CoinIDs)) {
				continue
			}
			action := withAction(base, ActionTactic)
			action.ActorUnitID = unit.UnitID
			action.From = unit.Cell
			action.To = destination
			action.TargetUnitID = state.Board[target].UnitID
			action.Path = []string{destination}
			result = append(result, action)
		}
	}
	return result
}

func lancerTactics(
	state *State,
	base LegalAction,
	unit *UnitStack,
) []LegalAction {
	origin := boardCells[unit.Cell]
	result := []LegalAction{}
	for _, direction := range directions {
		path := []string{}
		for step := 1; step <= 2; step++ {
			next := cellKey(
				origin.Q+step*direction.Q,
				origin.R+step*direction.R,
			)
			if !emptyAt(state, next) {
				break
			}
			path = append(path, next)
			target := cellKey(
				origin.Q+(step+1)*direction.Q,
				origin.R+(step+1)*direction.R,
			)
			if !canAttack(state, unit, target, len(unit.CoinIDs)) {
				continue
			}
			action := withAction(base, ActionTactic)
			action.ActorUnitID = unit.UnitID
			action.From = unit.Cell
			action.To = next
			action.TargetUnitID = state.Board[target].UnitID
			action.Path = append([]string(nil), path...)
			result = append(result, action)
		}
	}
	return result
}

func grantedTactics(
	state *State,
	player *Player,
	base LegalAction,
	source *UnitStack,
	maneuver ActionType,
) []LegalAction {
	result := []LegalAction{}
	for _, key := range boardCellOrder {
		unit := state.Board[key]
		if unit == nil ||
			unit.Side != player.Side ||
			distance(source.Cell, unit.Cell) > 2 {
			continue
		}
		actions := ordinaryManeuvers(
			state,
			player,
			base.CoinID,
			base.UnitType,
			unit,
			len(unit.CoinIDs),
			false,
		)
		for _, action := range actions {
			if action.Action != maneuver {
				continue
			}
			if maneuver == ActionMove &&
				distance(source.Cell, action.To) > 2 {
				continue
			}
			action.Maneuver = maneuver
			action.Action = ActionTactic
			action.SourceUnitID = source.UnitID
			result = append(result, action)
		}
	}
	return result
}

func appendSwordFollowups(
	state *State,
	result []LegalAction,
	action LegalAction,
	attacker *UnitStack,
) []LegalAction {
	result = append(result, action)
	if attacker.UnitType != UnitSword {
		return result
	}
	target := findUnitByID(state, action.TargetUnitID)
	if target == nil ||
		(target.UnitType == UnitPike &&
			distance(attacker.Cell, target.Cell) == 1 &&
			len(attacker.CoinIDs) == 1) ||
		(target.UnitType == UnitGuard &&
			len(state.playerForSide(target.Side).Supply[UnitGuard]) > 0) {
		return result
	}
	for _, destination := range adjacent(attacker.Cell) {
		if emptyAt(state, destination) ||
			(destination == target.Cell && len(target.CoinIDs) == 1) {
			next := action
			next.After = destination
			result = append(result, next)
		}
	}
	return result
}

func canAttack(
	state *State,
	attacker *UnitStack,
	targetCell string,
	attackerCoins int,
) bool {
	target := state.Board[targetCell]
	return attacker != nil &&
		target != nil &&
		target.Side != attacker.Side &&
		(target.UnitType != UnitKnight || attackerCoins >= 2)
}

func withAction(base LegalAction, action ActionType) LegalAction {
	result := base
	result.Action = action
	return result
}

func stableActionID(state *State, action LegalAction) string {
	action.ActionID = ""
	data, _ := json.Marshal(action)
	sum := sha256.Sum256(data)
	return "a-" +
		intString(state.ActionCounter) +
		"-" +
		hex.EncodeToString(sum[:6])
}

func sortedStringKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func intString(value int) string {
	if value == 0 {
		return "0"
	}
	buffer := [20]byte{}
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}
