package roompkg

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
	switch normalizeAIPlayerCount(players) {
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

func normalizeAIPlayerCount(players int) int {
	switch {
	case players < 2:
		return 2
	case players > 6:
		return 6
	default:
		return players
	}
}
