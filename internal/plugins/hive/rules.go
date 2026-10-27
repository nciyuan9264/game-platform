package hive

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

var directions = []Hex{
	{Q: 1, R: 0},
	{Q: 0, R: 1},
	{Q: -1, R: 1},
	{Q: -1, R: 0},
	{Q: 0, R: -1},
	{Q: 1, R: -1},
}

var kindOrder = []Kind{
	KindQueen,
	KindAnt,
	KindBeetle,
	KindGrasshopper,
	KindSpider,
}

var pieceCounts = map[Kind]int{
	KindQueen:       1,
	KindAnt:         3,
	KindBeetle:      2,
	KindGrasshopper: 3,
	KindSpider:      2,
}

var kindCodes = map[Kind]string{
	KindQueen:       "Q",
	KindAnt:         "A",
	KindBeetle:      "B",
	KindGrasshopper: "G",
	KindSpider:      "S",
}

var pieces, piecesByID = buildPieces()

func buildPieces() ([]Piece, map[string]Piece) {
	all := make([]Piece, 0, 22)
	byID := make(map[string]Piece, 22)
	for _, side := range []Side{SideWhite, SideBlack} {
		prefix := "w"
		if side == SideBlack {
			prefix = "b"
		}
		for _, kind := range kindOrder {
			for number := 1; number <= pieceCounts[kind]; number++ {
				piece := Piece{
					PieceID: fmt.Sprintf("%s%s%d", prefix, kindCodes[kind], number),
					Side:    side,
					Kind:    kind,
					Number:  number,
				}
				all = append(all, piece)
				byID[piece.PieceID] = piece
			}
		}
	}
	return all, byID
}

func key(value Hex) string {
	return fmt.Sprintf("%d,%d", value.Q, value.R)
}

func parseHex(value string) (Hex, bool) {
	parts := strings.Split(value, ",")
	if len(parts) != 2 {
		return Hex{}, false
	}
	q, qErr := strconv.Atoi(parts[0])
	r, rErr := strconv.Atoi(parts[1])
	if qErr != nil || rErr != nil || key(Hex{Q: q, R: r}) != value {
		return Hex{}, false
	}
	return Hex{Q: q, R: r}, true
}

func add(left, right Hex) Hex {
	return Hex{Q: left.Q + right.Q, R: left.R + right.R}
}

func same(left, right Hex) bool {
	return left == right
}

func neighbors(value Hex) []Hex {
	result := make([]Hex, len(directions))
	for index, direction := range directions {
		result[index] = add(value, direction)
	}
	return result
}

func distance(left, right Hex) int {
	return (abs(left.Q-right.Q) +
		abs(left.R-right.R) +
		abs(left.Q+left.R-right.Q-right.R)) / 2
}

func stackHeight(board map[string][]string, position Hex) int {
	return len(board[key(position)])
}

func topPiece(board map[string][]string, position Hex) (Piece, bool) {
	stack := board[key(position)]
	if len(stack) == 0 {
		return Piece{}, false
	}
	piece, ok := piecesByID[stack[len(stack)-1]]
	return piece, ok
}

func locate(state *State, pieceID string) (Hex, int, []string, bool) {
	for position, stack := range state.Board {
		for level, current := range stack {
			if current == pieceID {
				parsed, ok := parseHex(position)
				return parsed, level, stack, ok
			}
		}
	}
	return Hex{}, 0, nil, false
}

func reserve(state *State, side Side) []Piece {
	placed := make(map[string]bool, len(pieces))
	for _, stack := range state.Board {
		for _, pieceID := range stack {
			placed[pieceID] = true
		}
	}
	result := make([]Piece, 0, 11)
	for _, piece := range pieces {
		if piece.Side == side && !placed[piece.PieceID] {
			result = append(result, piece)
		}
	}
	return result
}

func queenLocation(state *State, side Side) (Hex, bool) {
	pieceID := "wQ1"
	if side == SideBlack {
		pieceID = "bQ1"
	}
	position, _, _, ok := locate(state, pieceID)
	return position, ok
}

