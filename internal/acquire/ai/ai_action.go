package roompkg

import (
	"encoding/json"
	"sort"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
)

type aiActionKind string

const (
	aiActionPlaceTile      aiActionKind = "place_tile"
	aiActionCreateCompany  aiActionKind = "create_company"
	aiActionBuyStock       aiActionKind = "buy_stock"
	aiActionMergeSelection aiActionKind = "merge_selection"
	aiActionMergeSettle    aiActionKind = "merge_settle"
)

type aiAction struct {
	Kind          aiActionKind
	TileKey       string
	Company       string
	Stocks        map[string]int
	MainCompany   string
	SettleActions []domain.MergingSettleItem
}

func payloadForAction(action aiAction) (string, []byte, bool) {
	var (
		cmdType string
		body    map[string]interface{}
	)
	switch action.Kind {
	case aiActionPlaceTile:
		if action.TileKey == "" {
			return "", nil, false
		}
		cmdType = "game_place_tile"
		body = map[string]interface{}{"tileKey": action.TileKey}
	case aiActionCreateCompany:
		if action.Company == "" {
			return "", nil, false
		}
		cmdType = "game_create_company"
		body = map[string]interface{}{"company": action.Company}
	case aiActionBuyStock:
		if action.Stocks == nil {
			return "", nil, false
		}
		cmdType = "game_buy_stock"
		body = map[string]interface{}{"stocks": action.Stocks}
	case aiActionMergeSelection:
		if action.MainCompany == "" {
			return "", nil, false
		}
		cmdType = "game_merging_selection"
		body = map[string]interface{}{"mainCompany": action.MainCompany}
	case aiActionMergeSettle:
		cmdType = "game_merging_settle"
		body = map[string]interface{}{"actions": action.SettleActions}
	default:
		return "", nil, false
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", nil, false
	}
	return cmdType, payload, true
}

func actionFromCommand(cmdType string, payload []byte) (aiAction, bool) {
	switch cmdType {
	case "game_place_tile":
		var body struct {
			TileKey string `json:"tileKey"`
		}
		if err := json.Unmarshal(payload, &body); err != nil || body.TileKey == "" {
			return aiAction{}, false
		}
		return aiAction{Kind: aiActionPlaceTile, TileKey: body.TileKey}, true
	case "game_create_company":
		var body struct {
			Company string `json:"company"`
		}
		if err := json.Unmarshal(payload, &body); err != nil || body.Company == "" {
			return aiAction{}, false
		}
		return aiAction{Kind: aiActionCreateCompany, Company: body.Company}, true
	case "game_buy_stock":
		var body struct {
			Stocks map[string]int `json:"stocks"`
		}
		if err := json.Unmarshal(payload, &body); err != nil || body.Stocks == nil {
			return aiAction{}, false
		}
		return aiAction{Kind: aiActionBuyStock, Stocks: body.Stocks}, true
	case "game_merging_selection":
		var body struct {
			MainCompany string `json:"mainCompany"`
		}
		if err := json.Unmarshal(payload, &body); err != nil || body.MainCompany == "" {
			return aiAction{}, false
		}
		return aiAction{Kind: aiActionMergeSelection, MainCompany: body.MainCompany}, true
	case "game_merging_settle":
		var body struct {
			Actions []domain.MergingSettleItem `json:"actions"`
		}
		if err := json.Unmarshal(payload, &body); err != nil {
			return aiAction{}, false
		}
		return aiAction{Kind: aiActionMergeSettle, SettleActions: body.Actions}, true
	default:
		return aiAction{}, false
	}
}

