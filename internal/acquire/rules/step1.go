package game

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/nciyuan9264/game-platform-server/internal/acquire/arrayutil"
	"github.com/nciyuan9264/game-platform-server/internal/acquire/domain/data"
	"github.com/nciyuan9264/game-platform-server/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform-server/internal/acquire/logger"
	"github.com/nciyuan9264/game-platform-server/internal/acquire/utils"
)

// PlaceTile 用于处理将棋子放置到棋盘上：修改 tile 的 belong 字段并更新 Redis，同时从玩家手牌中移除该 tile。
func placeTile(r *domain.Room, playerID, tileKey string) error {
	// Step 1：下棋
	r.State.BoardTiles[tileKey].Belong = "Blank"
	// Step 2：从玩家 tile 列表中移除该 tile
	r.State.Players[playerID].Tiles = arrayutil.SafeSliceRemove(r.State.Players[playerID].Tiles, tileKey)
	// Step 3: 设置LastTileKey
	r.State.LastTileKey = tileKey
	logger.Info("玩家放置棋子成功", logger.F("room_id", r.ID), logger.F("player_id", playerID), logger.F("tile_key", tileKey))
	return nil
}

func handleMergeProcess(
	r *domain.Room,
	mainHotel string,
	otherHotel []string,
	hotelTileCount map[string]int,
) error {
	tempSettleData := make(map[string]domain.SettleData)
	for _, hotel := range otherHotel {
		// Step 1：获取被并购酒店的 tile 数量
		tileCount, ok := hotelTileCount[hotel]
		if !ok {
			return fmt.Errorf("❌ 未找到酒店[%s]的 tile 数量", otherHotel)
		}
		// Step 2：遍历房间玩家，获取每人该公司股票数量
		type holder struct {
			PlayerID string
			Count    int
		}
		var holders []holder

		for _, pc := range r.Connections {
			playerID := pc.PlayerID
			// 获取该玩家对该并购酒店的股票数
			count := r.State.Players[playerID].Stocks[hotel] // 直接按 hotel 名称取股票数
			if count > 0 {
				holders = append(holders, holder{
					PlayerID: playerID,
					Count:    count,
				})
			}
		}
		// Step 3：根据持股数量排序
		sort.Slice(holders, func(i, j int) bool {
			return holders[i].Count > holders[j].Count
		})

		// 保存拥有当前公司股票的所有玩家
		currentCompanyHoders := make([]string, 0)
		for _, holder := range holders {
			currentCompanyHoders = append(currentCompanyHoders, holder.PlayerID)
		}

		company, ok := utils.ParseCompanyType(hotel)
		if !ok {
			return nil // 或报错
		}

		stockInfo := utils.GetStockInfo(company, tileCount)
		firstBonus := stockInfo.BonusFirst
		secondBonus := stockInfo.BonusSecond

		dividends := make(map[string]int)

		// Step 4：分红逻辑
		firstGroup := []string{holders[0].PlayerID}
		firstCount := holders[0].Count

		// 找出第一名的并列玩家
		for i := 1; i < len(holders); i++ {
			if holders[i].Count == firstCount {
				firstGroup = append(firstGroup, holders[i].PlayerID)
			} else {
				break
			}
		}

		if len(firstGroup) > 1 {
			// 并列第一名：平分总红利（first + second）
			totalBonus := firstBonus + secondBonus
			each := totalBonus / len(firstGroup)
			for _, pid := range firstGroup {
				dividends[pid] = each
			}
		} else {
			// 第一名独占
			dividends[firstGroup[0]] = firstBonus

			// 找第二名（可能并列）
			secondGroup := []string{}
			secondCount := -1
			for i := 1; i < len(holders); i++ {
				if holders[i].Count < firstCount {
					if secondCount == -1 {
						secondCount = holders[i].Count
					}
					if holders[i].Count == secondCount {
						secondGroup = append(secondGroup, holders[i].PlayerID)
					} else {
						break
					}
				}
			}

			if len(secondGroup) > 0 {
				each := secondBonus / len(secondGroup)
				for _, pid := range secondGroup {
					dividends[pid] = each
				}
			}
		}

		for playerID, money := range dividends {
			r.State.Players[playerID].Money += money
		}
		tempSettleData[hotel] = domain.SettleData{
			Hoders:    currentCompanyHoders,
			Dividends: dividends,
		}
	}

	r.State.MergeMainCompany = mainHotel
	r.State.MergeSettleData = tempSettleData
	r.State.RoomStatus = domain.RoomStatusMergingSettle
	logger.Info("完成酒店并入红利计算和状态更新", logger.F("room_id", r.ID), logger.F("other_hotel", otherHotel), logger.F("main_hotel", mainHotel))
	return nil
}

