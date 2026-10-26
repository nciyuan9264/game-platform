package warchest

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func playAction(
	state *State,
	playerID string,
	payload []byte,
	automatic bool,
	now time.Time,
) error {
	var value struct {
		ActionID string `json:"actionID"`
	}
	if err := decodePayload(payload, &value); err != nil {
		return err
	}
	if value.ActionID == "" {
		return errors.New("actionID is required")
	}
	var selected *LegalAction
	for _, action := range legalActions(state, playerID) {
		if action.ActionID == value.ActionID {
			current := action
			selected = &current
			break
		}
	}
	if selected == nil {
		return errors.New("action is stale or no longer legal")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return resolveAction(state, playerID, *selected, automatic, "", now.UTC())
}

func resolveAction(
	state *State,
	commandPlayerID string,
	action LegalAction,
	automatic bool,
	guardChoice string,
	now time.Time,
) error {
	if action.Action == ActionDefend {
		pending := state.Pending
		if pending == nil ||
			pending.Kind != "guard" ||
			pending.Attack == nil {
			return errors.New("guard defense is not pending")
		}
		attack := *pending.Attack
		attacker := findUnitByID(state, attack.ActorUnitID)
		if attacker == nil {
			return errors.New("attacking unit is unavailable")
		}
		actingPlayerID := state.playerIDForSide(attacker.Side)
		state.Pending = clonePending(pending.Resume)
		state.CurrentPlayer = actingPlayerID
		return resolveAction(
			state,
			actingPlayerID,
			attack,
			automatic,
			action.Defense,
			now,
		)
	}

	target := findUnitByID(state, action.TargetUnitID)
	if target != nil &&
		target.UnitType == UnitGuard &&
		len(state.playerForSide(target.Side).Supply[UnitGuard]) > 0 &&
		guardChoice == "" {
		attack := action
		state.Pending = &PendingDecision{
			Kind:         "guard",
			PlayerID:     state.playerIDForSide(target.Side),
			Side:         target.Side,
			ActorUnitID:  action.ActorUnitID,
			TargetUnitID: target.UnitID,
			UnitType:     UnitGuard,
			Attack:       &attack,
			Resume:       clonePending(state.Pending),
			Description:  "choose a royal guard loss source",
		}
		state.Status = StatusPending
		state.CurrentPlayer = state.Pending.PlayerID
		startDeadline(state, now)
		return nil
	}

	actingPlayerID := commandPlayerID
	actor := findUnitByID(state, action.ActorUnitID)
	if actor != nil {
		actingPlayerID = state.playerIDForSide(actor.Side)
	}
	player := state.Roster[actingPlayerID]
	if player == nil {
		return errors.New("acting player is unavailable")
	}
	prior := clonePending(state.Pending)
	state.Pending = nil
	resolvedActorType := UnitGuard
	if actor != nil {
		resolvedActorType = actor.UnitType
	} else if action.UnitType != UnitRoyal {
		resolvedActorType = action.UnitType
	}

	if action.Action == ActionFinish {
		appendActionEntry(state, player, action, automatic, "", "")
		nextTurn(state, player.PlayerID, now)
		return nil
	}

	footmanRemaining := []string{}
	if action.Action == ActionTactic &&
		action.UnitType == UnitFootman {
		for _, unit := range unitsOfType(state, player.Side, UnitFootman) {
			if unit.UnitID != action.ActorUnitID {
				footmanRemaining = append(footmanRemaining, unit.UnitID)
			}
		}
	} else if prior != nil && prior.Kind == "footman" {
		for _, unitID := range prior.RemainingUnitIDs {
			if unitID != action.ActorUnitID {
				footmanRemaining = append(footmanRemaining, unitID)
			}
		}
	}

	if action.Free {
		if prior != nil && prior.Kind == "berserker" {
			unit := findUnitByID(state, action.ActorUnitID)
			if unit == nil || len(unit.CoinIDs) < 2 {
				return errors.New("berserker reinforcement is unavailable")
			}
			index := len(unit.CoinIDs) - 1
			coinID := unit.CoinIDs[index]
			unit.CoinIDs = unit.CoinIDs[:index]
			player.Discard = append(player.Discard, Discard{
				CoinID: coinID,
				FaceUp: true,
			})
		}
	} else {
		var removed bool
		player.Hand, removed = removeCoin(player.Hand, action.CoinID)
		if !removed {
			return errors.New("payment coin is unavailable")
		}
		switch action.Action {
		case ActionDeploy:
			state.UnitSerial++
			unitID := fmt.Sprintf(
				"u-%s-%s-%d",
				player.Side,
				action.UnitType,
				state.UnitSerial,
			)
			state.Board[action.To] = &UnitStack{
				UnitID:   unitID,
				UnitType: action.UnitType,
				Side:     player.Side,
				Cell:     action.To,
				CoinIDs:  []string{action.CoinID},
			}
			action.ActorUnitID = unitID
		case ActionBolster:
			unit := findUnitByID(state, action.ActorUnitID)
			if unit == nil {
				return errors.New("unit to bolster is unavailable")
			}
			unit.CoinIDs = append(unit.CoinIDs, action.CoinID)
		default:
			faceUp := action.Action != ActionRecruit &&
				action.Action != ActionInitiative &&
				action.Action != ActionPass
			player.Discard = append(player.Discard, Discard{
				CoinID: action.CoinID,
				FaceUp: faceUp,
			})
		}
	}

	maneuver := action.Action
	if action.Maneuver != "" {
		maneuver = action.Maneuver
	}
	actor = findUnitByID(state, action.ActorUnitID)
	if actor != nil &&
		action.To != "" &&
		action.From != action.To &&
		(maneuver == ActionMove || action.Action == ActionTactic) {
		delete(state.Board, actor.Cell)
		actor.Cell = action.To
		state.Board[actor.Cell] = actor
	}

	targetType := UnitType("")
	if action.TargetUnitID != "" {
		actor = findUnitByID(state, action.ActorUnitID)
		target = findUnitByID(state, action.TargetUnitID)
		if actor == nil || target == nil {
			return errors.New("attack participants are unavailable")
		}
		targetType = target.UnitType
		retaliates := target.UnitType == UnitPike &&
			distance(actor.Cell, target.Cell) == 1
		if guardChoice == "supply" {
			defender := state.playerForSide(target.Side)
			coinID := removeUnitCoin(defender.Supply, UnitGuard)
			if coinID == "" {
				return errors.New("royal guard supply is unavailable")
			}
			defender.Lost[UnitGuard] = append(
				defender.Lost[UnitGuard],
				coinID,
			)
		} else {
			damageUnit(state, target)
		}
		if retaliates {
			damageUnit(state, actor)
		}
		actor = findUnitByID(state, action.ActorUnitID)
		if action.After != "" && actor != nil {
			if !emptyAt(state, action.After) {
				return errors.New("swordsman advance destination is occupied")
			}
			delete(state.Board, actor.Cell)
			actor.Cell = action.After
			state.Board[actor.Cell] = actor
		}
	}

	if maneuver == ActionControl {
		state.Controls[action.To] = player.Side
		if controlledLocations(state, player.Side) >= 6 {
			finishGame(state, player.PlayerID, player.Side, "control")
		}
	}
	if action.Action == ActionRecruit {
		coinID := removeUnitCoin(player.Supply, action.RecruitUnit)
		if coinID == "" {
			return errors.New("recruit supply is unavailable")
		}
		player.Discard = append(player.Discard, Discard{
			CoinID: coinID,
			FaceUp: true,
		})
	}
	if action.Action == ActionInitiative {
		state.InitiativePlayer = player.PlayerID
		state.InitiativeChanged = true
	}

	state.ActionCounter++
	summary := actionSummary(state, action, targetType, guardChoice)
	appendActionEntry(
		state,
		player,
		action,
		automatic,
		summary,
		targetType,
	)

	if state.Status != StatusEnd {
		if remaining := maneuverableFootmen(
			state,
			player,
			footmanRemaining,
		); len(remaining) > 0 {
			state.Pending = &PendingDecision{
				Kind:             "footman",
				PlayerID:         player.PlayerID,
				Side:             player.Side,
				UnitType:         UnitFootman,
				RemainingUnitIDs: remaining,
				Description:      "maneuver the other footman",
			}
		}

		actor = findUnitByID(state, action.ActorUnitID)
		if actor != nil &&
			actor.UnitType == UnitBerserker &&
			(maneuver == ActionMove ||
				maneuver == ActionAttack ||
				maneuver == ActionControl) &&
			len(actor.CoinIDs) > 1 {
			state.Pending = &PendingDecision{
				Kind:        "berserker",
				PlayerID:    player.PlayerID,
				Side:        player.Side,
				ActorUnitID: actor.UnitID,
				UnitType:    UnitBerserker,
				Description: "discard a reinforcement to maneuver again",
			}
		}

		if action.Action == ActionRecruit &&
			action.RecruitUnit == UnitMercenary {
			if units := unitsOfType(
				state,
				player.Side,
				UnitMercenary,
			); len(units) > 0 {
				state.Pending = &PendingDecision{
					Kind:        "mercenary",
					PlayerID:    player.PlayerID,
					Side:        player.Side,
					ActorUnitID: units[0].UnitID,
					UnitType:    UnitMercenary,
					Description: "maneuver the deployed mercenary",
				}
			}
		}

		if resolvedActorType == UnitPriest &&
			(action.TargetUnitID != "" || maneuver == ActionControl) {
			if coinID := drawOne(state, player); coinID != "" {
				state.Pending = &PendingDecision{
					Kind:        "priest",
					PlayerID:    player.PlayerID,
					Side:        player.Side,
					ActorUnitID: action.ActorUnitID,
					CoinID:      coinID,
					UnitType:    coinType(state, coinID),
					Description: "use the newly drawn coin immediately",
				}
			}
		}

		if guardChoice != "" &&
			resolvedActorType == UnitSword {
			if actor = findUnitByID(state, action.ActorUnitID); actor != nil &&
				hasEmptyNeighbor(state, actor.Cell) {
				state.Pending = &PendingDecision{
					Kind:        "sword",
					PlayerID:    player.PlayerID,
					Side:        player.Side,
					ActorUnitID: actor.UnitID,
					UnitType:    UnitSword,
					Description: "optionally advance after the attack",
				}
			}
		}
	}

	nextTurn(state, player.PlayerID, now)
	return validateCoinConservation(state)
}

func damageUnit(state *State, unit *UnitStack) {
	if unit == nil || len(unit.CoinIDs) == 0 {
		return
	}
	index := len(unit.CoinIDs) - 1
	coinID := unit.CoinIDs[index]
	unit.CoinIDs = unit.CoinIDs[:index]
	player := state.playerForSide(unit.Side)
	player.Lost[unit.UnitType] = append(player.Lost[unit.UnitType], coinID)
	if len(unit.CoinIDs) == 0 {
		delete(state.Board, unit.Cell)
	}
}

func maneuverableFootmen(
	state *State,
	player *Player,
	unitIDs []string,
) []string {
	result := []string{}
	for _, unitID := range unitIDs {
		unit := findUnitByID(state, unitID)
		if unit == nil {
			continue
		}
		if len(ordinaryManeuvers(
			state,
			player,
			"",
			UnitFootman,
			unit,
			len(unit.CoinIDs),
			true,
		)) > 0 {
			result = append(result, unitID)
		}
	}
	return result
}

func hasEmptyNeighbor(state *State, cell string) bool {
	for _, target := range adjacent(cell) {
		if emptyAt(state, target) {
			return true
		}
	}
	return false
}

func appendActionEntry(
	state *State,
	player *Player,
	action LegalAction,
	automatic bool,
	summary string,
	targetType UnitType,
) {
	if targetType == "" {
		if target := findUnitByID(state, action.TargetUnitID); target != nil {
			targetType = target.UnitType
		}
	}
	privateFor := ""
	if action.Action == ActionRecruit ||
		action.Action == ActionInitiative ||
		action.Action == ActionPass {
		privateFor = player.PlayerID
	}
	entry := ActionEntry{
		ID:             len(state.Log) + 1,
		RoundNo:        state.RoundNo,
		PlayerID:       player.PlayerID,
		Side:           player.Side,
		Action:         action.Action,
		CoinID:         action.CoinID,
		UnitType:       action.UnitType,
		ActorUnitID:    action.ActorUnitID,
		SourceUnitID:   action.SourceUnitID,
		From:           action.From,
		To:             action.To,
		TargetUnitID:   action.TargetUnitID,
		TargetUnitType: targetType,
		RecruitUnit:    action.RecruitUnit,
		After:          action.After,
		Defense:        action.Defense,
		Maneuver:       action.Maneuver,
		Path:           append([]string(nil), action.Path...),
		Automatic:      automatic,
		PrivateFor:     privateFor,
		Summary:        summary,
	}
	state.Log = append(state.Log, entry)
	copyEntry := entry
	state.LastAction = &copyEntry
}

func actionSummary(
	state *State,
	action LegalAction,
	targetType UnitType,
	guardChoice string,
) string {
	payload := map[string]any{
		"action": action.Action,
	}
	if action.UnitType != "" {
		payload["unitType"] = action.UnitType
	}
	if action.From != "" {
		payload["from"] = action.From
	}
	if action.To != "" {
		payload["to"] = action.To
	}
	if targetType != "" {
		payload["targetUnitType"] = targetType
	}
	if action.RecruitUnit != "" {
		payload["recruitUnit"] = action.RecruitUnit
	}
	if guardChoice != "" {
		payload["defense"] = guardChoice
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func clonePending(value *PendingDecision) *PendingDecision {
	if value == nil {
		return nil
	}
	data, _ := json.Marshal(value)
	var result PendingDecision
	_ = json.Unmarshal(data, &result)
	return &result
}
