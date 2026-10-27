package onitama

import (
	"errors"
	"sort"
	"time"
)

func freshPieces() map[string]*Piece {
	result := map[string]*Piece{}
	addSide := func(side Side, row int) {
		student := 1
		for column := 0; column < 5; column++ {
			kind := PieceStudent
			id := string(side) + "-student-" + string(rune('0'+student))
			if column == 2 {
				kind = PieceMaster
				id = string(side) + "-master"
			} else {
				student++
			}
			result[id] = &Piece{
				ID:   id,
				Side: side,
				Kind: kind,
				Cell: Cell{Row: row, Column: column},
			}
		}
	}
	addSide(SideBlue, 4)
	addSide(SideRed, 0)
	return result
}

func inside(cell Cell) bool {
	return cell.Row >= 0 && cell.Row < 5 &&
		cell.Column >= 0 && cell.Column < 5
}

func templeFor(side Side) Cell {
	if side == SideBlue {
		return Cell{Row: 0, Column: 2}
	}
	return Cell{Row: 4, Column: 2}
}

func targetFor(piece Piece, offset Offset) Cell {
	if piece.Side == SideBlue {
		return Cell{
			Row:    piece.Cell.Row + offset.Y,
			Column: piece.Cell.Column + offset.X,
		}
	}
	return Cell{
		Row:    piece.Cell.Row - offset.Y,
		Column: piece.Cell.Column - offset.X,
	}
}

func pieceAt(state *State, cell Cell) *Piece {
	for _, piece := range state.Pieces {
		if piece != nil && piece.Cell == cell {
			return piece
		}
	}
	return nil
}

func legalMoves(state *State, side Side) []LegalMove {
	if state.Status != StatusPlaying {
		return []LegalMove{}
	}
	result := []LegalMove{}
	for _, cardID := range state.Hands[side] {
		card, ok := cardByID(cardID)
		if !ok {
			continue
		}
		for _, piece := range sortedPieces(state.Pieces) {
			if piece.Side != side {
				continue
			}
			for _, offset := range card.Moves {
				target := targetFor(*piece, offset)
				if !inside(target) {
					continue
				}
				occupant := pieceAt(state, target)
				if occupant != nil && occupant.Side == side {
					continue
				}
				move := LegalMove{
					CardID:  cardID,
					PieceID: piece.ID,
					To:      target,
					Winning: piece.Kind == PieceMaster &&
						target == templeFor(side),
				}
				if occupant != nil {
					move.CapturedPieceID = occupant.ID
					if occupant.Kind == PieceMaster {
						move.Winning = true
					}
				}
				result = append(result, move)
			}
		}
	}
	sortLegalMoves(result)
	return result
}

func movePiece(
	state *State,
	playerID,
	cardID,
	pieceID string,
	to Cell,
	automatic bool,
) error {
	if state.Status != StatusPlaying {
		return errors.New("not in playing phase")
	}
	if state.CurrentPlayer != playerID {
		return errors.New("not the active player")
	}
	side := sideForPlayer(state, playerID)
	if !handContains(state.Hands[side], cardID) {
		return errors.New("card is not in the active hand")
	}
	piece := state.Pieces[pieceID]
	if piece == nil || piece.Side != side {
		return errors.New("piece is not controlled by the active player")
	}
	var selected *LegalMove
	for _, move := range legalMoves(state, side) {
		if move.CardID == cardID &&
			move.PieceID == pieceID &&
			move.To == to {
			value := move
			selected = &value
			break
		}
	}
	if selected == nil {
		return errors.New("illegal onitama move")
	}
	from := piece.Cell
	capturedKind := PieceKind("")
	if selected.CapturedPieceID != "" {
		captured := state.Pieces[selected.CapturedPieceID]
		if captured == nil || captured.Side == side {
			return errors.New("invalid captured piece")
		}
		capturedKind = captured.Kind
		delete(state.Pieces, captured.ID)
	}
	piece.Cell = to
	action := Action{
		Kind:            "move",
		PlayerID:        playerID,
		Side:            side,
		CardID:          cardID,
		PieceID:         pieceID,
		From:            &from,
		To:              &to,
		CapturedPieceID: selected.CapturedPieceID,
		CapturedKind:    capturedKind,
		Automatic:       automatic,
	}
	if capturedKind == PieceMaster {
		appendAction(state, action)
		finish(state, playerID, side, "stone")
		return nil
	}
	if piece.Kind == PieceMaster && to == templeFor(side) {
		appendAction(state, action)
		finish(state, playerID, side, "stream")
		return nil
	}
	action.ReceivedCardID = state.TransitCardID
	exchangeCard(state, side, cardID)
	appendAction(state, action)
	state.CurrentPlayer = playerForSide(state, other(side))
	return nil
}

