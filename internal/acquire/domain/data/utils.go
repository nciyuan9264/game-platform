package data

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"sort"
	"strconv"

	"github.com/nciyuan9264/game-platform/internal/acquire/arrayutil"
	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
)

// GenerateAvailableTiles 生成count个当前房间中未被任何玩家使用的 tile
func GenerateAvailableTiles(room *domain.Room, count int) ([]string, error) {
	// 1. 收集所有玩家手中的 tile
	playerTiles := make(map[string]struct{})

	for _, player := range room.State.Players {
		if player == nil {
			continue
		}
		for _, tile := range player.Tiles {
			playerTiles[tile] = struct{}{}
		}
	}

	// 2. 收集可用 tile
	available := make([]string, 0, len(room.State.BoardTiles))
	for tileID, tile := range room.State.BoardTiles {
		if tile.Belong != "" {
			continue
		}
		if _, used := playerTiles[tileID]; used {
			continue
		}
		available = append(available, tileID)
	}
	sort.Strings(available)

	seed := room.State.RandomSeed
	if seed == 0 {
		seed = deterministicAvailableTileSeed(room, available)
		room.State.RandomSeed = seed
	}
	counter := room.State.RandomCounter
	room.State.RandomCounter++
	rng := rand.New(rand.NewPCG(seed+counter, ^(seed + counter)))
	rng.Shuffle(len(available), func(i, j int) { available[i], available[j] = available[j], available[i] })
	tiles := arrayutil.SafeSlice(available, count)

	return tiles, nil
}

func deterministicAvailableTileSeed(room *domain.Room, available []string) uint64 {
	h := fnv.New64a()
	writeHash := func(format string, args ...interface{}) {
		_, _ = fmt.Fprintf(h, format, args...)
	}
	writeHash("room=%s;current=%s;status=%s;", room.ID, room.State.CurrentPlayer, room.State.RoomStatus)

	for _, tileID := range available {
		writeHash("a=%s;", tileID)
	}

	playerIDs := make([]string, 0, len(room.State.Players))
	for playerID := range room.State.Players {
		playerIDs = append(playerIDs, playerID)
	}
	sort.Strings(playerIDs)
	for _, playerID := range playerIDs {
		player := room.State.Players[playerID]
		if player == nil {
			continue
		}
		writeHash("p=%s,m=%d;", playerID, player.Money)
		tiles := append([]string(nil), player.Tiles...)
		sort.Strings(tiles)
		for _, tileID := range tiles {
			writeHash("pt=%s;", tileID)
		}
		stockNames := make([]string, 0, len(player.Stocks))
		for stockName := range player.Stocks {
			stockNames = append(stockNames, stockName)
		}
		sort.Strings(stockNames)
		for _, stockName := range stockNames {
			writeHash("ps=%s:%d;", stockName, player.Stocks[stockName])
		}
	}

	tileIDs := make([]string, 0, len(room.State.BoardTiles))
	for tileID := range room.State.BoardTiles {
		tileIDs = append(tileIDs, tileID)
	}
	sort.Strings(tileIDs)
	for _, tileID := range tileIDs {
		tile := room.State.BoardTiles[tileID]
		if tile != nil && tile.Belong != "" {
			writeHash("b=%s:%s;", tileID, tile.Belong)
		}
	}

	return h.Sum64()
}

// GetConnectedTiles 用于从 tileKey 开始，递归查找相邻、归属一致的 tile
func GetConnectedTiles(r *domain.Room, startTileKey string) []string {
	visited := make(map[string]bool)
	queue := []string{startTileKey}
	var connected []string

	startTile := r.State.BoardTiles[startTileKey]
	startTileOwner := startTile.Belong

	for len(queue) > 0 {
		tile := queue[0]
		queue = queue[1:]

		if visited[tile] {
			continue
		}
		visited[tile] = true
		connected = append(connected, tile)

		neighbors := GetAdjacentTileKeys(tile)
		for _, neighbor := range neighbors {
			if visited[neighbor] {
				continue
			}
			tile := r.State.BoardTiles[neighbor]
			belong := tile.Belong
			if belong == startTileOwner {
				queue = append(queue, neighbor)
			}
		}
	}

	return connected
}

// getAdjacentTileKeys 用于获取指定 tileKey 的上下左右邻接的 tileKey 列表
func GetAdjacentTileKeys(tileKey string) []string {
	row := tileKey[:len(tileKey)-1] // 例如 "6"
	col := tileKey[len(tileKey)-1:] // 例如 "A"

	// 上下左右邻接逻辑
	rowNum, err := strconv.Atoi(row)
	if err != nil {
		return nil
	}

	var adjacent []string

	// 上 (row-1)
	if rowNum > 1 {
		adjacent = append(adjacent, fmt.Sprintf("%d%s", rowNum-1, col))
	}
	// 下 (row+1)
	if rowNum < 12 {
		adjacent = append(adjacent, fmt.Sprintf("%d%s", rowNum+1, col))
	}
	// 左 (col-1)
	if col[0] > 'A' {
		adjacent = append(adjacent, fmt.Sprintf("%d%s", rowNum, string(col[0]-1)))
	}
	// 右 (col+1)
	if col[0] < 'I' {
		adjacent = append(adjacent, fmt.Sprintf("%d%s", rowNum, string(col[0]+1)))
	}

	return adjacent
}

// 初始化玩家数据
func InitPlayerData(r *domain.Room, playerID string) error {
	// 1. 检查玩家数据是否已存在
	if r.State.Players[playerID] != nil {
		return fmt.Errorf("玩家数据已存在")
	}

	// 2. 随机抽取起始 Tiles
	tiles, err := GenerateAvailableTiles(r, 6)
	if err != nil {
		return fmt.Errorf("生成可用 tiles 失败: %w", err)
	}

	r.State.Players[playerID] = &domain.PlayerState{
		Money: 6000,
		Stocks: map[string]int{
			"Sackson":     0,
			"Tower":       0,
			"American":    0,
			"Festival":    0,
			"Worldwide":   0,
			"Continental": 0,
			"Imperial":    0,
		},
		Tiles: tiles,
	}

	return nil
}
