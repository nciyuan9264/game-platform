package game

import (
	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/acquire/logger"
	"github.com/nciyuan9264/game-platform/internal/acquire/utils"
)

// RecomputeDerivedState 基于当前 r.State 重新计算公司股价/库存、终局判定、各玩家结算。
//
// 该函数仅做纯计算与 state 同步更新，不做任何 IO（不广播、不写日志）。
// 同时被 BroadcastToRoom（每条命令处理后）和 replay 引擎（回放推进时）使用。
//
// 返回 result（每个玩家的 money/stocks/total）与 gameEnded（本次推进是否使游戏进入终局）。
func RecomputeDerivedState(r *domain.Room) (map[string]interface{}, bool) {
	companyInfoMap := r.State.Companies

	allTileMap := make(map[string]int)
	totalTiles := 0
	for _, tile := range r.State.BoardTiles {
		if tile.Belong != "" {
			totalTiles++
			if tile.Belong != "Blank" {
				allTileMap[tile.Belong] = allTileMap[tile.Belong] + 1
			}
		}
	}

	gameShouldEnd := false
	allCompaniesAbove11 := true
	for company, tileCount := range allTileMap {
		if tileCount >= 41 {
			gameShouldEnd = true
			logger.Info("游戏结束：公司的 tile 数量 >= 41",
				logger.F("room_id", r.ID),
				logger.F("company", company),
				logger.F("tile_count", tileCount))
			break
		}
		if tileCount <= 11 {
			allCompaniesAbove11 = false
		}
	}

	if !gameShouldEnd && allCompaniesAbove11 && totalTiles > 90 {
		gameShouldEnd = true
		logger.Info("游戏结束：每个公司 tile 都 > 11 且 tile 被公司占用总数 > 90",
			logger.F("room_id", r.ID),
			logger.F("total_tiles", totalTiles))
	}

	transitionedToEnd := false
	if gameShouldEnd {
		if r.State.RoomStatus != domain.RoomStatusEnd {
			r.State.RoomStatus = domain.RoomStatusEnd
			transitionedToEnd = true
		}
	}

	allStockMap := make(map[string]int)
	for _, pc := range r.Connections {
		if stockMap, ok := r.State.Players[pc.PlayerID]; ok && stockMap != nil {
			for stockID, stockCount := range stockMap.Stocks {
				allStockMap[stockID] += stockCount
			}
		}
	}

	for companyName, info := range companyInfoMap {
		company, ok := utils.ParseCompanyType(companyName)
		if !ok {
			continue
		}
		stockInfo := utils.GetStockInfo(company, allTileMap[companyName])
		stockLeft := 25 - allStockMap[companyName]
		info.StockTotal = stockLeft
		info.Tiles = allTileMap[companyName]
		info.StockPrice = stockInfo.Price
		companyInfoMap[companyName] = info
	}

	r.State.Companies = companyInfoMap

	result := make(map[string]interface{})
	for _, pc := range r.Connections {
		if playerInfo, ok := r.State.Players[pc.PlayerID]; ok && playerInfo != nil {
			playerStocks := playerInfo.Stocks
			money := playerInfo.Money
			result[pc.PlayerID] = map[string]interface{}{
				"money":  money,
				"stocks": CalculateTotalValue(playerStocks, companyInfoMap),
				"total":  CalculateTotalValue(playerStocks, companyInfoMap) + money,
			}
		}
	}

	return result, transitionedToEnd
}