func HandlePostTilePlacement(r *domain.Room, playerID string) error {
	// 第一步：获取公司信息
	companyInfo := r.State.Companies

	// 第二步：检查是否有任何公司可购买股票
	for _, info := range companyInfo {
		if tilesCount := info.Tiles; tilesCount > 0 {
			// 有公司可买，设置房间状态为“买股票”
			r.State.RoomStatus = domain.RoomStatusBuyStock
			return nil
		}
	}
	// 发一张 tile
	if err := GiveRandomTileToPlayer(r, playerID); err != nil {
		return fmt.Errorf("发牌失败: %w", err)
	}

	// 切换玩家
	if err := SwitchToNextPlayer(r, playerID); err != nil {
		logger.Error("切换玩家失败", logger.F("room_id", r.ID), logger.F("player_id", playerID), logger.F("error", err))
	}
	return nil
}

func handleMergingLogic(r *domain.Room, hotelSet map[string]struct{}) error {
	// 统计每个酒店的 tile 数量
	hotelTileCount := make(map[string]int)
	maxCount := 0
	for hotel := range hotelSet {
		info := r.State.Companies[hotel]
		tileCount := info.Tiles
		hotelTileCount[hotel] = tileCount
		if tileCount > maxCount {
			maxCount = tileCount
		}
	}
	// 找出最大 tile 数量的酒店
	var topHotels []string
	for hotel, count := range hotelTileCount {
		if count == maxCount {
			topHotels = append(topHotels, hotel)
		}
	}

	if len(topHotels) > 1 {
		for _, hotel := range topHotels {
			delete(hotelSet, hotel)
		}
		otherHotel := make([]string, 0, len(hotelSet))
		for key := range hotelSet {
			if r.State.Companies[key].Tiles >= 11 {
				continue
			}
			otherHotel = append(otherHotel, key)
		}
		if len(otherHotel) == 0 && maxCount >= 11 {
			r.State.RoomStatus = domain.RoomStatusBuyStock
			log.Println("没有其他可以合并的公司")
			return nil
		}
		r.State.MergingSelection = domain.MergingSelection{
			MainCompany:  topHotels,
			OtherCompany: otherHotel,
		}
		r.State.RoomStatus = domain.RoomStatusMergingSelection
		return nil
	} else {
		// 只有一个最大的酒店
		mainHotel := topHotels[0]
		delete(hotelSet, mainHotel)
		var otherHotel []string
		for key := range hotelSet {
			if r.State.Companies[key].Tiles >= 11 {
				continue
			}
			otherHotel = append(otherHotel, key)
		}
		if len(otherHotel) == 0 {
			r.State.RoomStatus = domain.RoomStatusBuyStock
			log.Println("没有其他可以合并的公司")
			return nil
		}
		err := handleMergeProcess(r, mainHotel, otherHotel, hotelTileCount)
		if err != nil {
			return err
		}
	}
	return nil
}

