package roompkg

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/nciyuan9264/game-platform/internal/acquire/arrayutil"
	"github.com/nciyuan9264/game-platform/internal/acquire/domain/data"
	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/acquire/logger"
	"github.com/nciyuan9264/game-platform/internal/acquire/roomcore"
	rules "github.com/nciyuan9264/game-platform/internal/acquire/rules"
	"github.com/nciyuan9264/game-platform/internal/acquire/utils"

	"math/rand/v2"
)

const (
	aiSafeCompanyTiles = 11 // 与 step1.go 一致：tile 数 >= 11 视为 safe（不可被吞）
	aiMaxStockPerCo    = 13 // 单家公司持股上限
	aiMaxStockPerTurn  = 3  // 每回合最多购股数
	aiFounderBonus     = 300
)

type VirtualConn struct {
	Room *domain.Room
}

// WriteMessage 实现ConnInterface接口
func (v *VirtualConn) WriteMessage(messageType int, data []byte) error {
	MaybeRunAIIfNeeded(v.Room, data)
	return nil
}

func (v *VirtualConn) ReadMessage() (messageType int, p []byte, err error) {
	return 0, nil, fmt.Errorf("virtual connection cannot read")
}

// Close 实现ConnInterface接口
func (v *VirtualConn) Close() error {
	// AI玩家不需要实际关闭连接
	return nil
}

// ----------------------------------------------------------------------------
// AI 评估器：只读 helper，不修改 r.State
// ----------------------------------------------------------------------------

// simulateTilePlacement 模拟把 tileKey 放到棋盘上后，会"邻接"哪些公司、Blank 块。
// 不修改 BoardTiles。illegal=true 表示该落子会同时连接 ≥2 家 safe 公司（≥11 tile），
// 这种落子在游戏侧只是空转，AI 应当回避。
func simulateTilePlacement(r *domain.Room, tileKey string) (companies map[string]struct{}, blankCount int, illegal bool) {
	companies = make(map[string]struct{})
	for _, adj := range data.GetAdjacentTileKeys(tileKey) {
		t, ok := r.State.BoardTiles[adj]
		if !ok || t == nil {
			continue
		}
		switch t.Belong {
		case "":
			continue
		case "Blank":
			blankCount++
		default:
			companies[t.Belong] = struct{}{}
		}
	}
	if len(companies) >= 2 {
		safeCount := 0
		for c := range companies {
			if info, ok := r.State.Companies[c]; ok && info.Tiles >= aiSafeCompanyTiles {
				safeCount++
			}
		}
		if safeCount >= 2 {
			illegal = true
		}
	}
	return
}