func enumerateActions(r *domain.Room, playerID string, status domain.RoomStatus, explicitMainCompany []string, limit int) []aiAction {
	if r == nil || r.State == nil || r.State.Players == nil {
		return nil
	}
	switch status {
	case domain.RoomStatusSetTile:
		return limitActions(enumeratePlaceTileActions(r, playerID), limit)
	case domain.RoomStatusCreateCompany:
		return limitActions(enumerateCreateCompanyActions(r), limit)
	case domain.RoomStatusBuyStock:
		return limitActions(enumerateBuyStockActions(r, playerID), limit)
	case domain.RoomStatusMergingSelection:
		return limitActions(enumerateMergeSelectionActions(r, explicitMainCompany), limit)
	case domain.RoomStatusMergingSettle:
		return limitActions(enumerateMergeSettleActions(r, playerID), limit)
	default:
		return nil
	}
}

func enumeratePlaceTileActions(r *domain.Room, playerID string) []aiAction {
	player := r.State.Players[playerID]
	if player == nil {
		return nil
	}
	actions := make([]aiAction, 0, len(player.Tiles))
	for _, tile := range player.Tiles {
		if _, _, illegal := simulateTilePlacement(r, tile); illegal {
			continue
		}
		actions = append(actions, aiAction{Kind: aiActionPlaceTile, TileKey: tile})
	}
	sort.Slice(actions, func(i, j int) bool {
		ci, bi, _ := simulateTilePlacement(r, actions[i].TileKey)
		cj, bj, _ := simulateTilePlacement(r, actions[j].TileKey)
		si := scoreTilePlacement(r, playerID, actions[i].TileKey, ci, bi)
		sj := scoreTilePlacement(r, playerID, actions[j].TileKey, cj, bj)
		if si != sj {
			return si > sj
		}
		return actions[i].TileKey < actions[j].TileKey
	})
	return actions
}

func enumerateCreateCompanyActions(r *domain.Room) []aiAction {
	if r.State.Companies == nil {
		return nil
	}
	names := make([]string, 0, len(r.State.Companies))
	for name, info := range r.State.Companies {
		if info != nil && info.Tiles == 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	actions := make([]aiAction, 0, len(names))
	preferred := chooseCompanyForAI(r)
	if preferred != "" {
		actions = append(actions, aiAction{Kind: aiActionCreateCompany, Company: preferred})
	}
	for _, name := range names {
		if name == preferred {
			continue
		}
		actions = append(actions, aiAction{Kind: aiActionCreateCompany, Company: name})
	}
	return actions
}

func enumerateBuyStockActions(r *domain.Room, playerID string) []aiAction {
	player := r.State.Players[playerID]
	if player == nil {
		return nil
	}
	type candidate struct {
		name  string
		price int
		score int
	}
	candidates := []candidate{}
	for name, info := range r.State.Companies {
		if info == nil || info.Tiles == 0 || info.StockTotal <= 0 || info.StockPrice <= 0 {
			continue
		}
		if player.Stocks[name] >= aiMaxStockPerCo || info.StockPrice > player.Money {
			continue
		}
		candidates = append(candidates, candidate{name: name, price: info.StockPrice, score: scoreBuyCandidate(r, playerID, name)})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].name < candidates[j].name
	})
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}

	actions := []aiAction{{Kind: aiActionBuyStock, Stocks: map[string]int{}}}
	var walk func(idx, remainingCount, remainingMoney int, stocks map[string]int)
	walk = func(idx, remainingCount, remainingMoney int, stocks map[string]int) {
		if idx >= len(candidates) || remainingCount == 0 {
			if len(stocks) > 0 {
				actions = append(actions, aiAction{Kind: aiActionBuyStock, Stocks: cloneIntMap(stocks)})
			}
			return
		}
		c := candidates[idx]
		maxAffordable := remainingMoney / c.price
		maxStockRoom := aiMaxStockPerCo - player.Stocks[c.name]
		maxInventory := r.State.Companies[c.name].StockTotal
		maxCount := min(maxAffordable, maxStockRoom, min2(maxInventory, remainingCount))
		for count := 0; count <= maxCount; count++ {
			if count > 0 {
				stocks[c.name] = count
			} else {
				delete(stocks, c.name)
			}
			walk(idx+1, remainingCount-count, remainingMoney-count*c.price, stocks)
		}
		delete(stocks, c.name)
	}
	walk(0, aiMaxStockPerTurn, player.Money, map[string]int{})

	sort.Slice(actions, func(i, j int) bool {
		return buyActionScore(r, playerID, actions[i]) > buyActionScore(r, playerID, actions[j])
	})
	return actions
}

