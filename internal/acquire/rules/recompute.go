package game

import (
	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/acquire/utils"
)

// RecomputeDerivedState 基于当前 r.State 重新计算公司股价/库存、终局资格、各玩家结算。
//
// 该函数仅做纯计算与 state 同步更新，不做任何 IO（不广播、不写日志）。
// 同时被 BroadcastToRoom（每条命令处理后）和 replay 引擎（回放推进时）使用。
//
// 返回 result（每个玩家的 money/stocks/total）与 eligibilityChanged（本次是否首次获得终局资格）。
func RecomputeDerivedState(r *domain.Room) (map[string]interface{}, bool) {
	companyInfoMap := r.State.Companies

	allTileMap := make(map[string]int)
	for _, tile := range r.State.BoardTiles {
		if tile != nil && tile.Belong != "" {
			if tile.Belong != "Blank" {
				allTileMap[tile.Belong] = allTileMap[tile.Belong] + 1
			}
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
		if info == nil {
			continue
		}
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
	wasEligible := r.State.EndGame.Eligible
	RefreshEndGameEligibility(r, allTileMap)

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

	return result, !wasEligible && r.State.EndGame.Eligible
}