// 检查是否有创建、并购、扩建规则触发
func checkTileTriggerRules(r *domain.Room, playerID string, tileKey string) error {
	adjTiles := data.GetAdjacentTileKeys(tileKey)
	companySet := make(map[string]struct{})
	blankTileCount := 0

	for _, adjKey := range adjTiles {
		tile := r.State.BoardTiles[adjKey]
		switch tile.Belong {
		case "Blank":
			blankTileCount++
		case "": // 未被占用
			continue
		default:
			companySet[tile.Belong] = struct{}{}
		}
	}

	if len(companySet) >= 2 {
		logger.Warn("触发并购规则", logger.F("companies", companySet))
		err := handleMergingLogic(r, companySet)
		if err != nil {
			return err
		}
		return nil
	}

	if len(companySet) == 1 {
		logger.Warn("触发扩建公司规则", logger.F("company_set", companySet))
		var hotelList []string
		for key := range companySet {
			hotelList = append(hotelList, key)
		}
		company := hotelList[0]

		connectedTiles := data.GetConnectedTiles(r, tileKey)
		for _, tileKeyBlank := range connectedTiles {
			r.State.BoardTiles[tileKeyBlank] = &domain.Tile{ID: tileKeyBlank, Belong: company}
		}

		// 统计公司 tiles 数量
		connectedTiles = data.GetConnectedTiles(r, tileKey)
		r.State.Companies[company].Tiles = len(connectedTiles)
		logger.Info("公司数据已更新", logger.F("room_id", r.ID), logger.F("company", r.State.Companies[company]))

		err := HandlePostTilePlacement(r, playerID)
		if err != nil {
			logger.Warn("处理玩家放置 tile 后逻辑失败", logger.F("error", err))
		}
		return nil
	}

	if blankTileCount >= 1 {
		companyInfo := r.State.Companies
		flag := false
		for _, info := range companyInfo {
			if info.Tiles == 0 {
				flag = true
				break
			}
		}
		if !flag {
			r.State.RoomStatus = domain.RoomStatusBuyStock
			logger.Warn("没有可以创建的公司")
			return nil
		}

		logger.Warn("触发创建公司规则")
		// Step 1: 修改房间状态为“创建公司状态”
		r.State.RoomStatus = domain.RoomStatusCreateCompany
		return nil
	}

	err := HandlePostTilePlacement(r, playerID)
	if err != nil {
		logger.Warn("处理玩家放置 tile 后逻辑失败", logger.F("error", err))
	}
	return nil
}

type PlaceTilePayload struct {
	TileKey string `json:"tileKey"`
}

// 处理玩家放置 tile 消息
func HandlePlaceTileMessage(r *domain.Room, cmd domain.Command) {
	var p PlaceTilePayload
	if err := json.Unmarshal(cmd.Payload, &p); err != nil {
		logger.Warn("无效的 payload", logger.F("error", err))
		return
	}
	tileKey := p.TileKey

	currentPlayer := r.State.CurrentPlayer
	if currentPlayer != cmd.PlayerID {
		logger.Warn("不是当前玩家的回合", logger.F("player_id", cmd.PlayerID), logger.F("current_player", currentPlayer))
		return
	}
	if r.State.RoomStatus != domain.RoomStatusSetTile {
		logger.Warn("不是放置 tile 的状态", logger.F("status", r.State.RoomStatus))
		return
	}

	// Step1: 放置棋子
	err := placeTile(r, cmd.PlayerID, tileKey)
	if err != nil {
		logger.Error("放置棋子失败", logger.F("room_id", r.ID), logger.F("player_id", cmd.PlayerID), logger.F("tile_key", tileKey), logger.F("error", err))
		return
	}
	// Step2: 检查 创建公司/并购公司
	err = checkTileTriggerRules(r, cmd.PlayerID, tileKey)
	if err != nil {
		logger.Error("检查棋子规则失败", logger.F("room_id", r.ID), logger.F("player_id", cmd.PlayerID), logger.F("error", err))
		return
	}
}

