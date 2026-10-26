package warchest

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const (
	aiMinDelay = 3 * time.Second
	aiMaxDelay = 5 * time.Second
)

func (p *Plugin) Decide(
	ctx context.Context,
	request game.AIRequest,
) (contracts.RoomCommand, error) {
	state, err := asState(request.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	if state.Status == StatusMatch &&
		state.SetupMode == SetupDraft &&
		state.Draft != nil &&
		!state.Draft.Complete {
		if state.Draft.CurrentPickerID != request.PlayerID {
			return contracts.RoomCommand{}, errors.New("AI is not the current picker")
		}
		unit, ok := bestDraftUnit(state, request.PlayerID)
		if !ok {
			return contracts.RoomCommand{}, errors.New("draft has no available unit")
		}
		return contracts.RoomCommand{
			Type: CommandDraftUnit,
			Payload: map[string]any{
				"unitType": unit,
			},
		}, nil
	}
	if state.CurrentPlayer != request.PlayerID {
		return contracts.RoomCommand{}, errors.New("AI is not the active player")
	}
	actions := legalActions(state, request.PlayerID)
	if len(actions) == 0 {
		return contracts.RoomCommand{}, errors.New("AI has no legal action")
	}
	action := bestAction(ctx, state, request.PlayerID, actions)
	return contracts.RoomCommand{
		Type: CommandPlayAction,
		Payload: map[string]any{
			"actionID": action.ActionID,
		},
	}, nil
}

func bestDraftUnit(state *State, playerID string) (UnitType, bool) {
	available := availableDraftUnits(state.Draft)
	if len(available) == 0 {
		return "", false
	}
	roster := state.Draft.Rosters[playerID]
	score := func(unit UnitType) int {
		result := unitTotals[unit] * 10
		if containsUnit(roster, UnitMarshall) &&
			unit != UnitArcher &&
			unit != UnitLancer {
			result += 8
		}
		if containsUnit(roster, UnitEnsign) {
			result += 4
		}
		switch unit {
		case UnitPriest, UnitMercenary, UnitGuard:
			result += 12
		case UnitFootman, UnitBerserker, UnitKnight:
			result += 8
		case UnitArcher, UnitCrossbow, UnitLancer:
			result += 5
		}
		return result
	}
	sort.SliceStable(available, func(i, j int) bool {
		left := score(available[i])
		right := score(available[j])
		if left != right {
			return left > right
		}
		return available[i] < available[j]
	})
	return available[0], true
}

func bestAction(
	ctx context.Context,
	state *State,
	playerID string,
	actions []LegalAction,
) LegalAction {
	player := state.Roster[playerID]
	targets := []string{}
	for _, location := range locationCells {
		if state.Controls[location] != player.Side {
			targets = append(targets, location)
		}
	}
	nearest := func(cell string) int {
		if len(targets) == 0 {
			return 0
		}
		best := 1 << 30
		for _, target := range targets {
			best = minInt(best, distance(cell, target))
		}
		return best
	}
	value := func(action LegalAction) int {
		if ctx.Err() != nil {
			return -1 << 30
		}
		switch action.Action {
		case ActionDefend:
			if action.Defense == "supply" {
				return 120
			}
			return 0
		case ActionControl:
			result := 100
			if controlledLocations(state, player.Side) == 5 {
				result += 1000
			}
			return result
		case ActionAttack:
			target := findUnitByID(state, action.TargetUnitID)
			result := 50
			if target != nil {
				if len(target.CoinIDs) == 1 {
					result += 12
				}
				if target.UnitType == UnitPike {
					result -= 15
				}
			}
			return result
		case ActionDeploy:
			return 26 - nearest(action.To)
		case ActionMove:
			return (nearest(action.From)-nearest(action.To))*12 +
				boolScore(locationSet[action.To], 20) +
				4
		case ActionTactic:
			if action.Maneuver == ActionControl {
				result := 100
				if controlledLocations(state, player.Side) == 5 {
					result += 1000
				}
				return result
			}
			if action.TargetUnitID != "" {
				return 55
			}
			if action.To != "" {
				return (nearest(action.From)-nearest(action.To))*12 +
					boolScore(locationSet[action.To], 20) +
					4
			}
			return 2
		case ActionInitiative:
			return 18
		case ActionRecruit:
			result := 8
			if len(unitsOfType(state, player.Side, action.RecruitUnit)) > 0 {
				result += 4
			}
			if action.RecruitUnit == UnitMercenary &&
				len(unitsOfType(state, player.Side, UnitMercenary)) > 0 {
				result += 15
			}
			return result
		case ActionBolster:
			unit := findUnitByID(state, action.ActorUnitID)
			if unit != nil && len(unit.CoinIDs) < 2 {
				if unit.UnitType == UnitBerserker ||
					unit.UnitType == UnitKnight ||
					unit.UnitType == UnitGuard {
					return 20
				}
				return 6
			}
			return 0
		case ActionFinish:
			return -12
		default:
			return -20
		}
	}
	best := actions[0]
	bestValue := value(best)
	for _, action := range actions[1:] {
		current := value(action)
		if current > bestValue {
			best = action
			bestValue = current
		}
	}
	return best
}

func (p *Plugin) applyTimeout(
	ctx context.Context,
	state *State,
	playerID string,
	now time.Time,
) error {
	if state.Status == StatusMatch &&
		state.SetupMode == SetupDraft &&
		state.Draft != nil &&
		state.Draft.CurrentPickerID == playerID {
		unit, ok := bestDraftUnit(state, playerID)
		if !ok {
			return errors.New("draft has no available unit")
		}
		return draftUnit(
			state,
			playerID,
			mustJSON(map[string]any{"unitType": unit}),
			now,
		)
	}
	if state.CurrentPlayer != playerID {
		return errors.New("timeout player is no longer active")
	}
	actions := legalActions(state, playerID)
	if len(actions) == 0 {
		return errors.New("timeout has no legal action")
	}
	var selected *LegalAction
	pendingKind := ""
	if state.Pending != nil {
		pendingKind = state.Pending.Kind
	}
	for index := range actions {
		action := &actions[index]
		switch pendingKind {
		case "berserker", "mercenary", "sword":
			if action.Action == ActionFinish {
				selected = action
			}
		case "guard":
			if action.Action == ActionDefend && action.Defense == "unit" {
				selected = action
			}
		case "priest":
			if action.Action == ActionPass {
				selected = action
			}
		}
		if selected != nil {
			break
		}
	}
	if selected == nil && pendingKind == "footman" {
		value := bestAction(ctx, state, playerID, actions)
		selected = &value
	}
	if selected == nil && pendingKind == "" {
		player := state.Roster[playerID]
		if player != nil && len(player.Hand) > 0 {
			for index := range actions {
				if actions[index].CoinID == player.Hand[0] &&
					actions[index].Action == ActionPass {
					selected = &actions[index]
					break
				}
			}
		}
	}
	if selected == nil {
		value := bestAction(ctx, state, playerID, actions)
		selected = &value
	}
	return resolveAction(
		state,
		playerID,
		*selected,
		true,
		"",
		now,
	)
}

func nextAIRequest(state *State) *game.AIRequest {
	playerID := state.CurrentPlayer
	if state.Status == StatusMatch &&
		state.SetupMode == SetupDraft &&
		state.Draft != nil &&
		!state.Draft.Complete {
		playerID = state.Draft.CurrentPickerID
	}
	player := state.Roster[playerID]
	if player == nil ||
		!player.AI ||
		state.Status == StatusWaiting ||
		state.Status == StatusEnd {
		return nil
	}
	delayRange := uint64((aiMaxDelay - aiMinDelay) / time.Millisecond)
	offset := (state.RandomSeed +
		uint64(state.ActionCounter)*7919 +
		uint64(state.RoundNo)*104729 +
		uint64(len(state.Log))*17) % (delayRange + 1)
	fallback := contracts.RoomCommand{
		Type:     CommandTimeout,
		PlayerID: playerID,
	}
	return &game.AIRequest{
		RoomID:          state.RoomID,
		GameType:        GameType,
		PlayerID:        playerID,
		State:           cloneState(state),
		Delay:           aiMinDelay + time.Duration(offset)*time.Millisecond,
		FallbackCommand: &fallback,
	}
}

func boolScore(value bool, score int) int {
	if value {
		return score
	}
	return 0
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
