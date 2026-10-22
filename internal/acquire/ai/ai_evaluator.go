package roompkg

import (
	"math"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
)

type AIWeights struct {
	// Cash 控制评估器对当前现金的重视程度。
	Cash int
	// StockValue 控制评估器对已持有股票按当前股价估值的重视程度。
	StockValue int
	// MajorityBonus 控制第一大股东奖金估值的权重。
	MajorityBonus int
	// MinorityBonus 控制第二大股东奖金估值的权重。
	MinorityBonus int
	// ControlLead 奖励相对最强对手的持股领先优势。
	ControlLead int
	// SafeCompanyControl 对持有安全公司股票给予固定奖励。
	SafeCompanyControl int
	// LeaderPenalty 惩罚对手在估算总资产上领先的局面。
	LeaderPenalty int
	// TerminalWinBonus 在终局时强奖励胜利、强惩罚失败。
	TerminalWinBonus int
	// OpponentResponseCost 控制 2-ply 搜索中对手最佳反击带来的扣分。
	OpponentResponseCost int
	// TopRankBonus 多人局中自己成为全场第一时的奖励。
	TopRankBonus int
	// LeaderGapPenalty 多人局中自己落后当前第一名时，按资产差扣分。
	LeaderGapPenalty int
	// SecondPlacePenalty 多人局中自己排第二但未成为第一时的固定惩罚。
	SecondPlacePenalty int
	// LeaderBenefitPenalty 多人局中领先者相对自己拉开差距时的额外惩罚。
	LeaderBenefitPenalty int
}

func evaluateRoomForPlayer(r *domain.Room, playerID string, weights AIWeights) int {
	if r == nil || r.State == nil || r.State.Players == nil || r.State.Companies == nil {
		return math.MinInt / 4
	}
	player := r.State.Players[playerID]
	if player == nil {
		return math.MinInt / 4
	}

	score := player.Money * weights.Cash
	for company, info := range r.State.Companies {
		if info == nil || info.Tiles == 0 {
			continue
		}
		count := player.Stocks[company]
		score += count * info.StockPrice * weights.StockValue
		bonus := shareholderBonusValue(r, playerID, company)
		if holderRankValue(r, playerID, company) == 2 {
			score += bonus * weights.MajorityBonus
		} else {
			score += bonus * weights.MinorityBonus
		}
		score += controlLeadValue(r, playerID, company) * weights.ControlLead
		if info.Tiles >= aiSafeCompanyTiles && holderRankValue(r, playerID, company) > 0 {
			score += weights.SafeCompanyControl
		}
	}

	if leader, lead := leadingOpponent(r, playerID); leader != "" && lead > 0 {
		score -= lead * weights.LeaderPenalty
		for company := range r.State.Companies {
			score -= shareholderBonusValue(r, leader, company) * weights.LeaderPenalty / 4
		}
	}
	score += multiplayerRankScore(r, playerID, weights)

	if r.State.RoomStatus == domain.RoomStatusEnd {
		myValue := estimatePlayerTotalValue(r, playerID)
		bestOther := math.MinInt
		for pid := range r.State.Players {
			if pid == playerID {
				continue
			}
			bestOther = max(bestOther, estimatePlayerTotalValue(r, pid))
		}
		if myValue >= bestOther {
			score += weights.TerminalWinBonus
		} else {
			score -= weights.TerminalWinBonus
		}
	}

	return score
}

func multiplayerRankScore(r *domain.Room, playerID string, weights AIWeights) int {
	if len(r.State.Players) < 3 {
		return 0
	}
	if weights.TopRankBonus == 0 && weights.LeaderGapPenalty == 0 && weights.SecondPlacePenalty == 0 && weights.LeaderBenefitPenalty == 0 {
		return 0
	}
	myValue := estimatePlayerTotalValue(r, playerID)
	bestOther := math.MinInt
	playersAhead := 0
	for pid := range r.State.Players {
		if pid == playerID {
			continue
		}
		value := estimatePlayerTotalValue(r, pid)
		if value > bestOther {
			bestOther = value
		}
		if value > myValue {
			playersAhead++
		}
	}
	if bestOther == math.MinInt {
		return 0
	}

	score := 0
	if playersAhead == 0 {
		score += weights.TopRankBonus
		return score
	}
	gap := bestOther - myValue
	if gap > 0 {
		score -= gap * weights.LeaderGapPenalty
		score -= gap * weights.LeaderBenefitPenalty
	}
	if playersAhead == 1 {
		score -= weights.SecondPlacePenalty
	}
	return score
}

func shareholderBonusValue(r *domain.Room, playerID string, company string) int {
	info := r.State.Companies[company]
	if info == nil || info.Tiles == 0 {
		return 0
	}
	si := stockInfoOf(company, info.Tiles)
	if si == nil {
		return 0
	}
	ranking := holdersRanking(r, company)
	if len(ranking) == 0 {
		return 0
	}
	myCount := 0
	if player := r.State.Players[playerID]; player != nil {
		myCount = player.Stocks[company]
	}
	if myCount <= 0 {
		return 0
	}

	firstCount := ranking[0].Count
	firstGroup := 0
	secondCount := 0
	secondGroup := 0
	for _, item := range ranking {
		switch {
		case item.Count == firstCount:
			firstGroup++
		case secondCount == 0:
			secondCount = item.Count
			secondGroup = 1
		case item.Count == secondCount:
			secondGroup++
		}
	}
	if myCount == firstCount {
		if firstGroup > 1 {
			return (si.BonusFirst + si.BonusSecond) / firstGroup
		}
		return si.BonusFirst
	}
	if secondCount > 0 && myCount == secondCount && secondGroup > 0 {
		return si.BonusSecond / secondGroup
	}
	return 0
}

func controlLeadValue(r *domain.Room, playerID string, company string) int {
	myCount := 0
	if player := r.State.Players[playerID]; player != nil {
		myCount = player.Stocks[company]
	}
	if myCount <= 0 {
		return 0
	}
	bestOther := 0
	for pid, player := range r.State.Players {
		if pid == playerID || player == nil {
			continue
		}
		bestOther = max(bestOther, player.Stocks[company])
	}
	return myCount - bestOther
}
