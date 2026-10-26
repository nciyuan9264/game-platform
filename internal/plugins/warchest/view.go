package warchest

import (
	"context"
	"errors"
	"fmt"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func (p *Plugin) BuildPlayerView(
	_ context.Context,
	state game.State,
	request game.ViewRequest,
) (any, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	if typed.Roster[request.PlayerID] == nil {
		return nil, errors.New("player is not in room")
	}
	players := playerViews(typed, request.PlayerProfiles)
	if typed.Status == StatusMatch {
		return matchView(typed, request, players), nil
	}
	return roomView(
		typed,
		request.PlayerID,
		request.MatchID,
		request.Seq,
		players,
		false,
	), nil
}

func (p *Plugin) BuildSpectatorView(
	_ context.Context,
	state game.State,
	seq int64,
) (any, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	return roomView(
		typed,
		"",
		"",
		seq,
		playerViews(typed, nil),
		false,
	), nil
}

func matchView(
	state *State,
	request game.ViewRequest,
	players map[string]any,
) map[string]any {
	sides := map[string]Side{}
	rosters := map[string][]UnitType{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		sides[playerID] = player.Side
		rosters[playerID] = copyUnits(player.Roster)
	}
	setup := map[string]any{
		"mode":     state.SetupMode,
		"sides":    sides,
		"rosters":  rosters,
		"complete": setupComplete(state),
	}
	if state.InitialPlayerID != "" {
		setup["initialPlayerID"] = state.InitialPlayerID
	}
	if state.InitialSide != "" {
		setup["initialSide"] = state.InitialSide
	}
	if state.Draft != nil {
		setup["draft"] = draftView(state)
	}
	return map[string]any{
		"type":        contracts.MessageTypeMatchSync,
		"roomID":      state.RoomID,
		"matchID":     request.MatchID,
		"ownerID":     state.OwnerID,
		"status":      state.Status,
		"playerID":    request.PlayerID,
		"visibility":  state.Visibility,
		"maxPlayers":  state.MaxPlayers,
		"players":     players,
		"playerOrder": state.PlayerOrder,
		"setup":       map[string]any{"warchest": setup},
	}
}

func draftView(state *State) map[string]any {
	draft := state.Draft
	if draft == nil {
		return nil
	}
	rosters := map[string][]UnitType{}
	for playerID, roster := range draft.Rosters {
		rosters[playerID] = copyUnits(roster)
	}
	result := map[string]any{
		"pool":               copyUnits(draft.Pool),
		"availableUnits":     availableDraftUnits(draft),
		"pickedUnits":        copyUnits(draft.PickedUnits),
		"rosters":            rosters,
		"firstPickerID":      draft.FirstPickerID,
		"currentPickCount":   currentDraftPickCount(draft),
		"completedPickCount": draft.CompletedPickCount,
		"complete":           draft.Complete,
	}
	if draft.CurrentPickerID != "" {
		result["currentPickerID"] = draft.CurrentPickerID
	}
	if deadline := deadlineString(draft.TurnDeadline); deadline != nil {
		result["turnDeadline"] = *deadline
		result["turnTimeoutMs"] = int64(defaultTurnTimeout) * 1000
	}
	return result
}

func roomView(
	state *State,
	playerID,
	matchID string,
	seq int64,
	players map[string]any,
	omniscient bool,
) map[string]any {
	playerData := projectedPlayerData(state, playerID, omniscient)
	legal := []LegalAction{}
	if !omniscient && playerID != "" {
		legal = legalActions(state, playerID)
	}
	log := projectedLog(state, playerID, omniscient)
	var last any
	if len(log) > 0 {
		entry := log[len(log)-1]
		last = entry
	}
	roomData := replayRoomData(
		state,
		players,
		projectLegalActions(legal),
		log,
		last,
	)
	view := map[string]any{
		"type":       contracts.MessageTypeRoomSync,
		"roomID":     state.RoomID,
		"matchID":    matchID,
		"seq":        seq,
		"playerId":   playerID,
		"ownerID":    state.OwnerID,
		"visibility": state.Visibility,
		"playerData": playerData,
		"privateData": privateData(
			state,
			playerID,
			omniscient,
		),
		"roomData": roomData,
	}
	if state.Status == StatusEnd {
		view["result"] = resultView(state)
	}
	return view
}

func replayRoomData(
	state *State,
	players map[string]any,
	legal []map[string]any,
	log []ActionEntry,
	last any,
) map[string]any {
	board := make([]map[string]any, 0, len(state.Board))
	for _, key := range boardCellOrder {
		unit := state.Board[key]
		if unit == nil {
			continue
		}
		board = append(board, map[string]any{
			"unitID":    unit.UnitID,
			"unitType":  unit.UnitType,
			"side":      unit.Side,
			"cell":      unit.Cell,
			"coinCount": len(unit.CoinIDs),
		})
	}
	controls := make([]map[string]any, 0, len(state.Controls))
	for _, cell := range locationCells {
		if side := state.Controls[cell]; side != "" {
			controls = append(controls, map[string]any{
				"cell": cell,
				"side": side,
			})
		}
	}
	currentSide := state.sideForPlayer(state.CurrentPlayer)
	initiativeSide := state.sideForPlayer(state.InitiativePlayer)
	result := map[string]any{
		"board":            board,
		"controls":         controls,
		"currentPlayer":    state.CurrentPlayer,
		"currentSide":      currentSide,
		"initiativePlayer": state.InitiativePlayer,
		"initiativeSide":   initiativeSide,
		"roundNo":          state.RoundNo,
		"gameStatus":       state.Status,
		"legalActions":     legal,
		"lastData":         last,
		"activityLog":      log,
		"players":          players,
		"playerOrder":      state.PlayerOrder,
	}
	if state.Pending != nil {
		result["pending"] = pendingView(state.Pending)
	}
	if deadline := deadlineString(state.TurnDeadline); deadline != nil {
		result["turnDeadline"] = *deadline
		result["turnTimeoutMs"] = state.TurnTimeout.Milliseconds()
	}
	return result
}

func projectedPlayerData(
	state *State,
	viewerID string,
	omniscient bool,
) map[string]any {
	result := map[string]any{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		supply := map[UnitType]int{}
		lost := map[UnitType]int{}
		for _, unit := range player.Roster {
			supply[unit] = len(player.Supply[unit])
			lost[unit] = len(player.Lost[unit])
		}
		discard := make([]map[string]any, 0, len(player.Discard))
		for _, value := range player.Discard {
			entry := map[string]any{
				"coinID": value.CoinID,
				"faceUp": value.FaceUp,
			}
			if value.FaceUp || viewerID == playerID || omniscient {
				entry["unitType"] = state.Coins[value.CoinID]
			}
			discard = append(discard, entry)
		}
		result[playerID] = map[string]any{
			"side":                player.Side,
			"roster":              copyUnits(player.Roster),
			"controlledLocations": controlledLocations(state, player.Side),
			"bagCount":            len(player.Bag),
			"handCount":           len(player.Hand),
			"supply":              supply,
			"discard":             discard,
			"lost":                lost,
		}
	}
	return result
}

func privateData(
	state *State,
	playerID string,
	omniscient bool,
) any {
	if omniscient {
		result := map[string]any{}
		for _, currentID := range state.PlayerOrder {
			result[currentID] = privatePlayerData(
				state,
				state.Roster[currentID],
				true,
			)
		}
		return result
	}
	player := state.Roster[playerID]
	if player == nil {
		return map[string]any{
			"hand":      []any{},
			"bagCounts": map[string]int{},
		}
	}
	return privatePlayerData(state, player, false)
}

func privatePlayerData(
	state *State,
	player *Player,
	includeOrder bool,
) map[string]any {
	hand := make([]map[string]any, 0, len(player.Hand))
	for _, coinID := range player.Hand {
		hand = append(hand, map[string]any{
			"coinID":   coinID,
			"unitType": state.Coins[coinID],
			"faceUp":   true,
		})
	}
	counts := map[UnitType]int{}
	for _, coinID := range player.Bag {
		counts[state.Coins[coinID]]++
	}
	result := map[string]any{
		"hand":      hand,
		"bagCounts": counts,
	}
	if includeOrder {
		order := make([]map[string]any, 0, len(player.Bag))
		for _, coinID := range player.Bag {
			order = append(order, map[string]any{
				"coinID":   coinID,
				"unitType": state.Coins[coinID],
				"faceUp":   false,
			})
		}
		result["bagOrder"] = order
	}
	return result
}

func projectedLog(
	state *State,
	viewerID string,
	omniscient bool,
) []ActionEntry {
	result := make([]ActionEntry, 0, len(state.Log))
	for _, action := range state.Log {
		entry := action
		entry.Path = append([]string(nil), action.Path...)
		if action.PrivateFor != "" &&
			action.PrivateFor != viewerID &&
			!omniscient {
			entry.CoinID = ""
			entry.UnitType = ""
		}
		entry.Private = action.PrivateFor != "" &&
			action.PrivateFor == viewerID &&
			!omniscient
		entry.PrivateFor = ""
		result = append(result, entry)
	}
	return result
}

func pendingView(value *PendingDecision) map[string]any {
	if value == nil {
		return nil
	}
	result := map[string]any{
		"kind":     value.Kind,
		"playerID": value.PlayerID,
		"side":     value.Side,
	}
	optional := map[string]string{
		"actorUnitID":  value.ActorUnitID,
		"sourceUnitID": value.SourceUnitID,
		"targetUnitID": value.TargetUnitID,
		"coinID":       value.CoinID,
		"description":  value.Description,
	}
	for key, current := range optional {
		if current != "" {
			result[key] = current
		}
	}
	if value.UnitType != "" {
		result["unitType"] = value.UnitType
	}
	if len(value.RemainingUnitIDs) > 0 {
		result["remainingUnitIDs"] = append(
			[]string(nil),
			value.RemainingUnitIDs...,
		)
	}
	return result
}

func resultView(state *State) map[string]any {
	return map[string]any{
		"winnerID":   state.WinnerID,
		"winnerSide": state.WinnerSide,
		"reason":     state.EndReason,
		"controlledLocations": map[string]int{
			state.playerIDForSide(SideOak): controlledLocations(
				state,
				SideOak,
			),
			state.playerIDForSide(SideFalcon): controlledLocations(
				state,
				SideFalcon,
			),
		},
		"roundNo":     state.RoundNo,
		"actionCount": state.ActionCounter,
	}
}

func playerViews(
	state *State,
	profiles map[string]game.PlayerProfile,
) map[string]any {
	result := make(map[string]any, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		displayName := player.DisplayName
		userID := player.UserID
		avatar := ""
		if profile, ok := profiles[playerID]; ok {
			userID = profile.UserID
			displayName = profile.DisplayName
			avatar = profile.Avatar
		}
		if displayName == "" {
			if player.AI {
				displayName = "渡鸦"
			} else {
				displayName = fmt.Sprintf("玩家 %s", playerID)
			}
		}
		result[playerID] = map[string]any{
			"userID":      userID,
			"playerID":    playerID,
			"displayName": displayName,
			"avatar":      avatar,
			"online":      player.Online,
			"ai":          player.AI,
			"ready":       player.Ready,
			"seatIndex":   player.SeatIndex,
			"gameRole":    player.Side,
		}
	}
	return result
}

func projectLegalActions(actions []LegalAction) []map[string]any {
	result := make([]map[string]any, 0, len(actions))
	for _, action := range actions {
		entry := map[string]any{
			"actionID": action.ActionID,
			"action":   action.Action,
		}
		optional := map[string]string{
			"coinID":       action.CoinID,
			"actorUnitID":  action.ActorUnitID,
			"sourceUnitID": action.SourceUnitID,
			"from":         action.From,
			"to":           action.To,
			"targetUnitID": action.TargetUnitID,
			"after":        action.After,
			"defense":      action.Defense,
		}
		for key, value := range optional {
			if value != "" {
				entry[key] = value
			}
		}
		if action.RecruitUnit != "" {
			entry["recruitUnit"] = action.RecruitUnit
		}
		if action.Maneuver != "" {
			entry["maneuver"] = action.Maneuver
		}
		if len(action.Path) > 0 {
			entry["path"] = append([]string(nil), action.Path...)
		}
		result = append(result, entry)
	}
	return result
}
