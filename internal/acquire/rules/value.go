package game

import (
	"github.com/nciyuan9264/game-platform-server/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform-server/internal/acquire/logger"
)

func CalculateTotalValue(playerStocks map[string]int, companies map[string]*domain.CompanyState) int {
	total := 0
	for company, count := range playerStocks {
		info, ok := companies[company]
		if !ok || info == nil {
			logger.Error("无法找到公司信息", logger.F("company", company))
			continue
		}
		total += count * info.StockPrice
	}
	return total
}
