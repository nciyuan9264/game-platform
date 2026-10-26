package roompkg

import "testing"

func TestOnlineWeightsUseRuntimeValues(t *testing.T) {
	want := AIWeights{
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
	if onlineAIWeights != want {
		t.Fatalf("online weights = %+v, want %+v", onlineAIWeights, want)
	}
}

func TestNormalizeAIPlayerCount(t *testing.T) {
	tests := map[int]int{
		0: 2,
		1: 2,
		2: 2,
		4: 4,
		6: 6,
		7: 6,
	}
	for input, want := range tests {
		if got := normalizeAIPlayerCount(input); got != want {
			t.Fatalf("normalizeAIPlayerCount(%d) = %d, want %d", input, got, want)
		}
	}
}