func queenPressure(state *State, side Side) int {
	position, ok := queenLocation(state, side)
	if !ok {
		return 0
	}
	count := 0
	for _, neighbor := range neighbors(position) {
		if stackHeight(state.Board, neighbor) > 0 {
			count++
		}
	}
	return count
}

func queenDue(state *State, side Side) bool {
	_, placed := queenLocation(state, side)
	return !placed && state.Turns[side] >= 3
}

func connected(board map[string][]string) bool {
	cells := make([]string, 0, len(board))
	for position, stack := range board {
		if len(stack) > 0 {
			cells = append(cells, position)
		}
	}
	if len(cells) == 0 {
		return true
	}
	seen := map[string]bool{cells[0]: true}
	queue := []string{cells[0]}
	for head := 0; head < len(queue); head++ {
		position, _ := parseHex(queue[head])
		for _, neighbor := range neighbors(position) {
			neighborKey := key(neighbor)
			if len(board[neighborKey]) > 0 && !seen[neighborKey] {
				seen[neighborKey] = true
				queue = append(queue, neighborKey)
			}
		}
	}
	return len(seen) == len(cells)
}

func lifted(state *State, pieceID string) map[string][]string {
	board := make(map[string][]string, len(state.Board))
	for position, stack := range state.Board {
		board[position] = append([]string(nil), stack...)
	}
	position, _, _, ok := locate(state, pieceID)
	if !ok {
		return board
	}
	positionKey := key(position)
	stack := board[positionKey]
	board[positionKey] = stack[:len(stack)-1]
	if len(board[positionKey]) == 0 {
		delete(board, positionKey)
	}
	return board
}

func commonNeighbors(left, right Hex) []Hex {
	result := []Hex{}
	for _, candidate := range neighbors(left) {
		if distance(candidate, right) == 1 {
			result = append(result, candidate)
		}
	}
	return result
}

func groundSlide(board map[string][]string, from, to Hex) bool {
	if distance(from, to) != 1 || stackHeight(board, to) > 0 {
		return false
	}
	gates := commonNeighbors(from, to)
	if len(gates) != 2 {
		return false
	}
	left := stackHeight(board, gates[0]) > 0
	right := stackHeight(board, gates[1]) > 0
	return left != right
}

func beetleStep(
	board map[string][]string,
	from,
	to Hex,
	sourceHeight int,
) bool {
	if distance(from, to) != 1 {
		return false
	}
	targetHeight := stackHeight(board, to)
	if sourceHeight == 1 && targetHeight == 0 {
		return groundSlide(board, from, to)
	}
	travelLevel := max(sourceHeight, targetHeight+1)
	blocked := true
	for _, gate := range commonNeighbors(from, to) {
		if stackHeight(board, gate) < travelLevel {
			blocked = false
		}
	}
	if blocked {
		return false
	}
	if targetHeight > 0 {
		return true
	}
	for _, neighbor := range neighbors(to) {
		if stackHeight(board, neighbor) > 0 {
			return true
		}
	}
	return false
}

func placementReason(state *State, pieceID string, target *Hex) string {
	piece, ok := piecesByID[pieceID]
	if state.Status != StatusPlaying {
		return "game is not active"
	}
	if !ok || piece.Side != state.CurrentSide {
		return "piece does not belong to the active side"
	}
	if _, _, _, placed := locate(state, pieceID); placed {
		return "piece is already in the hive"
	}
	if queenDue(state, piece.Side) && piece.Kind != KindQueen {
		return "queen must be placed on the fourth turn"
	}
	if piece.Kind == KindQueen && state.Turns[piece.Side] == 0 {
		return "queen cannot be the first piece"
	}
	if target == nil {
		return ""
	}
	if stackHeight(state.Board, *target) > 0 {
		return "placement target is occupied"
	}
	if len(state.Board) == 0 {
		if *target == (Hex{}) {
			return ""
		}
		return "first piece must be placed at the origin"
	}
	adjacent := make([]Piece, 0, 6)
	for _, neighbor := range neighbors(*target) {
		if top, found := topPiece(state.Board, neighbor); found {
			adjacent = append(adjacent, top)
		}
	}
	if len(adjacent) == 0 {
		return "piece must touch the hive"
	}
	if state.Turns[piece.Side] == 0 {
		return ""
	}
	for _, current := range adjacent {
		if current.Side != piece.Side {
			return "new piece cannot touch the opponent"
		}
	}
	return ""
}

