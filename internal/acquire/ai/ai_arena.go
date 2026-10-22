package roompkg

import (
	"fmt"

	"github.com/nciyuan9264/game-platform-server/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform-server/internal/acquire/roomcore"
)

const (
	arenaCandidatePlayerID = "ai_candidate"
	arenaBaselinePlayerID  = "ai_baseline_1"
)

type ArenaConfig struct {
	Games       int
	Players     int
	Seed        uint64
	MaxTurns    int
	SearchDepth int
	BeamWidth   int
}

type ArenaResult struct {
	Games             int     `json:"games"`
	Players           int     `json:"players"`
	CandidateWins     int     `json:"candidateWins"`
	BaselineWins      int     `json:"baselineWins"`
	Draws             int     `json:"draws"`
	AverageValueDelta float64 `json:"averageValueDelta"`
	AverageTurns      float64 `json:"averageTurns"`
}

func RunAIArena(config ArenaConfig, candidate AIWeights, baseline AIWeights) ArenaResult {
	if config.Games <= 0 {
		config.Games = 1
	}
	config.Players = normalizeArenaPlayers(config.Players)
	if config.MaxTurns <= 0 {
		config.MaxTurns = 120
	}
	if config.SearchDepth <= 0 {
		config.SearchDepth = 1
	}
	if config.BeamWidth <= 0 {
		config.BeamWidth = 6
	}

	result := ArenaResult{Games: config.Games, Players: config.Players}
	totalDelta := 0
	totalTurns := 0
	for i := 0; i < config.Games; i++ {
		gameSeed := config.Seed + uint64(i)
		room := newArenaRoom(gameSeed, config.Players)
		turns := runArenaGame(room, config, candidate, baseline)
		candidateValue := estimatePlayerTotalValue(room, arenaCandidatePlayerID)
		baselineValue := bestArenaBaselineValue(room)
		switch {
		case candidateValue > baselineValue:
			result.CandidateWins++
		case baselineValue > candidateValue:
			result.BaselineWins++
		default:
			result.Draws++
		}
		totalDelta += candidateValue - baselineValue
		totalTurns += turns
	}
	result.AverageValueDelta = float64(totalDelta) / float64(config.Games)
	result.AverageTurns = float64(totalTurns) / float64(config.Games)
	return result
}

func normalizeArenaPlayers(players int) int {
	switch {
	case players <= 0:
		return 2
	case players < 2:
		return 2
	case players > 6:
		return 6
	default:
		return players
	}
}

func arenaBaselinePlayerIDFor(index int) string {
	if index <= 1 {
		return arenaBaselinePlayerID
	}
	return fmt.Sprintf("ai_baseline_%d", index)
}

func bestArenaBaselineValue(r *domain.Room) int {
	best := 0
	for playerID := range r.State.Players {
		if playerID == arenaCandidatePlayerID {
			continue
		}
		value := estimatePlayerTotalValue(r, playerID)
		if value > best {
			best = value
		}
	}
	return best
}

func runArenaGame(r *domain.Room, config ArenaConfig, candidate AIWeights, baseline AIWeights) int {
	for turn := 0; turn < config.MaxTurns; turn++ {
		if r.State.RoomStatus == domain.RoomStatusEnd {
			return turn
		}
		playerID := arenaActionPlayerID(r)
		if playerID == "" {
			return turn + 1
		}
		weights := baseline
		if playerID == arenaCandidatePlayerID {
			weights = candidate
		}
		action, ok := chooseBestActionBySearch(r, playerID, r.State.RoomStatus, nil, AISearchConfig{
			Depth:       config.SearchDepth,
			BeamWidth:   config.BeamWidth,
			ActionLimit: 24,
			Weights:     weights,
		})
		if !ok {
			cmdType, payload, ok := buildAIActionMsgByHeuristic(r, playerID, r.State.RoomStatus, nil)
			if !ok {
				return turn + 1
			}
			action, ok = actionFromCommand(cmdType, payload)
			if !ok {
				return turn + 1
			}
		}
		next, ok := simulateAction(r, playerID, action)
		if !ok {
			return turn + 1
		}
		r.State = next.State
	}
	return config.MaxTurns
}

func arenaActionPlayerID(r *domain.Room) string {
	return nextActionPlayerID(r)
}

func newArenaRoom(seed uint64, playersOpt ...int) *domain.Room {
	players := 2
	if len(playersOpt) > 0 {
		players = normalizeArenaPlayers(playersOpt[0])
	}
	base := roomcore.NewBase(fmt.Sprintf("arena-%d", seed), 128)
	playerIDs := make([]string, 0, players)
	playerIDs = append(playerIDs, arenaCandidatePlayerID)
	for i := 1; i < players; i++ {
		playerIDs = append(playerIDs, arenaBaselinePlayerIDFor(i))
	}
	base.PlayerSeq = append([]string(nil), playerIDs...)
	for _, playerID := range playerIDs {
		base.Connections[playerID] = &roomcore.PlayerConn{PlayerID: playerID, Ready: true, Online: true, AI: true}
	}

	board := make(map[string]*domain.Tile, 108)
	allTiles := make([]string, 0, 108)
	for row := 1; row <= 12; row++ {
		for col := 'A'; col <= 'I'; col++ {
			id := fmt.Sprintf("%d%c", row, col)
			board[id] = &domain.Tile{ID: id, Belong: ""}
			allTiles = append(allTiles, id)
		}
	}
	offset := int(seed % uint64(len(allTiles)))
	draw := func(n int) []string {
		tiles := make([]string, 0, n)
		for len(tiles) < n {
			tiles = append(tiles, allTiles[offset%len(allTiles)])
			offset++
		}
		return tiles
	}

	playerStates := make(map[string]*domain.PlayerState, players)
	for _, playerID := range playerIDs {
		playerStates[playerID] = newArenaPlayer(draw(5))
	}

	return &domain.Room{
		Base: base,
		State: &domain.GameState{
			CurrentPlayer: arenaCandidatePlayerID,
			RoomStatus:    domain.RoomStatusSetTile,
			OwnerID:       arenaCandidatePlayerID,
			MaxPlayers:    players,
			RandomSeed:    seed,
			BoardTiles:    board,
			Players:       playerStates,
			Companies:     newArenaCompanies(),
		},
	}
}

func newArenaPlayer(tiles []string) *domain.PlayerState {
	return &domain.PlayerState{
		Money: 6000,
		Tiles: append([]string(nil), tiles...),
		Stocks: map[string]int{
			"Tower":       0,
			"Sackson":     0,
			"American":    0,
			"Festival":    0,
			"Imperial":    0,
			"Worldwide":   0,
			"Continental": 0,
		},
	}
}

func newArenaCompanies() map[string]*domain.CompanyState {
	names := []string{"Tower", "Sackson", "American", "Festival", "Imperial", "Worldwide", "Continental"}
	companies := make(map[string]*domain.CompanyState, len(names))
	for _, name := range names {
		companies[name] = &domain.CompanyState{Name: name, Tiles: 0, StockPrice: 0, StockTotal: 25}
	}
	return companies
}
