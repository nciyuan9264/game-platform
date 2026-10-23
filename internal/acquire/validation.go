package acquire

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/acquire/roomcore"
	rules "github.com/nciyuan9264/game-platform/internal/acquire/rules"
	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func validateAcquireCommand(room *domain.Room, command contracts.RoomCommand, payload []byte) error {
	state := room.State
	if command.Type == "connect" {
		switch {
		case command.PlayerID == "":
			return errors.New("playerID is required")
		case room.Connections[command.PlayerID] != nil:
			return nil
		case state.RoomStatus != domain.RoomStatusMatch:
			return errors.New("游戏已开始，无法加入")
		case len(room.Connections) >= state.MaxPlayers:
			return errors.New("房间已满")
		default:
			return nil
		}
	}

	player := room.Connections[command.PlayerID]
	if player == nil {
		return errors.New("玩家不在房间中")
	}
	if command.Type == "disconnect" {
		return nil
	}
	if command.Type == "turn_timeout" {
		if command.ConnectionRef.Valid() {
			return errors.New("客户端不能提交超时命令")
		}
		return nil
	}

	switch command.Type {
	case "room_update_settings", "match_begin", "match_add_ai", "match_remove_player":
		if command.PlayerID != state.OwnerID {
			return errors.New("只有房主可以操作")
		}
		if state.RoomStatus != domain.RoomStatusMatch {
			return errors.New("只能在准备阶段操作")
		}
	case "match_ready":
		if state.RoomStatus != domain.RoomStatusMatch {
			return errors.New("准备阶段已结束")
		}
	case "game_restart_game":
		if command.PlayerID != state.OwnerID {
			return errors.New("只有房主可以重新开始游戏")
		}
		if state.RoomStatus != domain.RoomStatusEnd {
			return errors.New("本局尚未结束")
		}
	case "game_end_vote":
		if state.RoomStatus == domain.RoomStatusMatch ||
			state.RoomStatus == domain.RoomStatusWaiting ||
			state.RoomStatus == domain.RoomStatusEnd {
			return errors.New("当前不能确认终局")
		}
		if !state.EndGame.Eligible {
			return errors.New("尚未达到终局条件")
		}
	}

	switch command.Type {
	case "room_update_settings":
		var request struct {
			Visibility string `json:"visibility"`
		}
		if json.Unmarshal(payload, &request) != nil ||
			(request.Visibility != contracts.VisibilityPublic && request.Visibility != contracts.VisibilityPrivate) {
			return errors.New("请选择公开或私密")
		}
	case "match_ready":
		var request struct {
			Ready *bool `json:"ready"`
		}
		if json.Unmarshal(payload, &request) != nil || request.Ready == nil {
			return errors.New("准备状态无效")
		}
		if command.PlayerID == state.OwnerID && !*request.Ready {
			return errors.New("房主自动准备")
		}
	case "match_begin":
		if len(room.Connections) < 2 {
			return errors.New("至少需要两名玩家")
		}
		for _, connection := range room.Connections {
			if connection == nil || !connection.Ready || !connection.Online {
				return errors.New("请等待所有玩家准备")
			}
		}
	case "match_add_ai":
		var request struct {
			SeatIndex *int `json:"seatIndex"`
		}
		if json.Unmarshal(payload, &request) != nil {
			return errors.New("席位无效")
		}
		if len(room.Connections) >= state.MaxPlayers {
			return errors.New("房间已满")
		}
		if request.SeatIndex != nil {
			if *request.SeatIndex < 0 || *request.SeatIndex >= state.MaxPlayers {
				return errors.New("席位无效")
			}
			if acquireSeatOccupied(room, *request.SeatIndex) {
				return errors.New("席位已被占用")
			}
		}
	case "match_remove_player":
		var request struct {
			PlayerID string `json:"playerID"`
		}
		if json.Unmarshal(payload, &request) != nil || room.Connections[request.PlayerID] == nil {
			return errors.New("玩家已离开")
		}
		if request.PlayerID == state.OwnerID || request.PlayerID == command.PlayerID {
			return errors.New("不能移除房主或自己")
		}
	case "game_end_vote":
		if _, ok := rules.ParseEndVotePayload(payload); !ok {
			return errors.New("终局选择无效")
		}
	case "game_play_audio":
		var request struct {
			AudioType string `json:"audioType"`
		}
		if json.Unmarshal(payload, &request) != nil || request.AudioType == "" {
			return errors.New("音效类型无效")
		}
	}

	phases := map[string]domain.RoomStatus{
		"game_place_tile":        domain.RoomStatusSetTile,
		"game_create_company":    domain.RoomStatusCreateCompany,
		"game_buy_stock":         domain.RoomStatusBuyStock,
		"game_merging_selection": domain.RoomStatusMergingSelection,
		"game_merging_settle":    domain.RoomStatusMergingSettle,
	}
	if phase, isAction := phases[command.Type]; isAction {
		if state.RoomStatus != phase {
			return errors.New("当前阶段不允许此操作")
		}
		if command.Type == "game_merging_settle" {
			if command.PlayerID != rules.CurrentMergeSettlePlayer(room) {
				return errors.New("请等待当前股东完成并购结算")
			}
		} else if command.PlayerID != state.CurrentPlayer {
			return errors.New("请等待你的回合")
		}
		if state.Players[command.PlayerID] == nil {
			return errors.New("玩家数据尚未就绪")
		}
	}

	switch command.Type {
	case "game_place_tile":
		var request rules.PlaceTilePayload
		tile := (*domain.Tile)(nil)
		if json.Unmarshal(payload, &request) == nil {
			tile = state.BoardTiles[request.TileKey]
		}
		if tile == nil || tile.Belong != "" ||
			!slices.Contains(state.Players[command.PlayerID].Tiles, request.TileKey) {
			return errors.New("这块地不能放置")
		}
	case "game_create_company":
		var request rules.CreateCompanyPayload
		company := (*domain.CompanyState)(nil)
		if json.Unmarshal(payload, &request) == nil {
			company = state.Companies[request.Company]
		}
		if company == nil || company.Tiles != 0 {
			return errors.New("请选择尚未成立的公司")
		}
	case "game_merging_selection":
		var request rules.MergingSelectionPayload
		if json.Unmarshal(payload, &request) != nil ||
			!slices.Contains(state.MergingSelection.MainCompany, request.MainCompany) {
			return errors.New("请选择可存续的公司")
		}
	case "game_buy_stock":
		var request rules.BuyStockPayload
		if json.Unmarshal(payload, &request) != nil {
			return errors.New("购股数据无效")
		}
		total, cost := 0, 0
		for name, count := range request.Stocks {
			company := state.Companies[name]
			if company == nil || count < 0 || count > 3 || count > company.StockTotal ||
				(count > 0 && company.Tiles == 0) {
				return errors.New("股票数量或库存已变化")
			}
			total += count
			cost += count * company.StockPrice
		}
		if total > 3 || cost > state.Players[command.PlayerID].Money {
			return errors.New("最多购买三股，且不能超过现金余额")
		}
	case "game_merging_settle":
		var request rules.MergingSettlePayload
		if json.Unmarshal(payload, &request) != nil {
			return errors.New("合并数据无效")
		}
		pending := false
		for _, settlement := range state.MergeSettleData {
			pending = pending || slices.Contains(settlement.Hoders, command.PlayerID)
		}
		if !pending {
			return errors.New("你已完成本次结算")
		}
		exchanged := 0
		seen := map[string]bool{}
		for _, action := range request.Actions {
			settlement, exists := state.MergeSettleData[action.Company]
			holding := state.Players[command.PlayerID].Stocks[action.Company]
			if !exists || seen[action.Company] ||
				!slices.Contains(settlement.Hoders, command.PlayerID) ||
				action.SellAmount < 0 || action.SellAmount > holding ||
				action.ExchangeAmount < 0 ||
				action.ExchangeAmount > holding-action.SellAmount ||
				action.ExchangeAmount%2 != 0 {
				return errors.New("卖出与兑换数量超出持股，或兑换不是二换一")
			}
			seen[action.Company] = true
			exchanged += action.ExchangeAmount / 2
		}
		main := state.Companies[state.MergeMainCompany]
		if main == nil || exchanged > main.StockTotal {
			return errors.New("存续公司库存已变化，请调整兑换数量")
		}
	}
	return nil
}