func placementOptions(state *State, pieceID string) []candidate {
	if placementReason(state, pieceID, nil) != "" {
		return []candidate{}
	}
	candidates := map[string]Hex{}
	if len(state.Board) == 0 {
		candidates["0,0"] = Hex{}
	}
	for position := range state.Board {
		parsed, ok := parseHex(position)
		if !ok {
			continue
		}
		for _, neighbor := range neighbors(parsed) {
			if stackHeight(state.Board, neighbor) == 0 {
				candidates[key(neighbor)] = neighbor
			}
		}
	}
	result := []candidate{}
	for _, target := range candidates {
		if placementReason(state, pieceID, &target) == "" {
			result = append(result, candidate{
				Kind:    "place",
				PieceID: pieceID,
				To:      target,
				Path:    []Hex{target},
			})
		}
	}
	sortCandidates(result)
	return result
}

func moveBlock(state *State, pieceID string) string {
	piece, ok := piecesByID[pieceID]
	position, level, stack, placed := locate(state, pieceID)
	_ = position
	if state.Status != StatusPlaying {
		return "game is not active"
	}
	if !ok || !placed {
		return "piece is not in the hive"
	}
	if level != len(stack)-1 {
		return "piece is covered"
	}
	if piece.Side != state.CurrentSide {
		return "piece does not belong to the active side"
	}
	if _, queenPlaced := queenLocation(state, piece.Side); !queenPlaced {
		return "queen must be placed before moving"
	}
	if !connected(lifted(state, pieceID)) {
		return "move would split the hive"
	}
	return ""
}

func moveOptions(state *State, pieceID string) []candidate {
	if moveBlock(state, pieceID) != "" {
		return []candidate{}
	}
	from, _, stack, _ := locate(state, pieceID)
	piece := piecesByID[pieceID]
	board := lifted(state, pieceID)
	found := map[string]candidate{}
	remember := func(target Hex, path []Hex) {
		if target == from {
			return
		}
		targetKey := key(target)
		if _, exists := found[targetKey]; !exists {
			found[targetKey] = candidate{
				Kind:    "move",
				PieceID: pieceID,
				To:      target,
				Path:    append([]Hex(nil), path...),
			}
		}
	}
	switch piece.Kind {
	case KindQueen, KindBeetle:
		for _, target := range neighbors(from) {
			legal := groundSlide(board, from, target)
			if piece.Kind == KindBeetle {
				legal = beetleStep(board, from, target, len(stack))
			}
			if legal {
				remember(target, []Hex{from, target})
			}
		}
	case KindGrasshopper:
		for _, direction := range directions {
			target := add(from, direction)
			if stackHeight(board, target) == 0 {
				continue
			}
			path := []Hex{from}
			for stackHeight(board, target) > 0 {
				path = append(path, target)
				target = add(target, direction)
			}
			remember(target, append(path, target))
		}
	case KindSpider:
		var walk func(Hex, []Hex)
		walk = func(at Hex, path []Hex) {
			if len(path) == 4 {
				remember(at, path)
				return
			}
			for _, target := range neighbors(at) {
				revisited := false
				for _, step := range path {
					if step == target {
						revisited = true
						break
					}
				}
				if !revisited && groundSlide(board, at, target) {
					walk(target, append(append([]Hex(nil), path...), target))
				}
			}
		}
		walk(from, []Hex{from})
	case KindAnt:
		visited := map[string]bool{key(from): true}
		queue := []candidate{{To: from, Path: []Hex{from}}}
		for head := 0; head < len(queue); head++ {
			for _, target := range neighbors(queue[head].To) {
				targetKey := key(target)
				if visited[targetKey] ||
					!groundSlide(board, queue[head].To, target) {
					continue
				}
				path := append(
					append([]Hex(nil), queue[head].Path...),
					target,
				)
				visited[targetKey] = true
				queue = append(queue, candidate{To: target, Path: path})
				remember(target, path)
			}
		}
	}
	result := make([]candidate, 0, len(found))
	for _, option := range found {
		result = append(result, option)
	}
	sortCandidates(result)
	return result
}