type MergingSelectionPayload struct {
	MainCompany string `json:"mainCompany"`
}

func HandleMergingSelectionMessage(r *domain.Room, cmd domain.Command) {
	var p MergingSelectionPayload
	if err := json.Unmarshal(cmd.Payload, &p); err != nil {
		logger.Warn("无效的 payload", logger.F("error", err))
		return
	}
	maincompany := p.MainCompany

	currentPlayer := r.State.CurrentPlayer
	if currentPlayer != cmd.PlayerID {
		logger.Warn("不是当前玩家的回合", logger.F("player_id", cmd.PlayerID), logger.F("current_player", currentPlayer))
		return
	}

	if r.State.RoomStatus != domain.RoomStatusMergingSelection {
		logger.Warn("不是 merging_selection 的状态")
		return
	}

	mergeSelectionTemp := r.State.MergingSelection
	for _, company := range mergeSelectionTemp.MainCompany {
		if company == maincompany {
			continue
		}
		mergeSelectionTemp.OtherCompany = append(mergeSelectionTemp.OtherCompany, company)
	}

	hotelTileCount := make(map[string]int)
	maxCount := 0
	for i := len(mergeSelectionTemp.OtherCompany) - 1; i >= 0; i-- {
		hotel := mergeSelectionTemp.OtherCompany[i]
		info := r.State.Companies[hotel]
		if info.Tiles >= 11 {
			mergeSelectionTemp.OtherCompany = arrayutil.RemoveAtIndex(mergeSelectionTemp.OtherCompany, i)
			continue
		}
		tileCount := info.Tiles
		hotelTileCount[hotel] = tileCount
		if tileCount > maxCount {
			maxCount = tileCount
		}
	}

	err := handleMergeProcess(r, maincompany, mergeSelectionTemp.OtherCompany, hotelTileCount)
	if err != nil {
		logger.Error("处理合并过程失败", logger.F("room_id", r.ID), logger.F("player_id", cmd.PlayerID), logger.F("error", err))
		return
	}
}

type MergingSettlePayload struct {
	Actions []domain.MergingSettleItem `json:"actions"`
}