// holdersRanking 返回某公司持股从高到低的 (playerID, count) 排序。
func holdersRanking(r *domain.Room, company string) []struct {
	PlayerID string
	Count    int
} {
	type entry = struct {
		PlayerID string
		Count    int
	}
	out := make([]entry, 0, len(r.State.Players))
	for pid, p := range r.State.Players {
		if p == nil {
			continue
		}
		if c := p.Stocks[company]; c > 0 {
			out = append(out, entry{PlayerID: pid, Count: c})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// dominanceScore 描述 me 在 company 中的"主导地位"：
//
//	2 = 第一大股东（含并列），1 = 第二大股东（含并列），0 = 暂无人持股，-1 = 落后
func dominanceScore(r *domain.Room, me string, company string) int {
	rank := holdersRanking(r, company)
	if len(rank) == 0 {
		return 0
	}
	myCount := 0
	if p, ok := r.State.Players[me]; ok && p != nil {
		myCount = p.Stocks[company]
	}
	if myCount == 0 {
		return -1
	}
	first := rank[0].Count
	if myCount >= first {
		return 2
	}
	// second
	second := 0
	for _, e := range rank {
		if e.Count < first {
			second = e.Count
			break
		}
	}
	if myCount >= second && second > 0 {
		return 1
	}
	return -1
}

// stockInfoOf 安全地按公司名 + tile 数取价格档位信息。
func stockInfoOf(company string, tileCount int) *utils.StockInfo {
	t, ok := utils.ParseCompanyType(company)
	if !ok {
		return nil
	}
	return utils.GetStockInfo(t, tileCount)
}

// bandStepGain 估算公司 tile 数增加 delta 后，价格 / 第一大奖金 的跨档涨幅。
func bandStepGain(company string, currentTiles, delta int) (priceDelta int, bonusFirstDelta int) {
	cur := stockInfoOf(company, currentTiles)
	nxt := stockInfoOf(company, currentTiles+delta)
	if cur == nil || nxt == nil {
		return 0, 0
	}
	return nxt.Price - cur.Price, nxt.BonusFirst - cur.BonusFirst
}

func estimatePlayerStockValue(r *domain.Room, playerID string) int {
	if r == nil || r.State == nil || r.State.Players == nil || r.State.Companies == nil {
		return 0
	}
	p, ok := r.State.Players[playerID]
	if !ok || p == nil {
		return 0
	}
	total := 0
	for company, count := range p.Stocks {
		info := r.State.Companies[company]
		if info == nil || info.Tiles == 0 || count <= 0 {
			continue
		}
		total += count * info.StockPrice
	}
	return total
}

func estimatePlayerTotalValue(r *domain.Room, playerID string) int {
	if r == nil || r.State == nil || r.State.Players == nil {
		return 0
	}
	p, ok := r.State.Players[playerID]
	if !ok || p == nil {
		return 0
	}
	return p.Money + estimatePlayerStockValue(r, playerID)
}

func leadingOpponent(r *domain.Room, me string) (playerID string, lead int) {
	if r == nil || r.State == nil || r.State.Players == nil {
		return "", 0
	}
	myValue := estimatePlayerTotalValue(r, me)
	bestHuman := ""
	bestHumanValue := math.MinInt
	bestAny := ""
	bestAnyValue := math.MinInt
	for pid := range r.State.Players {
		if pid == me {
			continue
		}
		value := estimatePlayerTotalValue(r, pid)
		if value > bestAnyValue {
			bestAnyValue = value
			bestAny = pid
		}
		pc, ok := r.Connections[pid]
		if !ok || pc == nil || !pc.AI {
			if value > bestHumanValue {
				bestHumanValue = value
				bestHuman = pid
			}
		}
	}
	if bestHuman != "" {
		return bestHuman, bestHumanValue - myValue
	}
	if bestAny != "" {
		return bestAny, bestAnyValue - myValue
	}
	return "", 0
}

func companyHolderCounts(r *domain.Room, company string) map[string]int {
	counts := make(map[string]int)
	if r == nil || r.State == nil || r.State.Players == nil {
		return counts
	}
	for pid, p := range r.State.Players {
		if p == nil || p.Stocks == nil {
			continue
		}
		if count := p.Stocks[company]; count > 0 {
			counts[pid] = count
		}
	}
	return counts
}

func holderRankValueFromCounts(counts map[string]int, playerID string) int {
	myCount := counts[playerID]
	if myCount <= 0 {
		return 0
	}
	first := 0
	second := 0
	for _, count := range counts {
		switch {
		case count > first:
			second = first
			first = count
		case count < first && count > second:
			second = count
		}
	}
	if myCount >= first {
		return 2
	}
	if second > 0 && myCount >= second {
		return 1
	}
	return 0
}

func holderRankValue(r *domain.Room, playerID string, company string) int {
	return holderRankValueFromCounts(companyHolderCounts(r, company), playerID)
}

func rankValueAfterStockDelta(r *domain.Room, playerID string, company string, delta int) int {
	counts := companyHolderCounts(r, company)
	counts[playerID] += delta
	if counts[playerID] <= 0 {
		delete(counts, playerID)
	}
	return holderRankValueFromCounts(counts, playerID)
}

func opponentBenefitPenalty(r *domain.Room, me string, company string, amount int) int {
	leader, lead := leadingOpponent(r, me)
	if leader == "" {
		return 0
	}
	rank := holderRankValue(r, leader, company)
	if rank == 0 {
		return 0
	}
	penalty := amount * rank
	if lead > 0 {
		penalty += lead / 20
	}
	return penalty
}

func mergerBonusValue(r *domain.Room, playerID string, company string) int {
	info := r.State.Companies[company]
	if info == nil {
		return 0
	}
	si := stockInfoOf(company, info.Tiles)
	if si == nil {
		return 0
	}
	switch holderRankValue(r, playerID, company) {
	case 2:
		return si.BonusFirst
	case 1:
		return si.BonusSecond
	default:
		return 0
	}
}

// ----------------------------------------------------------------------------
// 决策函数
// ----------------------------------------------------------------------------

// scoreTilePlacement 给一张候选 tile 评分，分数越高越值得下。
func scoreTilePlacement(r *domain.Room, me, _ string, companies map[string]struct{}, blankCount int) int {
	if len(companies) == 0 && blankCount == 0 {
		return 1 // 孤立落点，保底分
	}

	if len(companies) == 1 {
		var only string
		for c := range companies {
			only = c
		}
		info := r.State.Companies[only]
		dom := dominanceScore(r, me, only)
		leader, lead := leadingOpponent(r, me)
		leaderRank := holderRankValue(r, leader, only)
		// 扩张后该公司新增 tile 数 ≈ blankCount + 1（含放下的这块）
		priceGain, bonusGain := bandStepGain(only, info.Tiles, blankCount+1)
		score := dom*300 + priceGain + bonusGain/8
		if dom <= 0 {
			// 给对手扩张是负价值
			score -= info.StockPrice
		} else {
			score += info.StockPrice * dom
		}
		if leader != "" && leaderRank > 0 {
			score -= info.StockPrice*leaderRank + opponentBenefitPenalty(r, me, only, info.StockPrice/2)
			if lead > 0 {
				score -= lead / 15
			}
		}
		if dom > 0 && leaderRank == 0 {
			score += info.StockPrice / 2
		}
		return score
	}

	if len(companies) >= 2 {
		// 触发并购：优先自己拿分，避免给领先者送分。
		score := 0
		// 选 tile 最多的为存活方近似
		mainTiles := -1
		totalTiles := 1 + blankCount
		var main string
		for c := range companies {
			t := r.State.Companies[c].Tiles
			totalTiles += t
			if t > mainTiles {
				mainTiles = t
				main = c
			}
		}
		leader, lead := leadingOpponent(r, me)
		// 被吞公司的奖金期望
		for c := range companies {
			if c == main {
				continue
			}
			info := r.State.Companies[c]
			myBenefit := mergerBonusValue(r, me, c)
			leaderBenefit := 0
			if leader != "" {
				leaderBenefit = mergerBonusValue(r, leader, c)
			}
			// 把被吞公司的持股按当前价折现
			if p, ok := r.State.Players[me]; ok && p != nil {
				myBenefit += p.Stocks[c] * info.StockPrice
			}
			if leader != "" {
				if p, ok := r.State.Players[leader]; ok && p != nil {
					leaderBenefit += p.Stocks[c] * info.StockPrice
				}
			}
			score += (myBenefit - leaderBenefit) / 8
		}
		// 对存活方的持股估值
		if main != "" {
			info := r.State.Companies[main]
			futurePrice := info.StockPrice
			if si := stockInfoOf(main, totalTiles); si != nil {
				futurePrice = si.Price
			}
			if p, ok := r.State.Players[me]; ok && p != nil {
				score += p.Stocks[main] * futurePrice / 8
			}
			if leader != "" {
				if p, ok := r.State.Players[leader]; ok && p != nil {
					score -= p.Stocks[main] * futurePrice / 8
				}
				if holderRankValue(r, leader, main) > 0 && lead > 0 {
					score -= lead / 20
				}
			}
			if holderRankValue(r, me, main) > 0 {
				score += futurePrice
			}
		}
		return score
	}

	// 仅连接 Blank：可能触发"创建公司"。
	if blankCount >= 1 {
		hasUncreated := false
		for _, info := range r.State.Companies {
			if info.Tiles == 0 {
				hasUncreated = true
				break
			}
		}
		if hasUncreated {
			// 估算创建后的初始体量（连同 tileKey 自身的 Blank 连通块）
			predictedSize := blankCount + 1
			score := aiFounderBonus + predictedSize*70
			activeCompanies := 0
			for _, info := range r.State.Companies {
				if info != nil && info.Tiles > 0 {
					activeCompanies++
				}
			}
			if p, ok := r.State.Players[me]; ok && p != nil && p.Money < 1200 && activeCompanies > 0 {
				score -= 120
			}
			return score
		}
	}
	return 1
}

func chooseTileForAI(room *domain.Room, playerID string) string {
	tiles := room.State.Players[playerID].Tiles
	if len(tiles) == 0 {
		return ""
	}

	bestScore := math.MinInt
	best := ""
	legalFallback := ""
	for _, t := range tiles {
		companies, blanks, illegal := simulateTilePlacement(room, t)
		if illegal {
			continue
		}
		if legalFallback == "" {
			legalFallback = t
		}
		s := scoreTilePlacement(room, playerID, t, companies, blanks)
		if s > bestScore {
			bestScore = s
			best = t
		}
	}
	if best != "" {
		return best
	}
	if legalFallback != "" {
		return legalFallback
	}
	// 全部 tile 都会触发违规并购：不得已随机
	return tiles[rand.IntN(len(tiles))]
}

func chooseCompanyForAI(r *domain.Room) string {
	var uncreated []string
	for company, info := range r.State.Companies {
		if info.Tiles == 0 {
			uncreated = append(uncreated, company)
		}
	}
	if len(uncreated) == 0 {
		return ""
	}
	sort.Strings(uncreated)

	priority1 := []string{"Continental", "Imperial"}
	priority2 := []string{"American", "Festival", "Worldwide"}
	var p1, p2, p3 []string
	for _, c := range uncreated {
		switch {
		case arrayutil.StringInSlice(c, priority1):
			p1 = append(p1, c)
		case arrayutil.StringInSlice(c, priority2):
			p2 = append(p2, c)
		default:
			p3 = append(p3, c)
		}
	}

	// 当 LastTileKey 所在的 Blank 连通块较大时，优先选 Premium 公司（奖金跨档更大）
	predictedSize := 0
	if r.State.LastTileKey != "" {
		predictedSize = len(data.GetConnectedTiles(r, r.State.LastTileKey))
	}
	if predictedSize >= 6 && len(p1) > 0 {
		return p1[0]
	}

	if len(p1) > 0 {
		return p1[0]
	}
	if len(p2) > 0 {
		return p2[0]
	}
	return p3[0]
}

// scoreBuyCandidate 为购股候选打 ROI 分。调用方会临时写入已买入的股票快照，
// 因此这里可以安全地用当前 r.State 评估"再买 1 股"后的排名变化。
func scoreBuyCandidate(r *domain.Room, me, company string) int {
	info := r.State.Companies[company]
	si := stockInfoOf(company, info.Tiles)
	if si == nil {
		return math.MinInt
	}
	dom := dominanceScore(r, me, company)
	beforeRank := holderRankValue(r, me, company)
	afterRank := rankValueAfterStockDelta(r, me, company, 1)
	// 进档时 1st 奖金跨档收益（粗略估计）
	_, bonusGain := bandStepGain(company, info.Tiles, 1)
	roi := bonusGain / 5
	roi += dom * info.StockPrice
	roi -= info.StockPrice
	if afterRank > beforeRank {
		roi += (afterRank - beforeRank) * info.StockPrice * 2
		if afterRank == 2 {
			roi += si.BonusFirst / 8
		} else {
			roi += si.BonusSecond / 8
		}
	}
	if beforeRank == 0 && afterRank > 0 {
		roi += info.StockPrice
	}
	// safe 公司加成（稳定收益）
	if info.Tiles >= aiSafeCompanyTiles {
		roi += info.StockPrice / 2
	}
	leader, lead := leadingOpponent(r, me)
	if leader != "" {
		leaderRank := holderRankValue(r, leader, company)
		leaderCount := 0
		myCount := 0
		if p := r.State.Players[leader]; p != nil {
			leaderCount = p.Stocks[company]
		}
		if p := r.State.Players[me]; p != nil {
			myCount = p.Stocks[company]
		}
		if leaderRank > 0 {
			if afterRank >= leaderRank && myCount+1 >= leaderCount {
				roi += info.StockPrice * 2
				if lead > 0 {
					roi += lead / 12
				}
			}
			if afterRank == 0 && leaderCount-myCount > 3 {
				roi -= info.StockPrice + lead/20
			}
		}
	}
	return roi
}

func chooseStocksToBuyForAI(r *domain.Room, playerID string) map[string]int {
	playerInfo := r.State.Players[playerID]
	money := playerInfo.Money
	playerStock := playerInfo.Stocks

	// 用临时持股快照在每次买入后递增，使 dominanceScore 在循环内反映买后状态
	tmpStock := make(map[string]int, len(playerStock))
	for k, v := range playerStock {
		tmpStock[k] = v
	}

	result := map[string]int{}
	stockCount := 0
	for stockCount < aiMaxStockPerTurn {
		// 候选：已创建、有库存、未达上限、买得起
		type candidate struct {
			Name  string
			Score int
			Price int
		}
		var options []candidate
		// 把临时持股写回再评估
		original := r.State.Players[playerID].Stocks
		r.State.Players[playerID].Stocks = tmpStock
		for name, info := range r.State.Companies {
			remain := info.StockTotal - result[name]
			if info.Tiles == 0 || remain <= 0 || tmpStock[name] >= aiMaxStockPerCo || info.StockPrice > money {
				continue
			}
			s := scoreBuyCandidate(r, playerID, name)
			options = append(options, candidate{Name: name, Score: s, Price: info.StockPrice})
		}
		r.State.Players[playerID].Stocks = original

		if len(options) == 0 {
			break
		}
		sort.Slice(options, func(i, j int) bool {
			if options[i].Score != options[j].Score {
				return options[i].Score > options[j].Score
			}
			return options[i].Price > options[j].Price // 同分时优先贵的（潜力更大）
		})
		best := options[0]
		if best.Score <= 0 {
			break
		}
		result[best.Name]++
		tmpStock[best.Name]++
		money -= best.Price
		stockCount++
		if money <= 0 {
			break
		}
	}

	return result
}

func chooseMergingSettleForAI(r *domain.Room, playerID string) []domain.MergingSettleItem {
	result := []domain.MergingSettleItem{}
	mainCompanyInfo := r.State.Companies[r.State.MergeMainCompany]
	remainingMainStock := mainCompanyInfo.StockTotal

	for _, companyKey := range mergeSettlementCompanies(r) {
		count := r.State.Players[playerID].Stocks[companyKey]
		if count == 0 {
			continue
		}
		company := r.State.Companies[companyKey]

		exchangeAmount := 0
		sellAmount := count

		maxEven := count
		if maxEven%2 != 0 {
			maxEven--
		}
		maxCanExchange := remainingMainStock * 2
		maxEven = min2(maxEven, maxCanExchange)
		beforeRank := holderRankValue(r, playerID, r.State.MergeMainCompany)
		afterRank := rankValueAfterStockDelta(r, playerID, r.State.MergeMainCompany, maxEven/2)
		leader, _ := leadingOpponent(r, playerID)
		leaderRank := holderRankValue(r, leader, r.State.MergeMainCompany)

		// 2 股换 1 股：mainPrice >= 2*absorbedPrice 时换股净收益 ≥ 0。
		// 若换股能进入奖金区，允许以略低价格换股；若只会强化领先者控制，则卖出拿现金。
		shouldExchange := mainCompanyInfo.StockPrice >= 2*company.StockPrice
		if !shouldExchange && afterRank > beforeRank && mainCompanyInfo.StockPrice >= company.StockPrice {
			shouldExchange = true
		}
		if leaderRank > 0 && afterRank == 0 && mainCompanyInfo.StockPrice < 2*company.StockPrice {
			shouldExchange = false
		}

		if shouldExchange && maxEven > 0 {
			exchangeAmount = maxEven
			sellAmount = count - exchangeAmount
			remainingMainStock -= exchangeAmount / 2
		}

		result = append(result, domain.MergingSettleItem{
			Company:        companyKey,
			SellAmount:     sellAmount,
			ExchangeAmount: exchangeAmount,
		})
	}

	return result
}

func mergeSettlementCompanies(r *domain.Room) []string {
	companies := make([]string, 0, len(r.State.MergeSettleData))
	for company := range r.State.MergeSettleData {
		companies = append(companies, company)
	}
	sort.Strings(companies)
	return companies
}

// AutoSettleDisconnectedHolder settles consecutive offline shareholders at
// the head of the merger queue without skipping an online player or AI.
func AutoSettleDisconnectedHolder(r *domain.Room) {
	if r == nil || r.State == nil || r.State.RoomStatus != domain.RoomStatusMergingSettle {
		return
	}
	for r.State.RoomStatus == domain.RoomStatusMergingSettle {
		target := rules.CurrentMergeSettlePlayer(r)
		if target == "" {
			return
		}
		player := r.Connections[target]
		if player != nil && (player.AI || player.Online) {
			return
		}

		actions := chooseMergingSettleForAI(r, target)
		payload, err := json.Marshal(map[string]interface{}{"actions": actions})
		if err != nil {
			r.Log().Error("自动结算编码失败", logger.F("room_id", r.ID), logger.F("player_id", target), logger.F("error", err))
			return
		}

		r.Log().Info("玩家离线，自动替其完成并购结算", logger.F("room_id", r.ID), logger.F("player_id", target))
		rules.HandleMergingSettleMessage(r, domain.Command{
			Type:     "game_merging_settle",
			PlayerID: target,
			Payload:  payload,
		})
	}
}

// CurrentTimeoutPlayer follows the active online human shareholder during a
// merger and the normal turn owner in every other phase.
func CurrentTimeoutPlayer(r *domain.Room) string {
	if r == nil || r.State == nil {
		return ""
	}
	if r.State.RoomStatus == domain.RoomStatusMergingSettle {
		playerID := rules.CurrentMergeSettlePlayer(r)
		player := r.Connections[playerID]
		if player != nil && !player.AI && player.Online {
			return playerID
		}
		return ""
	}
	return r.State.CurrentPlayer
}

func chooseMergingSelectionForAI(r *domain.Room, playerID string, mainCompany []string) string {
	if len(mainCompany) == 0 {
		return ""
	}

	candidates := append([]string(nil), mainCompany...)
	sort.Strings(candidates)
	mergeTilesSum := 0
	for _, c := range mainCompany {
		if info := r.State.Companies[c]; info != nil {
			mergeTilesSum += info.Tiles
		}
	}
	for _, c := range r.State.MergingSelection.OtherCompany {
		if info := r.State.Companies[c]; info != nil && info.Tiles < aiSafeCompanyTiles {
			mergeTilesSum += info.Tiles
		}
	}

	res := ""
	bestGain := math.MinInt
	leader, _ := leadingOpponent(r, playerID)
	for _, companyKey := range candidates {
		// 合并后 tile 数 ≈ 所有本次会并入的非 safe 公司 tile 总和。
		predicted := mergeTilesSum
		si := stockInfoOf(companyKey, predicted)
		if si == nil {
			continue
		}
		myStocks := r.State.Players[playerID].Stocks[companyKey]
		aiGain := myStocks * si.Price
		switch holderRankValue(r, playerID, companyKey) {
		case 2:
			aiGain += si.BonusFirst
		case 1:
			aiGain += si.BonusSecond
		}
		leaderGain := 0
		if leader != "" {
			if p := r.State.Players[leader]; p != nil {
				leaderGain += p.Stocks[companyKey] * si.Price
			}
			switch holderRankValue(r, leader, companyKey) {
			case 2:
				leaderGain += si.BonusFirst
			case 1:
				leaderGain += si.BonusSecond
			}
		}
		gain := aiGain - leaderGain
		if gain > bestGain {
			bestGain = gain
			res = companyKey
		}
	}
	if res == "" {
		// 兜底：取数组首项
		return candidates[0]
	}
	return res
}

// buildAIActionMsg 根据当前 RoomStatus 选出"AI/超时"应当投递的命令 type+payload。
// 真实入口只输出命令；搜索模拟必须在克隆房间内执行，不能向真实 CmdCh 投递命令。
// 返回 ok=false 表示该状态下没有可投递的动作。
//   - mergingSelection 阶段需要的 mainCompany 候选优先取 explicitMainCompany（来自前端 tempData），
//     若为空则回退到 r.State.MergingSelection.MainCompany（用于 turn_timeout 路径）。
func buildAIActionMsg(r *domain.Room, playerID string, status domain.RoomStatus, explicitMainCompany []string) (cmdType string, payload []byte, ok bool) {
	searchConfig, searchConfigName, aiPlayers := onlineAISearchConfigForRoom(r)
	if action, found := chooseBestActionBySearch(r, playerID, status, explicitMainCompany, searchConfig); found {
		if cmdType, payload, ok := payloadForAction(action); ok {
			r.Log().Info("AI 使用搜索策略出手",
				logger.F("room_id", r.ID),
				logger.F("player_id", playerID),
				logger.F("status", status),
				logger.F("cmd_type", cmdType),
				logger.F("ai_strategy", "search"),
				logger.F("ai_players", aiPlayers),
				logger.F("ai_config", searchConfigName),
				logger.F("ai_weights", searchConfigName),
				logger.F("depth", searchConfig.Depth),
				logger.F("beam_width", searchConfig.BeamWidth),
				logger.F("action_limit", searchConfig.ActionLimit),
			)
			return cmdType, payload, true
		}
	}
	cmdType, payload, ok = buildAIActionMsgByHeuristic(r, playerID, status, explicitMainCompany)
	if ok {
		r.Log().Info("AI 使用启发式策略出手",
			logger.F("room_id", r.ID),
			logger.F("player_id", playerID),
			logger.F("status", status),
			logger.F("cmd_type", cmdType),
			logger.F("ai_strategy", "heuristic"),
			logger.F("ai_players", aiPlayers),
			logger.F("ai_config", searchConfigName),
			logger.F("ai_weights", searchConfigName),
			logger.F("reason", "search_not_found"),
		)
	}
	return cmdType, payload, ok
}

func buildAIActionMsgByHeuristic(r *domain.Room, playerID string, status domain.RoomStatus, explicitMainCompany []string) (cmdType string, payload []byte, ok bool) {
	switch status {
	case domain.RoomStatusSetTile:
		tile := chooseTileForAI(r, playerID)
		if tile == "" {
			return "", nil, false
		}
		data, err := json.Marshal(map[string]interface{}{"tileKey": tile})
		if err != nil {
			return "", nil, false
		}
		return "game_place_tile", data, true
	case domain.RoomStatusCreateCompany:
		company := chooseCompanyForAI(r)
		if company == "" {
			return "", nil, false
		}
		data, err := json.Marshal(map[string]interface{}{"company": company})
		if err != nil {
			return "", nil, false
		}
		return "game_create_company", data, true
	case domain.RoomStatusBuyStock:
		stocks := chooseStocksToBuyForAI(r, playerID)
		data, err := json.Marshal(map[string]interface{}{"stocks": stocks})
		if err != nil {
			return "", nil, false
		}
		return "game_buy_stock", data, true
	case domain.RoomStatusMergingSelection:
		mainCompany := explicitMainCompany
		if len(mainCompany) == 0 {
			mainCompany = r.State.MergingSelection.MainCompany
		}
		selection := chooseMergingSelectionForAI(r, playerID, mainCompany)
		if selection == "" {
			// 兜底：从已创建公司里挑一个 Tiles>0 的，保证能继续推进。
			selection = anyActiveCompany(r)
		}
		if selection == "" {
			return "", nil, false
		}
		data, err := json.Marshal(map[string]interface{}{"mainCompany": selection})
		if err != nil {
			return "", nil, false
		}
		return "game_merging_selection", data, true
	case domain.RoomStatusMergingSettle:
		settle := chooseMergingSettleForAI(r, playerID)
		data, err := json.Marshal(map[string]interface{}{"actions": settle})
		if err != nil {
			return "", nil, false
		}
		return "game_merging_settle", data, true
	case domain.RoomStatusMerging:
		// merging 是过渡态，正常会被 handler 立刻切走；落到此处说明状态机异常，交给系统级兜底。
		return "", nil, false
	default:
		return "", nil, false
	}
}

// anyActiveCompany 兜底：从已创建（Tiles>0）的公司里取一个名字（按字典序）。
func anyActiveCompany(r *domain.Room) string {
	if r == nil || r.State == nil || r.State.Companies == nil {
		return ""
	}
	names := make([]string, 0, len(r.State.Companies))
	for name, info := range r.State.Companies {
		if info != nil && info.Tiles > 0 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[0]
}

// BuildTurnTimeoutCommand 思考超时时由 roomcore 调用。
// 与 MaybeRunAIIfNeeded 共用 buildAIActionMsg，但 PlayerID 用真实玩家 ID，且不修改身份。
func BuildTurnTimeoutCommand(r *domain.Room, playerID string) (roomcore.Command, bool) {
	if r == nil || r.State == nil || r.State.RoomStatus == domain.RoomStatusEnd {
		return roomcore.Command{}, false
	}
	cmdType, payload, ok := buildAIActionMsg(r, playerID, r.State.RoomStatus, nil)
	if !ok {
		// 兜底：发出 turn_timeout 强制切人，避免房间永久卡死。
		return roomcore.Command{
			Type:     "turn_timeout",
			PlayerID: playerID,
			Payload:  []byte("{}"),
			Conn:     &VirtualConn{Room: r},
		}, true
	}
	return roomcore.Command{
		Type:     cmdType,
		PlayerID: playerID,
		Payload:  payload,
		Conn:     &VirtualConn{Room: r},
	}, true
}

func MaybeRunAIIfNeeded(r *domain.Room, message []byte) bool {
	var msg map[string]interface{}
	if err := json.Unmarshal(message, &msg); err != nil {
		r.Log().Error("AI 消息格式错误", logger.F("room_id", r.ID), logger.F("error", err))
		return false
	}

	// 提取当前玩家
	roomData, ok := msg["roomData"].(map[string]interface{})
	if !ok {
		return false
	}
	currentPlayerID, ok := roomData["currentPlayer"].(string)
	if !ok || currentPlayerID == "" {
		return false
	}

	// 提取当前状态
	gameStatusStr, ok := roomData["gameStatus"].(string)
	if !ok || gameStatusStr == "" {
		return false
	}
	gameStatus := domain.RoomStatus(gameStatusStr)
	if gameStatus == domain.RoomStatusEnd {
		return false
	}

	playerId, ok := msg["playerId"].(string)
	if !ok || playerId == "" || (playerId != currentPlayerID && gameStatus != domain.RoomStatusMergingSettle) {
		return false
	}
	// 判断是否是 AI 玩家
	if gameStatus != domain.RoomStatusMergingSettle {
		isAI := false

		if r.Connections[currentPlayerID].AI {
			isAI = true
		}

		if !isAI {
			r.Log().Info("当前玩家 %s 不是 AI 玩家", logger.F("player_id", currentPlayerID))
			return false
		}
	}

	// 检查是否已经有 AI 行动在运行，防止多个 AI 玩家同时触发
	if r.AIRunning {
		r.Log().Info("AI 行动已在运行中，跳过重复触发", logger.F("room_id", r.ID), logger.F("player_id", playerId))
		return false
	}

	// 提取临时数据（合并选择）
	tempData, ok := msg["tempData"].(map[string]interface{})
	if !ok {
		r.Log().Error("tempData 类型错误", logger.F("room_id", r.ID))
		return false
	}

	var mainCompany []string
	if mergeSel, ok := tempData["merge_selection_temp"].(map[string]interface{}); ok {
		if raw, ok := mergeSel["mainCompany"]; ok {
			// 安全类型断言
			if arr, ok := raw.([]interface{}); ok {
				for _, item := range arr {
					if s, ok := item.(string); ok {
						mainCompany = append(mainCompany, s)
					}
				}
			}
		}
	}

	// mergingSettle 特殊校验
	if gameStatus == domain.RoomStatusMergingSettle {
		if rules.CurrentMergeSettlePlayer(r) != playerId {
			return false
		}
	}
	tiles := r.State.BoardTiles
	isAllTileUsed := true
	for _, tile := range tiles {
		if tile.Belong == "" {
			isAllTileUsed = false
		}
	}
	if isAllTileUsed {
		r.Log().Error("所有 tile 已被使用", logger.F("room_id", r.ID), logger.F("player_id", playerId))
	}

	// 标记 AI 行动已开始
	r.AIRunning = true
	r.Log().Info("当前是 AI 玩家的回合，准备延迟执行 AI 行动", logger.F("room_id", r.ID), logger.F("player_id", playerId), logger.F("game_status", gameStatus))

	// ---------- 在协程中延迟执行 ----------
	go func() {
		defer func() {
			// 无论如何都要重置标志
			r.AIRunning = false
		}()
		time.Sleep(5 * time.Second)
		if r.State == nil || r.State.RoomStatus == domain.RoomStatusEnd {
			return
		}

		cmdType, payload, ok := buildAIActionMsg(r, playerId, gameStatus, mainCompany)
		if !ok {
			r.Log().Warn("AI 未生成有效动作，发送 turn_timeout 兜底",
				logger.F("room_id", r.ID),
				logger.F("player_id", playerId),
				logger.F("game_status", gameStatus))
			r.CmdCh <- domain.Command{
				Type:     "turn_timeout",
				PlayerID: playerId,
				Payload:  []byte("{}"),
				Conn:     &VirtualConn{Room: r},
			}
			return
		}
		r.Log().Info("AI 发送消息", logger.F("room_id", r.ID), logger.F("player_id", playerId), logger.F("type", cmdType), logger.F("payload", string(payload)))

		// 向房间的命令通道发送消息，和玩家一样的处理方式
		r.CmdCh <- domain.Command{
			Type:     cmdType,
			PlayerID: playerId,
			Payload:  payload,
			Conn:     &VirtualConn{Room: r},
		}
	}()

	return true
}