func enumerateMergeSelectionActions(r *domain.Room, explicitMainCompany []string) []aiAction {
	candidates := explicitMainCompany
	if len(candidates) == 0 {
		candidates = r.State.MergingSelection.MainCompany
	}
	names := append([]string(nil), candidates...)
	sort.Strings(names)
	actions := make([]aiAction, 0, len(names))
	preferred := chooseMergingSelectionForAI(r, r.State.CurrentPlayer, names)
	if preferred != "" {
		actions = append(actions, aiAction{Kind: aiActionMergeSelection, MainCompany: preferred})
	}
	for _, name := range names {
		if name == preferred {
			continue
		}
		actions = append(actions, aiAction{Kind: aiActionMergeSelection, MainCompany: name})
	}
	return actions
}

func enumerateMergeSettleActions(r *domain.Room, playerID string) []aiAction {
	actions := []aiAction{{
		Kind:          aiActionMergeSettle,
		SettleActions: chooseMergingSettleForAI(r, playerID),
	}}
	if allSell := buildMergeSettleVariant(r, playerID, false); len(allSell) > 0 {
		actions = append(actions, aiAction{Kind: aiActionMergeSettle, SettleActions: allSell})
	}
	if exchange := buildMergeSettleVariant(r, playerID, true); len(exchange) > 0 {
		actions = append(actions, aiAction{Kind: aiActionMergeSettle, SettleActions: exchange})
	}
	return dedupeActions(actions)
}

func buildMergeSettleVariant(r *domain.Room, playerID string, exchange bool) []domain.MergingSettleItem {
	if r.State.MergeSettleData == nil {
		return nil
	}
	main := r.State.Companies[r.State.MergeMainCompany]
	if main == nil {
		return nil
	}
	result := []domain.MergingSettleItem{}
	remainingMainStock := main.StockTotal
	for _, company := range mergeSettlementCompanies(r) {
		count := r.State.Players[playerID].Stocks[company]
		if count <= 0 {
			continue
		}
		item := domain.MergingSettleItem{Company: company}
		if exchange {
			maxEven := count
			if maxEven%2 != 0 {
				maxEven--
			}
			item.ExchangeAmount = min2(maxEven, remainingMainStock*2)
			item.SellAmount = count - item.ExchangeAmount
			remainingMainStock -= item.ExchangeAmount / 2
		} else {
			item.SellAmount = count
		}
		result = append(result, item)
	}
	return result
}

func buyActionScore(r *domain.Room, playerID string, action aiAction) int {
	if action.Kind != aiActionBuyStock {
		return 0
	}
	score := 0
	for company, count := range action.Stocks {
		for i := 0; i < count; i++ {
			score += scoreBuyCandidate(r, playerID, company)
		}
	}
	return score
}

func limitActions(actions []aiAction, limit int) []aiAction {
	if limit <= 0 || len(actions) <= limit {
		return actions
	}
	return actions[:limit]
}

func cloneIntMap(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func dedupeActions(actions []aiAction) []aiAction {
	seen := make(map[string]struct{}, len(actions))
	result := make([]aiAction, 0, len(actions))
	for _, action := range actions {
		_, payload, ok := payloadForAction(action)
		if !ok {
			continue
		}
		key := action.Kind + ":" + aiActionKind(payload)
		if _, exists := seen[string(key)]; exists {
			continue
		}
		seen[string(key)] = struct{}{}
		result = append(result, action)
	}
	return result
}
