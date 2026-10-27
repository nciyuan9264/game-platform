package hex

import (
	"errors"
	"fmt"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func validateState(state *State) error {
	if state == nil {
		return errors.New("hex state is nil")
	}
	if state.MaxPlayers != defaultMaxPlayers {
		return errors.New("hex state must have exactly two seats")
	}
	if state.RandomSeed == 0 {
		return errors.New("hex state is missing random seed")
	}
	if state.RoundNo <= 0 {
		return errors.New("hex state has invalid round number")
	}
	if state.Visibility != contracts.VisibilityPublic &&
		state.Visibility != contracts.VisibilityPrivate {
		return errors.New("hex state has invalid visibility")
	}
	switch state.Status {
	case StatusMatch, StatusWaiting, StatusPlaying, StatusEnd:
	default:
		return errors.New("hex state has invalid status")
	}
	if state.Roster == nil || state.PlayerOrder == nil ||
		state.Board == nil || state.Log == nil ||
		state.WinningPath == nil {
		return errors.New("hex state has nil collections")
	}
	if err := validateRoster(state); err != nil {
		return err
	}
	if state.Status == StatusMatch {
		if len(state.Board) != 0 || len(state.Log) != 0 ||
			len(state.WinningPath) != 0 ||
			state.CurrentPlayer != "" ||
			state.RedPlayerID != "" || state.TealPlayerID != "" ||
			state.FirstPlayerID != "" ||
			state.WinnerID != "" || state.WinnerSide != "" {
			return errors.New("hex lobby contains game state")
		}
		for _, player := range state.Roster {
			if player.Side != "" {
				return errors.New("hex lobby contains side assignment")
			}
		}
		return nil
	}
	if err := validateGameAssignments(state); err != nil {
		return err
	}
	if len(state.Board) != boardSize*boardSize {
		return errors.New("hex board has invalid size")
	}
	for _, stone := range state.Board {
		if stone != StoneEmpty && stone != StoneRed && stone != StoneTeal {
			return errors.New("hex board contains invalid stone")
		}
	}
	if err := validateActions(state); err != nil {
		return err
	}
	return validateResult(state)
}

func validateRoster(state *State) error {
	if len(state.Roster) != len(state.PlayerOrder) ||
		len(state.Roster) > defaultMaxPlayers {
		return errors.New("hex roster is inconsistent")
	}
	seenSeats := map[int]bool{}
	seenPlayers := map[string]bool{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil || player.PlayerID != playerID ||
			player.SeatIndex < 0 || player.SeatIndex >= defaultMaxPlayers ||
			seenSeats[player.SeatIndex] || seenPlayers[playerID] {
			return errors.New("hex roster contains invalid player")
		}
		seenSeats[player.SeatIndex] = true
		seenPlayers[playerID] = true
	}
	return nil
}

func validateGameAssignments(state *State) error {
	if len(state.Roster) != defaultMaxPlayers ||
		state.RedPlayerID == "" || state.TealPlayerID == "" ||
		state.RedPlayerID == state.TealPlayerID ||
		state.FirstPlayerID == "" {
		return errors.New("hex side assignment is incomplete")
	}
	red := state.Roster[state.RedPlayerID]
	teal := state.Roster[state.TealPlayerID]
	if red == nil || teal == nil ||
		red.Side != SideRed || teal.Side != SideTeal ||
		(state.FirstPlayerID != state.RedPlayerID &&
			state.FirstPlayerID != state.TealPlayerID) {
		return errors.New("hex side assignment is inconsistent")
	}
	switch state.Status {
	case StatusWaiting:
		if state.CurrentPlayer != state.RedPlayerID {
			return errors.New("hex waiting state has invalid current player")
		}
	case StatusPlaying:
		if state.CurrentPlayer != state.RedPlayerID &&
			state.CurrentPlayer != state.TealPlayerID {
			return errors.New("hex playing state has invalid current player")
		}
	case StatusEnd:
		if state.CurrentPlayer != "" {
			return errors.New("hex ended state still has current player")
		}
	}
	return nil
}

func validateActions(state *State) error {
	placeCount := 0
	swapCount := 0
	for index, action := range state.Log {
		if action.ID != index+1 ||
			state.Roster[action.PlayerID] == nil ||
			(action.Side != SideRed && action.Side != SideTeal) {
			return errors.New("hex action log is invalid")
		}
		switch action.Kind {
		case "place":
			if action.Cell == nil || !inside(*action.Cell) {
				return errors.New("hex place action is invalid")
			}
			placeCount++
		case "swap":
			if action.Cell != nil || index != 1 {
				return errors.New("hex swap action is invalid")
			}
			swapCount++
		case "resign":
			if action.Cell != nil || index != len(state.Log)-1 {
				return errors.New("hex resign action is invalid")
			}
		default:
			return fmt.Errorf("hex action kind %q is invalid", action.Kind)
		}
	}
	if swapCount > 1 {
		return errors.New("hex action log has repeated swap")
	}
	red, teal := stoneCounts(state.Board)
	if red+teal != placeCount {
		return errors.New("hex stone count does not match action log")
	}
	if len(state.Log) == 0 {
		if state.LastAction != nil {
			return errors.New("hex empty log has last action")
		}
	} else if state.LastAction == nil ||
		state.LastAction.ID != state.Log[len(state.Log)-1].ID {
		return errors.New("hex last action is inconsistent")
	}
	return nil
}

func validateResult(state *State) error {
	redPath := connection(state.Board, SideRed)
	tealPath := connection(state.Board, SideTeal)
	if state.Status != StatusEnd {
		if state.WinnerID != "" || state.WinnerSide != "" ||
			len(state.WinningPath) != 0 ||
			len(redPath) != 0 || len(tealPath) != 0 {
			return errors.New("hex active state contains a winner")
		}
		return nil
	}
	if state.WinnerID == "" ||
		(state.WinnerSide != SideRed && state.WinnerSide != SideTeal) ||
		state.WinnerID != playerForSide(state, state.WinnerSide) {
		return errors.New("hex result has invalid winner")
	}
	resigned := len(state.Log) > 0 &&
		state.Log[len(state.Log)-1].Kind == "resign"
	if resigned {
		if len(state.WinningPath) != 0 {
			return errors.New("hex resignation contains a winning path")
		}
		return nil
	}
	path := redPath
	if state.WinnerSide == SideTeal {
		path = tealPath
	}
	if len(path) == 0 || !samePath(path, state.WinningPath) {
		return errors.New("hex result has invalid winning path")
	}
	if state.WinnerSide == SideRed && len(tealPath) > 0 ||
		state.WinnerSide == SideTeal && len(redPath) > 0 {
		return errors.New("hex board contains conflicting winners")
	}
	return nil
}

func samePath(left, right []Cell) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