func legalActions(state *State) []candidate {
	if state.Status != StatusPlaying {
		return []candidate{}
	}
	result := []candidate{}
	available := reserve(state, state.CurrentSide)
	for _, kind := range kindOrder {
		for _, piece := range available {
			if piece.Kind == kind {
				result = append(result, placementOptions(state, piece.PieceID)...)
				break
			}
		}
	}
	if _, placed := queenLocation(state, state.CurrentSide); placed {
		for _, stack := range state.Board {
			if len(stack) == 0 {
				continue
			}
			pieceID := stack[len(stack)-1]
			if piecesByID[pieceID].Side == state.CurrentSide {
				result = append(result, moveOptions(state, pieceID)...)
			}
		}
	}
	sortCandidates(result)
	return result
}

func detectOutcome(state *State) *Outcome {
	_, whitePlaced := queenLocation(state, SideWhite)
	_, blackPlaced := queenLocation(state, SideBlack)
	white := whitePlaced && queenPressure(state, SideWhite) == 6
	black := blackPlaced && queenPressure(state, SideBlack) == 6
	switch {
	case white && black:
		return &Outcome{Draw: true, Reason: "双方蜂后同时被围住"}
	case white:
		return &Outcome{
			WinnerSide: SideBlack,
			Reason:     "白方蜂后六边被围住",
		}
	case black:
		return &Outcome{
			WinnerSide: SideWhite,
			Reason:     "黑方蜂后六边被围住",
		}
	default:
		return nil
	}
}

func placePiece(
	state *State,
	playerID,
	pieceID string,
	target Hex,
	automatic bool,
) error {
	if state.CurrentPlayer != playerID {
		return errors.New("not the active player")
	}
	if reason := placementReason(state, pieceID, &target); reason != "" {
		return errors.New(reason)
	}
	return commitPiece(state, playerID, "place", pieceID, target, automatic)
}

func movePiece(
	state *State,
	playerID,
	pieceID string,
	target Hex,
	automatic bool,
) error {
	if state.CurrentPlayer != playerID {
		return errors.New("not the active player")
	}
	if reason := moveBlock(state, pieceID); reason != "" {
		return errors.New(reason)
	}
	legal := false
	for _, option := range moveOptions(state, pieceID) {
		if option.To == target {
			legal = true
			break
		}
	}
	if !legal {
		return errors.New("illegal piece move")
	}
	return commitPiece(state, playerID, "move", pieceID, target, automatic)
}

func commitPiece(
	state *State,
	playerID,
	actionKind,
	pieceID string,
	target Hex,
	automatic bool,
) error {
	piece := piecesByID[pieceID]
	var from *Hex
	path := []Hex{target}
	if actionKind == "move" {
		position, _, _, _ := locate(state, pieceID)
		from = &position
		for _, option := range moveOptions(state, pieceID) {
			if option.To == target {
				path = append([]Hex(nil), option.Path...)
				break
			}
		}
		state.Board = lifted(state, pieceID)
	}
	targetKey := key(target)
	state.Board[targetKey] = append(state.Board[targetKey], pieceID)
	to := target
	appendAction(state, Action{
		Kind:      actionKind,
		PlayerID:  playerID,
		Side:      piece.Side,
		PieceID:   pieceID,
		From:      from,
		To:        &to,
		Path:      path,
		Automatic: automatic,
	})
	state.Turns[piece.Side]++
	state.ConsecutivePasses = 0
	if outcome := detectOutcome(state); outcome != nil {
		if outcome.Draw {
			finishDraw(state, outcome.Reason)
		} else {
			finishWin(
				state,
				playerForSide(state, outcome.WinnerSide),
				outcome.WinnerSide,
				outcome.Reason,
			)
		}
		return nil
	}
	state.CurrentSide = other(piece.Side)
	state.CurrentPlayer = playerForSide(state, state.CurrentSide)
	return nil
}

