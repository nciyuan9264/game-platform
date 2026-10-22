package roompkg

import (
	"fmt"

	"golang.org/x/exp/rand"
)

type NamedAIWeights struct {
	Name    string    `json:"name"`
	Weights AIWeights `json:"weights"`
}

var onlineAIWeights = AIWeights{
	Cash:                 2,
	StockValue:           1,
	MajorityBonus:        2,
	MinorityBonus:        2,
	ControlLead:          120,
	SafeCompanyControl:   40,
	LeaderPenalty:        3,
	TerminalWinBonus:     10000,
	OpponentResponseCost: 3,
}

var defaultAIWeights = onlineAIWeights

func onlineAIWeightsForPlayers(players int) (string, AIWeights) {
	switch normalizeArenaPlayers(players) {
	case 3:
		return "online_3p_auto002_fallback", onlineAIWeights
	case 4:
		return "online_4p_auto002_fallback", onlineAIWeights
	case 5:
		return "online_5p_auto002_fallback", onlineAIWeights
	case 6:
		return "online_6p_auto002_fallback", onlineAIWeights
	default:
		return "online_2p_auto002", onlineAIWeights
	}
}

func OnlineAIWeightsForArena() AIWeights {
	return onlineAIWeights
}

func DefaultAIWeightsForArena() AIWeights {
	return onlineAIWeights
}

func AIWeightsByName(name string) (AIWeights, bool) {
	switch name {
	case "", "online", "default":
		return onlineAIWeights, true
	}
	for _, candidate := range localAIWeightPresets() {
		if candidate.Name == name {
			return candidate.Weights, true
		}
	}
	return AIWeights{}, false
}

func candidateWeightSets() []AIWeights {
	presets := localAIWeightPresets()
	sets := make([]AIWeights, 0, len(presets)+1)
	sets = append(sets, onlineAIWeights)
	for _, preset := range presets {
		sets = append(sets, preset.Weights)
	}
	return sets
}

func localAIWeightPresets() []NamedAIWeights {
	controlFocused := onlineAIWeights
	controlFocused.ControlLead = 180
	controlFocused.SafeCompanyControl = 140

	defensive := onlineAIWeights
	defensive.LeaderPenalty = 2
	defensive.OpponentResponseCost = 2

	bonusFocused := onlineAIWeights
	bonusFocused.MajorityBonus = 2
	bonusFocused.MinorityBonus = 2

	cashConservative := onlineAIWeights
	cashConservative.Cash = 2
	cashConservative.StockValue = 1

	stockAggressive := onlineAIWeights
	stockAggressive.StockValue = 2
	stockAggressive.ControlLead = 150

	multiplayerRanked := onlineAIWeights
	multiplayerRanked.TopRankBonus = 25000
	multiplayerRanked.LeaderGapPenalty = 2
	multiplayerRanked.SecondPlacePenalty = 8000
	multiplayerRanked.LeaderBenefitPenalty = 1

	leaderBlocker := onlineAIWeights
	leaderBlocker.LeaderPenalty = 5
	leaderBlocker.LeaderGapPenalty = 3
	leaderBlocker.LeaderBenefitPenalty = 2
	leaderBlocker.TopRankBonus = 15000

	comebackFocused := onlineAIWeights
	comebackFocused.StockValue = 2
	comebackFocused.ControlLead = 180
	comebackFocused.TopRankBonus = 30000
	comebackFocused.SecondPlacePenalty = 12000

	return []NamedAIWeights{
		{Name: "controlFocused", Weights: controlFocused},
		{Name: "defensive", Weights: defensive},
		{Name: "bonusFocused", Weights: bonusFocused},
		{Name: "cashConservative", Weights: cashConservative},
		{Name: "stockAggressive", Weights: stockAggressive},
		{Name: "multiplayerRanked", Weights: multiplayerRanked},
		{Name: "leaderBlocker", Weights: leaderBlocker},
		{Name: "comebackFocused", Weights: comebackFocused},
	}
}

