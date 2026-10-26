package warchest

import (
	"fmt"
	"sort"
)

type cell struct {
	Q int
	R int
}

var directions = []cell{
	{Q: 1, R: 0},
	{Q: 1, R: -1},
	{Q: 0, R: -1},
	{Q: -1, R: 0},
	{Q: -1, R: 1},
	{Q: 0, R: 1},
}

var (
	boardCells     = buildBoardCells()
	boardCellOrder = sortedCellKeys(boardCells)
	locationCells  = []string{
		"-1,-2",
		"2,-3",
		"-2,3",
		"1,2",
		"-2,0",
		"1,-1",
		"3,-2",
		"-3,2",
		"-1,1",
		"2,0",
	}
	startingLocations = map[Side][]string{
		SideOak:    {"-2,3", "1,2"},
		SideFalcon: {"-1,-2", "2,-3"},
	}
	locationSet = stringSet(locationCells)
)

func buildBoardCells() map[string]cell {
	result := make(map[string]cell, 37)
	for q := -3; q <= 3; q++ {
		low := maxInt(-3, -q-3)
		high := minInt(3, -q+3)
		for r := low; r <= high; r++ {
			result[cellKey(q, r)] = cell{Q: q, R: r}
		}
	}
	return result
}

func sortedCellKeys(values map[string]cell) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool {
		left := values[result[i]]
		right := values[result[j]]
		if left.Q != right.Q {
			return left.Q < right.Q
		}
		return left.R < right.R
	})
	return result
}

func cellKey(q, r int) string {
	return fmt.Sprintf("%d,%d", q, r)
}

func adjacent(key string) []string {
	origin, ok := boardCells[key]
	if !ok {
		return nil
	}
	result := make([]string, 0, 6)
	for _, direction := range directions {
		next := cellKey(origin.Q+direction.Q, origin.R+direction.R)
		if _, exists := boardCells[next]; exists {
			result = append(result, next)
		}
	}
	return result
}

func distance(first, second string) int {
	left, leftOK := boardCells[first]
	right, rightOK := boardCells[second]
	if !leftOK || !rightOK {
		return 1 << 30
	}
	return (absInt(left.Q-right.Q) +
		absInt(left.R-right.R) +
		absInt(left.Q+left.R-right.Q-right.R)) / 2
}

func emptyAt(state *State, key string) bool {
	if _, ok := boardCells[key]; !ok {
		return false
	}
	return state.Board[key] == nil
}

func shortRoutes(state *State, from string, limit int) map[string][]string {
	result := map[string][]string{}
	for _, via := range adjacent(from) {
		if !emptyAt(state, via) {
			continue
		}
		result[via] = []string{via}
		if limit <= 1 {
			continue
		}
		for _, destination := range adjacent(via) {
			if destination == from || !emptyAt(state, destination) {
				continue
			}
			if _, exists := result[destination]; !exists {
				result[destination] = []string{via, destination}
			}
		}
	}
	return result
}

func sortedRouteDestinations(routes map[string][]string) []string {
	result := make([]string, 0, len(routes))
	for destination := range routes {
		result = append(result, destination)
	}
	sort.Strings(result)
	return result
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func minInt(first, second int) int {
	if first < second {
		return first
	}
	return second
}

func maxInt(first, second int) int {
	if first > second {
		return first
	}
	return second
}
