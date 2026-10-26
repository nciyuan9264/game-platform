package yinsh

import (
	"errors"
	"fmt"
)

func initializeBoard(state *State) error {
	if len(state.PlayerOrder) != 2 {
		return errors.New("yinsh requires exactly two players")
	}
	whiteIndex := 0
	if state.RoundNo%2 == 0 {
		whiteIndex = 1
	}
	for index, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			return fmt.Errorf("player %s is missing", playerID)
		}
		if index == whiteIndex {
			player.Side = SideWhite
		} else {
			player.Side = SideBlack
		}
	}
	state.Rings = map[string]Side{}
	state.Markers = map[string]Side{}
	state.PlacedRings = map[Side]int{SideWhite: 0, SideBlack: 0}
	state.RingsRemoved = map[Side]int{SideWhite: 0, SideBlack: 0}
	state.CurrentPlayer = ""
	state.MoverPlayer = ""
	state.ResolverPlayer = ""
	state.Resolution = nil
	state.ConsecutivePasses = 0
	state.LastAction = nil
	state.Log = []Action{}
	state.WinnerID = ""
	state.EndReason = ""
	return nil
}

func startGame(state *State) error {
	white := playerForSide(state, SideWhite)
	if white == "" {
		return errors.New("white player is unavailable")
	}
	state.Status = StatusPlacement
	state.CurrentPlayer = white
	addAction(state, "game_started", "", map[string]any{
		"whitePlayer": white,
		"roundNo":     state.RoundNo,
	})
	return nil
}

func placeRing(state *State, playerID, pointID string) error {
	if state.Status != StatusPlacement || state.CurrentPlayer != playerID {
		return errors.New("it is not this player's placement turn")
	}
	if _, ok := pointByID[pointID]; !ok {
		return errors.New("invalid board point")
	}
	if state.Rings[pointID] != "" || state.Markers[pointID] != "" {
		return errors.New("board point is occupied")
	}
	side := sideForPlayer(state, playerID)
	if side == "" || state.PlacedRings[side] >= 5 {
		return errors.New("player cannot place another ring")
	}
	state.Rings[pointID] = side
	state.PlacedRings[side]++
	addAction(state, "place_ring", playerID, map[string]any{
		"pointID": pointID,
		"side":    side,
	})
	if state.PlacedRings[SideWhite] == 5 && state.PlacedRings[SideBlack] == 5 {
		state.Status = StatusMove
		state.CurrentPlayer = playerForSide(state, SideWhite)
		state.ConsecutivePasses = 0
		return nil
	}
	state.CurrentPlayer = playerForSide(state, other(side))
	return nil
}

func moveRing(state *State, playerID, from, to string) error {
	if state.Status != StatusMove || state.CurrentPlayer != playerID {
		return errors.New("it is not this player's move")
	}
	if markerPool(state) <= 0 {
		return errors.New("marker pool is empty")
	}
	side := sideForPlayer(state, playerID)
	if state.Rings[from] != side {
		return errors.New("source ring is not owned by player")
	}
	var selected *MoveOption
	for _, option := range legalMoves(state, from) {
		if option.To == to {
			copyOption := option
			selected = &copyOption
			break
		}
	}
	if selected == nil {
		return errors.New("illegal ring move")
	}
	delete(state.Rings, from)
	state.Rings[to] = side
	state.Markers[from] = side
	for _, pointID := range selected.Flips {
		state.Markers[pointID] = other(state.Markers[pointID])
	}
	state.MoverPlayer = playerID
	state.ResolverPlayer = ""
	state.Resolution = []string{
		playerID,
		playerForSide(state, other(side)),
	}
	state.ConsecutivePasses = 0
	addAction(state, "move_ring", playerID, map[string]any{
		"from":  from,
		"to":    to,
		"side":  side,
		"flips": append([]string(nil), selected.Flips...),
	})
	advanceResolution(state)
	return nil
}

