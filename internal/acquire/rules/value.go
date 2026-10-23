package game

import (
	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/acquire/logger"
)

func CalculateTotalValue(playerStocks map[string]int, companies map[string]*domain.CompanyState, commandLogger *logger.Logger) int {
	if commandLogger == nil {
		commandLogger = logger.DefaultLogger
	}
	total := 0
	for company, count := range playerStocks {
		info, ok := companies[company]
		if !ok || info == nil {
			commandLogger.Error("无法找到公司信息", logger.F("company", company))
			continue
		}
		total += count * info.StockPrice
	}
	return total
}
