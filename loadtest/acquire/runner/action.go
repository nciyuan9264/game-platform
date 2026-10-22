package main

import (
	"encoding/json"
	"fmt"
	"sort"
)

type roomSync struct {
	Type       string     `json:"type"`
	PlayerID   string     `json:"playerId"`
	PlayerData playerData `json:"playerData"`
	RoomData   roomData   `json:"roomData"`
	TempData   tempData   `json:"tempData"`
}

type playerData struct {
	Money  int            `json:"money"`
	Stocks map[string]int `json:"stocks"`
	Tiles  []string       `json:"tiles"`
}

type roomData struct {
	CurrentPlayer string                 `json:"currentPlayer"`
	GameStatus    string                 `json:"gameStatus"`
	CompanyInfo   map[string]companyInfo `json:"companyInfo"`
}

type companyInfo struct {
	Tiles      int `json:"tiles"`
	StockTotal int `json:"stockTotal"`
	StockPrice int `json:"stockPrice"`
}

type tempData struct {
	MergeSelectionTemp mergingSelection      `json:"merge_selection_temp"`
	MergeSettleData    map[string]settleData `json:"mergeSettleData"`
}

type mergingSelection struct {
	MainCompany []string `json:"mainCompany"`
}

type settleData struct {
	Hoders []string `json:"hoders"`
}

type settleAction struct {
	Company        string `json:"company"`
	SellAmount     int    `json:"sellAmount"`
	ExchangeAmount int    `json:"exchangeAmount"`
}

func decideAction(sync roomSync) ([]byte, bool) {
	if sync.PlayerID == "" || sync.RoomData.CurrentPlayer != sync.PlayerID {
		return nil, false
	}

	switch sync.RoomData.GameStatus {
	case "setTile":
		if len(sync.PlayerData.Tiles) == 0 {
			return nil, false
		}
		return marshalMessage("game_place_tile", map[string]any{"tileKey": sync.PlayerData.Tiles[0]})
	case "createCompany":
		for _, company := range sortedCompanies(sync.RoomData.CompanyInfo) {
			if sync.RoomData.CompanyInfo[company].Tiles == 0 {
				return marshalMessage("game_create_company", map[string]any{"company": company})
			}
		}
	case "buyStock":
		return marshalMessage("game_buy_stock", map[string]any{"stocks": chooseStocks(sync.PlayerData.Money, sync.RoomData.CompanyInfo)})
	case "mergingSelection":
		if len(sync.TempData.MergeSelectionTemp.MainCompany) == 0 {
			return nil, false
		}
		return marshalMessage("game_merging_selection", map[string]any{
			"mainCompany": sync.TempData.MergeSelectionTemp.MainCompany[0],
		})
	case "mergingSettle":
		return marshalMessage("game_merging_settle", map[string]any{"actions": chooseSettleActions(sync)})
	}

	return nil, false
}

func actionKey(sync roomSync) string {
	key := fmt.Sprintf("%s|%s|%s", sync.RoomData.GameStatus, sync.RoomData.CurrentPlayer, sync.PlayerID)
	switch sync.RoomData.GameStatus {
	case "setTile":
		if len(sync.PlayerData.Tiles) > 0 {
			key += "|" + sync.PlayerData.Tiles[0]
		}
	case "createCompany":
		key += "|" + firstEmptyCompany(sync.RoomData.CompanyInfo)
	case "buyStock":
		key += "|" + stableJSON(chooseStocks(sync.PlayerData.Money, sync.RoomData.CompanyInfo))
	case "mergingSelection":
		if len(sync.TempData.MergeSelectionTemp.MainCompany) > 0 {
			key += "|" + sync.TempData.MergeSelectionTemp.MainCompany[0]
		}
	case "mergingSettle":
		key += "|" + stableJSON(chooseSettleActions(sync))
	}
	return key
}

func stableJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func firstEmptyCompany(companies map[string]companyInfo) string {
	for _, company := range sortedCompanies(companies) {
		if companies[company].Tiles == 0 {
			return company
		}
	}
	return ""
}

func chooseStocks(money int, companies map[string]companyInfo) map[string]int {
	stocks := map[string]int{}
	remainingMoney := money
	remainingShares := 3
	for _, company := range sortedCompanies(companies) {
		info := companies[company]
		stocks[company] = 0
		if remainingShares == 0 || info.Tiles <= 0 || info.StockTotal <= 0 || info.StockPrice <= 0 {
			continue
		}
		canBuy := remainingMoney / info.StockPrice
		if canBuy > remainingShares {
			canBuy = remainingShares
		}
		if canBuy > info.StockTotal {
			canBuy = info.StockTotal
		}
		if canBuy <= 0 {
			continue
		}
		stocks[company] = canBuy
		remainingShares -= canBuy
		remainingMoney -= canBuy * info.StockPrice
	}
	return stocks
}

func chooseSettleActions(sync roomSync) []settleAction {
	actions := []settleAction{}
	for _, company := range sortedSettleCompanies(sync.TempData.MergeSettleData) {
		if sync.PlayerData.Stocks[company] <= 0 {
			continue
		}
		actions = append(actions, settleAction{
			Company:    company,
			SellAmount: sync.PlayerData.Stocks[company],
		})
	}
	return actions
}

func sortedCompanies(companies map[string]companyInfo) []string {
	names := make([]string, 0, len(companies))
	for name := range companies {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedSettleCompanies(data map[string]settleData) []string {
	names := make([]string, 0, len(data))
	for name := range data {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func marshalMessage(messageType string, payload map[string]any) ([]byte, bool) {
	msg := map[string]any{
		"type":    messageType,
		"payload": payload,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, false
	}
	return data, true
}