func removeRow(state *State, playerID, rowID string) error {
	if state.Status != StatusRemoveRow ||
		state.CurrentPlayer != playerID ||
		state.ResolverPlayer != playerID {
		return errors.New("player cannot remove a row now")
	}
	side := sideForPlayer(state, playerID)
	var selected *Row
	for _, row := range rowsFor(state, side) {
		if row.ID == rowID {
			copyRow := row
			selected = &copyRow
			break
		}
	}
	if selected == nil {
		return errors.New("row is not available")
	}
	for _, pointID := range selected.Points {
		delete(state.Markers, pointID)
	}
	state.Status = StatusRemoveRing
	addAction(state, "remove_row", playerID, map[string]any{
		"rowID":  selected.ID,
		"points": append([]string(nil), selected.Points...),
		"side":   side,
	})
	return nil
}

func removeRing(state *State, playerID, pointID string) error {
	if state.Status != StatusRemoveRing ||
		state.CurrentPlayer != playerID ||
		state.ResolverPlayer != playerID {
		return errors.New("player cannot remove a ring now")
	}
	side := sideForPlayer(state, playerID)
	if state.Rings[pointID] != side {
		return errors.New("ring is not owned by player")
	}
	delete(state.Rings, pointID)
	state.RingsRemoved[side]++
	addAction(state, "remove_ring", playerID, map[string]any{
		"pointID":      pointID,
		"side":         side,
		"removedCount": state.RingsRemoved[side],
	})
	if state.RingsRemoved[side] >= 3 {
		finish(state, playerID, "率先取走第三枚圆环")
		return nil
	}
	advanceResolution(state)
	return nil
}

func resign(state *State, playerID string) error {
	if state.Status == StatusMatch || state.Status == StatusWaiting || state.Status == StatusEnd {
		return errors.New("game is not active")
	}
	if state.Roster[playerID] == nil {
		return errors.New("player is not in room")
	}
	side := sideForPlayer(state, playerID)
	winner := playerForSide(state, other(side))
	addAction(state, "resign", playerID, map[string]any{"side": side})
	finish(state, winner, "对手认输")
	return nil
}

func advanceResolution(state *State) {
	for len(state.Resolution) > 0 {
		playerID := state.Resolution[0]
		side := sideForPlayer(state, playerID)
		if len(rowsFor(state, side)) > 0 {
			state.Status = StatusRemoveRow
			state.CurrentPlayer = playerID
			state.ResolverPlayer = playerID
			return
		}
		state.Resolution = state.Resolution[1:]
	}
	state.ResolverPlayer = ""
	if markerPool(state) == 0 {
		finishByScore(state, "公共棋子池已耗尽")
		return
	}
	next := playerForSide(state, other(sideForPlayer(state, state.MoverPlayer)))
	state.MoverPlayer = ""
	state.Status = StatusMove
	state.CurrentPlayer = next
	advanceBlockedPlayers(state)
}

func advanceBlockedPlayers(state *State) {
	for state.Status == StatusMove && !hasLegalMove(state, state.CurrentPlayer) {
		playerID := state.CurrentPlayer
		state.ConsecutivePasses++
		addAction(state, "pass", playerID, map[string]any{"reason": "no_legal_move"})
		if state.ConsecutivePasses >= 2 {
			finishByScore(state, "双方均无合法移动")
			return
		}
		state.CurrentPlayer = playerForSide(state, other(sideForPlayer(state, playerID)))
	}
}

func finishByScore(state *State, reason string) {
	whiteScore := state.RingsRemoved[SideWhite]
	blackScore := state.RingsRemoved[SideBlack]
	switch {
	case whiteScore > blackScore:
		finish(state, playerForSide(state, SideWhite), reason)
	case blackScore > whiteScore:
		finish(state, playerForSide(state, SideBlack), reason)
	default:
		finish(state, "", reason+"，双方取环数相同")
	}
}

func finish(state *State, winnerID, reason string) {
	state.Status = StatusEnd
	state.WinnerID = winnerID
	state.EndReason = reason
	state.CurrentPlayer = ""
	state.MoverPlayer = ""
	state.ResolverPlayer = ""
	state.Resolution = nil
	state.TurnDeadline = state.TurnDeadline.UTC()
	state.TurnTimeout = 0
}

func addAction(state *State, action, playerID string, payload map[string]any) {
	entry := Action{
		ID:       len(state.Log) + 1,
		Action:   action,
		PlayerID: playerID,
		Payload:  payload,
	}
	state.Log = append(state.Log, entry)
	copyEntry := cloneAction(entry)
	state.LastAction = &copyEntry
}
