package yinsh

import (
	"fmt"
	"sort"
	"strings"
)

type Point struct {
	ID string
	Q  int
	R  int
}

var directions = [][2]int{
	{1, 0},
	{0, 1},
	{-1, 1},
	{-1, 0},
	{0, -1},
	{1, -1},
}

var (
	boardPoints  []Point
	pointByID    map[string]Point
	pointByCoord map[[2]int]Point
)

func init() {
	pointByID = map[string]Point{}
	pointByCoord = map[[2]int]Point{}
	for q := -5; q <= 5; q++ {
		for r := -5; r <= 5; r++ {
			if max3(abs(q), abs(r), abs(q+r)) > 5 {
				continue
			}
			if (q == 0 && abs(r) == 5) ||
				(r == 0 && abs(q) == 5) ||
				(q == -r && abs(q) == 5) {
				continue
			}
			point := Point{
				ID: fmt.Sprintf("%c%d", rune('F'+q), r+q+6),
				Q:  q,
				R:  r,
			}
			boardPoints = append(boardPoints, point)
			pointByID[point.ID] = point
			pointByCoord[[2]int{q, r}] = point
		}
	}
}

func BoardPoints() []Point {
	return append([]Point(nil), boardPoints...)
}

func legalMoves(state *State, from string) []MoveOption {
	start, exists := pointByID[from]
	if !exists || state.Rings[from] == "" {
		return nil
	}
	result := make([]MoveOption, 0, 18)
	for _, direction := range directions {
		q := start.Q + direction[0]
		r := start.R + direction[1]
		flips := []string{}
		for {
			point, ok := pointByCoord[[2]int{q, r}]
			if !ok || state.Rings[point.ID] != "" {
				break
			}
			if state.Markers[point.ID] != "" {
				flips = append(flips, point.ID)
			} else {
				result = append(result, MoveOption{
					To:    point.ID,
					Flips: append([]string(nil), flips...),
				})
				if len(flips) > 0 {
					break
				}
			}
			q += direction[0]
			r += direction[1]
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].To < result[j].To
	})
	return result
}

func allLegalMoves(state *State, playerID string) map[string][]MoveOption {
	side := sideForPlayer(state, playerID)
	result := map[string][]MoveOption{}
	if side == "" {
		return result
	}
	rings := ringIDs(state, side)
	for _, from := range rings {
		options := legalMoves(state, from)
		if len(options) > 0 {
			result[from] = options
		}
	}
	return result
}

func hasLegalMove(state *State, playerID string) bool {
	for _, options := range allLegalMoves(state, playerID) {
		if len(options) > 0 {
			return true
		}
	}
	return false
}

func rowsFor(state *State, side Side) []Row {
	rows := []Row{}
	for _, start := range boardPoints {
		if state.Markers[start.ID] != side || state.Rings[start.ID] != "" {
			continue
		}
		for _, direction := range directions[:3] {
			points := make([]string, 0, 5)
			for offset := 0; offset < 5; offset++ {
				point, ok := pointByCoord[[2]int{
					start.Q + offset*direction[0],
					start.R + offset*direction[1],
				}]
				if !ok || state.Markers[point.ID] != side || state.Rings[point.ID] != "" {
					break
				}
				points = append(points, point.ID)
			}
			if len(points) == 5 {
				rows = append(rows, Row{
					ID:     strings.Join(points, "-"),
					Points: points,
					Owner:  side,
				})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].ID < rows[j].ID
	})
	return rows
}

func ringIDs(state *State, side Side) []string {
	result := []string{}
	for pointID, owner := range state.Rings {
		if owner == side {
			result = append(result, pointID)
		}
	}
	sort.Strings(result)
	return result
}

func emptyPointIDs(state *State) []string {
	result := make([]string, 0, len(boardPoints))
	for _, point := range boardPoints {
		if state.Rings[point.ID] == "" && state.Markers[point.ID] == "" {
			result = append(result, point.ID)
		}
	}
	sort.Strings(result)
	return result
}

func markerPool(state *State) int {
	remaining := 51 - len(state.Markers)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func sideForPlayer(state *State, playerID string) Side {
	if player := state.Roster[playerID]; player != nil {
		return player.Side
	}
	return ""
}

func playerForSide(state *State, side Side) string {
	for _, playerID := range state.PlayerOrder {
		if player := state.Roster[playerID]; player != nil && player.Side == side {
			return playerID
		}
	}
	return ""
}

func pointDistance(pointID string) int {
	point, ok := pointByID[pointID]
	if !ok {
		return 99
	}
	return max3(abs(point.Q), abs(point.R), abs(point.Q+point.R))
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func max3(first, second, third int) int {
	if second > first {
		first = second
	}
	if third > first {
		first = third
	}
	return first
}