func passTurn(
	state *State,
	playerID,
	cardID string,
	automatic bool,
) error {
	if state.Status != StatusPlaying {
		return errors.New("not in playing phase")
	}
	if state.CurrentPlayer != playerID {
		return errors.New("not the active player")
	}
	side := sideForPlayer(state, playerID)
	if !handContains(state.Hands[side], cardID) {
		return errors.New("card is not in the active hand")
	}
	if len(legalMoves(state, side)) != 0 {
		return errors.New("a legal move is available")
	}
	received := state.TransitCardID
	exchangeCard(state, side, cardID)
	appendAction(state, Action{
		Kind:           "pass",
		PlayerID:       playerID,
		Side:           side,
		CardID:         cardID,
		ReceivedCardID: received,
		Automatic:      automatic,
	})
	state.CurrentPlayer = playerForSide(state, other(side))
	return nil
}

func resign(state *State, playerID string) error {
	if state.Status == StatusMatch || state.Status == StatusEnd {
		return errors.New("game is not active")
	}
	player := state.Roster[playerID]
	if player == nil ||
		(player.Side != SideBlue && player.Side != SideRed) {
		return errors.New("player not found")
	}
	appendAction(state, Action{
		Kind:     "resign",
		PlayerID: playerID,
		Side:     player.Side,
	})
	winnerSide := other(player.Side)
	finish(
		state,
		playerForSide(state, winnerSide),
		winnerSide,
		"resign",
	)
	return nil
}

func exchangeCard(state *State, side Side, cardID string) {
	hand := state.Hands[side]
	for index, current := range hand {
		if current == cardID {
			hand[index] = state.TransitCardID
			state.Hands[side] = hand
			state.TransitCardID = cardID
			return
		}
	}
}

func finish(state *State, winnerID string, winnerSide Side, reason string) {
	state.Status = StatusEnd
	state.WinnerID = winnerID
	state.WinnerSide = winnerSide
	state.EndReason = reason
	state.CurrentPlayer = ""
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
}

func appendAction(state *State, action Action) {
	action.ID = len(state.Log) + 1
	value := cloneAction(action)
	state.Log = append(state.Log, value)
	state.LastAction = &value
}

func handContains(hand []string, target string) bool {
	for _, cardID := range hand {
		if cardID == target {
			return true
		}
	}
	return false
}

func sideForPlayer(state *State, playerID string) Side {
	if player := state.Roster[playerID]; player != nil {
		return player.Side
	}
	if playerID == state.BluePlayerID {
		return SideBlue
	}
	if playerID == state.RedPlayerID {
		return SideRed
	}
	return ""
}

func playerForSide(state *State, side Side) string {
	if side == SideBlue {
		return state.BluePlayerID
	}
	if side == SideRed {
		return state.RedPlayerID
	}
	return ""
}

func pieceCount(state *State, side Side) int {
	total := 0
	for _, piece := range state.Pieces {
		if piece != nil && piece.Side == side {
			total++
		}
	}
	return total
}

func masterAlive(state *State, side Side) bool {
	piece := state.Pieces[string(side)+"-master"]
	return piece != nil && piece.Kind == PieceMaster && piece.Side == side
}

func allCandidates(state *State, side Side) []candidate {
	moves := legalMoves(state, side)
	result := make([]candidate, 0, len(moves))
	for _, move := range moves {
		result = append(result, candidate{
			Kind:    "move",
			CardID:  move.CardID,
			PieceID: move.PieceID,
			To:      move.To,
		})
	}
	if len(result) == 0 && state.Status == StatusPlaying {
		for _, cardID := range state.Hands[side] {
			result = append(result, candidate{
				Kind:   "pass",
				CardID: cardID,
			})
		}
	}
	sortCandidates(result)
	return result
}

func applyCandidate(
	state *State,
	side Side,
	action candidate,
	automatic bool,
) bool {
	playerID := playerForSide(state, side)
	var err error
	if action.Kind == "pass" {
		err = passTurn(state, playerID, action.CardID, automatic)
	} else {
		err = movePiece(
			state,
			playerID,
			action.CardID,
			action.PieceID,
			action.To,
			automatic,
		)
	}
	return err == nil
}

func sortedPieces(values map[string]*Piece) []*Piece {
	result := make([]*Piece, 0, len(values))
	for _, piece := range values {
		if piece != nil {
			result = append(result, piece)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

func sortLegalMoves(values []LegalMove) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].CardID != values[j].CardID {
			return values[i].CardID < values[j].CardID
		}
		if values[i].PieceID != values[j].PieceID {
			return values[i].PieceID < values[j].PieceID
		}
		if values[i].To.Row != values[j].To.Row {
			return values[i].To.Row < values[j].To.Row
		}
		return values[i].To.Column < values[j].To.Column
	})
}

func sortCandidates(values []candidate) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].CardID != values[j].CardID {
			return values[i].CardID < values[j].CardID
		}
		if values[i].PieceID != values[j].PieceID {
			return values[i].PieceID < values[j].PieceID
		}
		if values[i].To.Row != values[j].To.Row {
			return values[i].To.Row < values[j].To.Row
		}
		return values[i].To.Column < values[j].To.Column
	})
}