func acquireCommandCompletedTurn(
	commandType string,
	previous domain.RoomStatus,
	previousPlayer string,
	state *domain.GameState,
) bool {
	if state == nil {
		return false
	}
	if state.RoomStatus == domain.RoomStatusSetTile && previous != domain.RoomStatusSetTile {
		return true
	}
	if previousPlayer == "" {
		return false
	}
	if previousPlayer != state.CurrentPlayer {
		return true
	}
	if state.RoomStatus != domain.RoomStatusSetTile {
		return false
	}
	return commandType == "turn_timeout" || commandType == "game_place_tile"
}

func normalizeAcquireSeats(room *domain.Room) {
	used := make(map[int]bool, len(room.Connections))
	for _, playerID := range room.PlayerSeq {
		player := room.Connections[playerID]
		if player == nil {
			continue
		}
		if player.SeatIndex < 0 || player.SeatIndex >= room.State.MaxPlayers || used[player.SeatIndex] {
			player.SeatIndex = firstAvailableAcquireSeat(room, used)
		}
		used[player.SeatIndex] = true
	}
	sort.SliceStable(room.PlayerSeq, func(i, j int) bool {
		left := room.Connections[room.PlayerSeq[i]]
		right := room.Connections[room.PlayerSeq[j]]
		if left == nil || right == nil {
			return room.PlayerSeq[i] < room.PlayerSeq[j]
		}
		if left.SeatIndex != right.SeatIndex {
			return left.SeatIndex < right.SeatIndex
		}
		return left.PlayerID < right.PlayerID
	})
}

func firstAvailableAcquireSeat(room *domain.Room, alreadyUsed ...map[int]bool) int {
	used := map[int]bool{}
	if len(alreadyUsed) > 0 {
		for seat, occupied := range alreadyUsed[0] {
			used[seat] = occupied
		}
	} else {
		for _, player := range room.Connections {
			if player != nil {
				used[player.SeatIndex] = true
			}
		}
	}
	for seat := 0; seat < room.State.MaxPlayers; seat++ {
		if !used[seat] {
			return seat
		}
	}
	return len(room.Connections)
}

func acquireSeatOccupied(room *domain.Room, seatIndex int) bool {
	for _, player := range room.Connections {
		if player != nil && player.SeatIndex == seatIndex {
			return true
		}
	}
	return false
}

func addAcquirePlayer(room *domain.Room, player *roomcore.PlayerConn) error {
	if player == nil {
		return errors.New("player is required")
	}
	if acquireSeatOccupied(room, player.SeatIndex) {
		return fmt.Errorf("seat %d is occupied", player.SeatIndex)
	}
	room.Connections[player.PlayerID] = player
	room.PlayerSeq = append(room.PlayerSeq, player.PlayerID)
	normalizeAcquireSeats(room)
	return nil
}

func acquireSeatIndex(player *roomcore.PlayerConn, fallback int) int {
	if player == nil {
		return fallback
	}
	return player.SeatIndex
}