func HandleMergingSettleMessage(r *domain.Room, cmd domain.Command) {
	var p MergingSettlePayload
	if err := json.Unmarshal(cmd.Payload, &p); err != nil {
		logger.Error("无效的 payload", logger.F("room_id", r.ID), logger.F("player_id", cmd.PlayerID), logger.F("error", err))
		return
	}

	settleActions := p.Actions
	if r.State.RoomStatus != domain.RoomStatusMergingSettle {
		logger.Error("不是合并的状态", logger.F("room_id", r.ID), logger.F("player_id", cmd.PlayerID))
		return
	}

	playerInHoder := false
	for _, data := range r.State.MergeSettleData {
		oldHoders := data.Hoders
		for _, h := range oldHoders {
			if h == cmd.PlayerID {
				playerInHoder = true
			}
		}
	}
	if !playerInHoder {
		logger.Warn("玩家不在任何合并中", logger.F("player_id", cmd.PlayerID))
		return
	}

	mergeMainCompany := r.State.MergeMainCompany

	for _, item := range settleActions {
		companyData, ok := r.State.Companies[item.Company]
		if !ok {
			logger.Warn("找不到公司信息", logger.F("company", item.Company))
			continue
		}

		sellAmount := int(item.SellAmount)
		exchangeAmount := int(item.ExchangeAmount)

		if sellAmount > 0 {
			r.State.Players[cmd.PlayerID].Stocks[item.Company] -= sellAmount
			money := sellAmount * companyData.StockPrice
			r.State.Players[cmd.PlayerID].Money += money
		}

		if exchangeAmount > 0 {
			// 修改股票持仓
			r.State.Players[cmd.PlayerID].Stocks[mergeMainCompany] += exchangeAmount / 2
			r.State.Players[cmd.PlayerID].Stocks[item.Company] -= exchangeAmount
		}
	}

	allHodersCleared := true
	// 移除 Hoders 中的 playerID
	for key, data := range r.State.MergeSettleData {
		oldHoders := data.Hoders
		newHoders := make([]string, 0)
		for _, h := range oldHoders {
			if h != cmd.PlayerID {
				newHoders = append(newHoders, h)
			}
		}
		data.Hoders = newHoders
		r.State.MergeSettleData[key] = data
		// 如果还有剩余 hoders，就不是全部清空
		if len(newHoders) > 0 {
			allHodersCleared = false
		}
	}

	if allHodersCleared {
		lastTile := r.State.LastTileKey

		connTile := data.GetConnectedTiles(r, lastTile)
		connTileSet := make(map[string]struct{})
		for _, id := range connTile {
			connTileSet[id] = struct{}{}
		}

		tileMap := r.State.BoardTiles

		for key, tile := range tileMap {
			if _, ok := r.State.MergeSettleData[tile.Belong]; ok {
				tile.Belong = mergeMainCompany
				tileMap[key] = tile
			}
			if tile.ID == lastTile {
				tile.Belong = mergeMainCompany
				tileMap[key] = tile
			}
			if _, ok := connTileSet[tile.ID]; ok {
				tile.Belong = mergeMainCompany
				tileMap[key] = tile
			}
		}

		r.State.BoardTiles = tileMap
		adj := data.GetAdjacentTileKeys(lastTile)
		for _, key := range adj {
			tile := tileMap[key]
			if tile.Belong == "Blank" {
				tile.Belong = mergeMainCompany
				tileMap[key] = tile
			}
		}

		r.State.RoomStatus = domain.RoomStatusBuyStock
		r.State.MergeSettleData = map[string]domain.SettleData{}
	}
}

type CreateCompanyPayload struct {
	Company string `json:"company"`
}

func HandleCreateCompanyMessage(r *domain.Room, cmd domain.Command) {
	var p CreateCompanyPayload
	if err := json.Unmarshal(cmd.Payload, &p); err != nil {
		logger.Warn("无效的 payload", logger.F("error", err))
		return
	}
	company := p.Company
	logger.Info("收到 create_company 消息", logger.F("company", company))

	if r.State.CurrentPlayer != cmd.PlayerID {
		logger.Warn("不是当前玩家的回合", logger.F("room_id", r.ID), logger.F("player_id", cmd.PlayerID), logger.F("current_player", r.State.CurrentPlayer))
		return
	}

	if r.State.RoomStatus != domain.RoomStatusCreateCompany {
		logger.Warn("不是创建公司的状态", logger.F("room_id", r.ID))
		return
	}

	logger.Info("创建公司使用的 tileKey", logger.F("tile_key", r.State.LastTileKey))
	// 统计公司 tiles 数量
	connectedTiles := data.GetConnectedTiles(r, r.State.LastTileKey)
	r.State.Companies[company].Tiles = len(connectedTiles)
	r.State.Companies[company].StockTotal--
	logger.Info("公司数据已更新", logger.F("room_id", r.ID), logger.F("company", company))

	for _, tileKey := range connectedTiles {
		tile, ok := r.State.BoardTiles[tileKey]
		if !ok {
			logger.Warn("tileKey 不存在，跳过", logger.F("room_id", r.ID), logger.F("tile_key", tileKey))
			continue
		}

		// 修改归属
		tile.Belong = company

		// 写回 Redis
		r.State.BoardTiles[tileKey] = tile
	}
	// Step 3: 增加玩家的股票数据
	r.State.Players[cmd.PlayerID].Stocks[company]++
	logger.Info("玩家获得股票", logger.F("room_id", r.ID), logger.F("player_id", cmd.PlayerID), logger.F("company", company), logger.F("count", 1))

	r.State.RoomStatus = domain.RoomStatusBuyStock
}
