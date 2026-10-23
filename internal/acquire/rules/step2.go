package game

import (
	"encoding/json"
	"fmt"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/acquire/logger"
)

// UpdateCompanyStockAndTiles 更新公司数据（stockTotal 减少）
func UpdateCompanyStockAndTiles(r *domain.Room, company string) error {

	// 更新 stockTotal（每次只减1）
	if r.State.Companies[company].StockTotal <= 0 {
		return fmt.Errorf("公司股票已售罄")
	}
	r.State.Companies[company].StockTotal--
	r.Log().Info("公司已更新", logger.F("room_id", r.ID), logger.F("company", company), logger.F("stockTotal", r.State.Companies[company].StockTotal))
	return nil
}

// UpdatePlayerStockAndMoney 更新玩家数据
func UpdatePlayerStockAndMoney(
	r *domain.Room,
	playerID string,
	company string,
	stockCount int,
	totalPrice int,
) error {

	player, ok := r.State.Players[playerID]
	if !ok {
		return fmt.Errorf("玩家不存在")
	}

	if player.Money < totalPrice {
		return fmt.Errorf("余额不足，购买失败")
	}

	// 扣钱（原地修改）
	player.Money -= totalPrice

	// 确保 Stocks 已初始化
	if player.Stocks == nil {
		player.Stocks = make(map[string]int)
	}

	// 原地修改股票数量
	player.Stocks[company] += stockCount

	return nil
}

type BuyStockPayload struct {
	Stocks map[string]int `json:"stocks"`
}

func HandleBuyStockMessage(r *domain.Room, cmd domain.Command) {
	var p BuyStockPayload
	if err := json.Unmarshal(cmd.Payload, &p); err != nil {
		r.Log().Warn("无效的 buy_stock payload", logger.F("error", err))
		return
	}

	stocks := p.Stocks
	currentPlayer := r.State.CurrentPlayer
	if currentPlayer != cmd.PlayerID {
		r.Log().Warn("不是当前玩家的回合", logger.F("player_id", cmd.PlayerID), logger.F("current_player", currentPlayer))
		return
	}

	roomInfo := r.State.RoomStatus
	if roomInfo != domain.RoomStatusBuyStock {
		r.Log().Warn("不是 buyStock 的状态", logger.F("status", roomInfo))
		return
	}

	totalPrice := 0
	priceMap := make(map[string]int)

	for company, countVal := range stocks {
		count := countVal

		// 获取股价
		price := r.State.Companies[company].StockPrice
		priceMap[company] = price
		totalPrice += price * count
	}

	// 遍历更新每个公司
	for company, countVal := range stocks {
		count := countVal
		for i := 0; i < count; i++ {
			if err := UpdateCompanyStockAndTiles(r, company); err != nil {
				r.Log().Error("更新公司失败", logger.F("room_id", r.ID), logger.F("player_id", cmd.PlayerID), logger.F("company", company), logger.F("error", err))
				return
			}
		}
	}

	// 再统一扣钱 & 更新玩家股票
	for company, countVal := range stocks {
		count := countVal
		if err := UpdatePlayerStockAndMoney(r, cmd.PlayerID, company, count, priceMap[company]*count); err != nil {
			r.Log().Error("更新玩家失败", logger.F("room_id", r.ID), logger.F("player_id", cmd.PlayerID), logger.F("company", company), logger.F("error", err))
			return
		}
	}

	err := GiveRandomTileToPlayer(r, cmd.PlayerID)
	if err != nil {
		r.Log().Warn("发牌失败", logger.F("room_id", r.ID), logger.F("player_id", cmd.PlayerID), logger.F("error", err))
	}
	// 切换玩家
	if err := SwitchToNextPlayer(r, cmd.PlayerID); err != nil {
		r.Log().Error("切换玩家失败", logger.F("room_id", r.ID), logger.F("player_id", cmd.PlayerID), logger.F("error", err))
	}
	// 最后设置房间状态为 setTile
	r.State.RoomStatus = domain.RoomStatusSetTile

	r.Log().Info("玩家购买股票成功", logger.F("room_id", r.ID), logger.F("player_id", cmd.PlayerID), logger.F("total_price", totalPrice))
}