func GenerateTuningCandidates(count int, seed uint64) []NamedAIWeights {
	if count <= 0 {
		count = len(localAIWeightPresets())
	}
	presets := localAIWeightPresets()
	candidates := make([]NamedAIWeights, 0, count)
	for _, preset := range presets {
		if len(candidates) >= count {
			return candidates
		}
		candidates = append(candidates, preset)
	}

	controlLeadValues := []int{80, 120, 160, 200}
	safeControlValues := []int{40, 80, 120, 160}
	leaderPenaltyValues := []int{1, 2, 3}
	opponentCostValues := []int{1, 2, 3}
	stockValueValues := []int{1, 2}
	cashValues := []int{1, 2}
	topRankBonusValues := []int{0, 15000, 25000, 35000}
	leaderGapPenaltyValues := []int{0, 1, 2, 3}
	secondPlacePenaltyValues := []int{0, 6000, 10000, 14000}
	leaderBenefitPenaltyValues := []int{0, 1, 2}

	rng := rand.New(rand.NewSource(seed))
	for len(candidates) < count {
		weights := onlineAIWeights
		weights.ControlLead = controlLeadValues[rng.Intn(len(controlLeadValues))]
		weights.SafeCompanyControl = safeControlValues[rng.Intn(len(safeControlValues))]
		weights.LeaderPenalty = leaderPenaltyValues[rng.Intn(len(leaderPenaltyValues))]
		weights.OpponentResponseCost = opponentCostValues[rng.Intn(len(opponentCostValues))]
		weights.StockValue = stockValueValues[rng.Intn(len(stockValueValues))]
		weights.Cash = cashValues[rng.Intn(len(cashValues))]
		weights.TopRankBonus = topRankBonusValues[rng.Intn(len(topRankBonusValues))]
		weights.LeaderGapPenalty = leaderGapPenaltyValues[rng.Intn(len(leaderGapPenaltyValues))]
		weights.SecondPlacePenalty = secondPlacePenaltyValues[rng.Intn(len(secondPlacePenaltyValues))]
		weights.LeaderBenefitPenalty = leaderBenefitPenaltyValues[rng.Intn(len(leaderBenefitPenaltyValues))]
		candidates = append(candidates, NamedAIWeights{
			Name:    fmt.Sprintf("auto-%03d", len(candidates)-len(presets)+1),
			Weights: weights,
		})
	}
	return candidates
}

func GenerateGridTuningCandidates(limit int) []NamedAIWeights {
	type gridTemplate struct {
		name    string
		weights AIWeights
	}
	templates := []gridTemplate{
		{
			name:    "balanced",
			weights: onlineAIWeights,
		},
		{
			name: "stockAggressive",
			weights: AIWeights{
				Cash:                 1,
				StockValue:           2,
				MajorityBonus:        onlineAIWeights.MajorityBonus,
				MinorityBonus:        onlineAIWeights.MinorityBonus,
				ControlLead:          160,
				SafeCompanyControl:   80,
				LeaderPenalty:        2,
				TerminalWinBonus:     onlineAIWeights.TerminalWinBonus,
				OpponentResponseCost: 2,
			},
		},
		{
			name: "leaderBlocker",
			weights: AIWeights{
				Cash:                 1,
				StockValue:           2,
				MajorityBonus:        onlineAIWeights.MajorityBonus,
				MinorityBonus:        onlineAIWeights.MinorityBonus,
				ControlLead:          120,
				SafeCompanyControl:   80,
				LeaderPenalty:        5,
				TerminalWinBonus:     onlineAIWeights.TerminalWinBonus,
				OpponentResponseCost: 2,
			},
		},
		{
			name: "controlStock",
			weights: AIWeights{
				Cash:                 1,
				StockValue:           3,
				MajorityBonus:        onlineAIWeights.MajorityBonus,
				MinorityBonus:        onlineAIWeights.MinorityBonus,
				ControlLead:          200,
				SafeCompanyControl:   120,
				LeaderPenalty:        3,
				TerminalWinBonus:     onlineAIWeights.TerminalWinBonus,
				OpponentResponseCost: 2,
			},
		},
	}
	topRankBonusValues := []int{0, 25000, 50000, 80000}
	leaderGapPenaltyValues := []int{0, 1, 2, 4}
	secondPlacePenaltyValues := []int{0, 8000, 15000, 30000}
	leaderBenefitPenaltyValues := []int{0, 1, 2}

	total := len(templates) * len(topRankBonusValues) * len(leaderGapPenaltyValues) * len(secondPlacePenaltyValues) * len(leaderBenefitPenaltyValues)
	if limit <= 0 || limit > total {
		limit = total
	}
	candidates := make([]NamedAIWeights, 0, limit)
	for _, tmpl := range templates {
		templateIndex := 1
		for _, topRankBonus := range topRankBonusValues {
			for _, leaderGapPenalty := range leaderGapPenaltyValues {
				for _, secondPlacePenalty := range secondPlacePenaltyValues {
					for _, leaderBenefitPenalty := range leaderBenefitPenaltyValues {
						weights := tmpl.weights
						weights.TopRankBonus = topRankBonus
						weights.LeaderGapPenalty = leaderGapPenalty
						weights.SecondPlacePenalty = secondPlacePenalty
						weights.LeaderBenefitPenalty = leaderBenefitPenalty
						candidates = append(candidates, NamedAIWeights{
							Name:    fmt.Sprintf("grid-%s-%03d", tmpl.name, templateIndex),
							Weights: weights,
						})
						if len(candidates) >= limit {
							return candidates
						}
						templateIndex++
					}
				}
			}
		}
	}
	return candidates
}