func forcePass(state *State, playerID string) error {
	if state.Status != StatusPlaying ||
		state.CurrentPlayer != playerID {
		return errors.New("pass player is no longer active")
	}
	if len(legalActions(state)) > 0 {
		return errors.New("active player still has a legal action")
	}
	side := state.CurrentSide
	appendAction(state, Action{
		Kind:      "pass",
		PlayerID:  playerID,
		Side:      side,
		Path:      []Hex{},
		Automatic: true,
	})
	state.Turns[side]++
	state.ConsecutivePasses++
	if state.ConsecutivePasses >= 2 {
		finishDraw(state, "双方连续无合法行动")
		return nil
	}
	state.CurrentSide = other(side)
	state.CurrentPlayer = playerForSide(state, state.CurrentSide)
	return nil
}

func resign(state *State, playerID string) error {
	if state.Status != StatusPlaying {
		return errors.New("game is not active")
	}
	player := state.Roster[playerID]
	if player == nil ||
		(player.Side != SideWhite && player.Side != SideBlack) {
		return errors.New("player not found")
	}
	appendAction(state, Action{
		Kind:     "resign",
		PlayerID: playerID,
		Side:     player.Side,
		Path:     []Hex{},
	})
	winnerSide := other(player.Side)
	finishWin(
		state,
		playerForSide(state, winnerSide),
		winnerSide,
		sideName(player.Side)+"主动认输，对局结束",
	)
	return nil
}

func finishWin(state *State, winnerID string, winnerSide Side, reason string) {
	state.Status = StatusEnd
	state.WinnerID = winnerID
	state.WinnerSide = winnerSide
	state.Draw = false
	state.EndReason = reason
	state.CurrentPlayer = ""
	state.CurrentSide = ""
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
}

func finishDraw(state *State, reason string) {
	state.Status = StatusEnd
	state.WinnerID = ""
	state.WinnerSide = ""
	state.Draw = true
	state.EndReason = reason
	state.CurrentPlayer = ""
	state.CurrentSide = ""
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
}

func appendAction(state *State, action Action) {
	action.ID = len(state.Log) + 1
	value := cloneAction(action)
	state.Log = append(state.Log, value)
	state.LastAction = &value
}

func sideForPlayer(state *State, playerID string) Side {
	if player := state.Roster[playerID]; player != nil {
		return player.Side
	}
	return ""
}

func playerForSide(state *State, side Side) string {
	if side == SideWhite {
		return state.WhitePlayerID
	}
	if side == SideBlack {
		return state.BlackPlayerID
	}
	return ""
}

func applyCandidate(state *State, option candidate, automatic bool) bool {
	playerID := state.CurrentPlayer
	var err error
	if option.Kind == "place" {
		err = placePiece(
			state,
			playerID,
			option.PieceID,
			option.To,
			automatic,
		)
	} else {
		err = movePiece(
			state,
			playerID,
			option.PieceID,
			option.To,
			automatic,
		)
	}
	return err == nil
}

func sortCandidates(values []candidate) {
	kindRank := map[string]int{"place": 0, "move": 1}
	pieceRank := map[Kind]int{}
	for index, kind := range kindOrder {
		pieceRank[kind] = index
	}
	sort.Slice(values, func(i, j int) bool {
		left, right := values[i], values[j]
		if kindRank[left.Kind] != kindRank[right.Kind] {
			return kindRank[left.Kind] < kindRank[right.Kind]
		}
		leftPiece, rightPiece := piecesByID[left.PieceID], piecesByID[right.PieceID]
		if pieceRank[leftPiece.Kind] != pieceRank[rightPiece.Kind] {
			return pieceRank[leftPiece.Kind] < pieceRank[rightPiece.Kind]
		}
		if leftPiece.Number != rightPiece.Number {
			return leftPiece.Number < rightPiece.Number
		}
		if left.To.Q != right.To.Q {
			return left.To.Q < right.To.Q
		}
		return left.To.R < right.To.R
	})
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
