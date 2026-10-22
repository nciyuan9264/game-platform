package roompkg

import "testing"

func TestCandidateWeightSetsIncludeDefaultAndVariants(t *testing.T) {
	sets := candidateWeightSets()
	if len(sets) < 5 {
		t.Fatalf("candidate weight sets = %d, want at least 5", len(sets))
	}
	if sets[0] != defaultAIWeights {
		t.Fatalf("first candidate should be default weights")
	}
	seenVariant := false
	for _, weights := range sets[1:] {
		if weights != defaultAIWeights {
			seenVariant = true
			break
		}
	}
	if !seenVariant {
		t.Fatalf("expected at least one non-default candidate")
	}
}

func TestOnlineAndLocalWeightsAreNamedSeparately(t *testing.T) {
	online, ok := AIWeightsByName("online")
	if !ok {
		t.Fatalf("online weights not found")
	}
	if online != OnlineAIWeightsForArena() {
		t.Fatalf("online weights should match arena baseline")
	}

	local, ok := AIWeightsByName("controlFocused")
	if !ok {
		t.Fatalf("controlFocused weights not found")
	}
	if local == online {
		t.Fatalf("controlFocused should differ from online weights")
	}
}

func TestOnlineWeightsUsePromotedBonusFocusedValues(t *testing.T) {
	online := OnlineAIWeightsForArena()
	if online.MajorityBonus != 2 || online.MinorityBonus != 2 {
		t.Fatalf("online bonus weights = majority:%d minority:%d, want 2/2", online.MajorityBonus, online.MinorityBonus)
	}
}

func TestOnlineWeightsUsePromotedAuto002Values(t *testing.T) {
	online := OnlineAIWeightsForArena()
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
	if online != want {
		t.Fatalf("online weights = %+v, want auto-002 %+v", online, want)
	}
}

func TestGenerateTuningCandidatesIncludesDeterministicLocalCandidates(t *testing.T) {
	first := GenerateTuningCandidates(8, 42)
	second := GenerateTuningCandidates(8, 42)
	if len(first) != 8 {
		t.Fatalf("candidates = %d, want 8", len(first))
	}
	if len(second) != len(first) {
		t.Fatalf("second candidates = %d, want %d", len(second), len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("candidate %d not deterministic:\nfirst=%+v\nsecond=%+v", i, first[i], second[i])
		}
	}
	if first[0].Name != "controlFocused" {
		t.Fatalf("first candidate = %q, want controlFocused", first[0].Name)
	}
}

func TestGenerateTuningCandidatesIncludesMultiplayerRankWeights(t *testing.T) {
	candidates := GenerateTuningCandidates(20, 42)
	seenRankWeights := false
	for _, candidate := range candidates {
		weights := candidate.Weights
		if weights.TopRankBonus > 0 && weights.LeaderGapPenalty > 0 && weights.SecondPlacePenalty > 0 {
			seenRankWeights = true
			break
		}
	}
	if !seenRankWeights {
		t.Fatalf("generated candidates did not include multiplayer rank weights: %+v", candidates)
	}
}

func TestGenerateGridTuningCandidatesIsDeterministicAndLimited(t *testing.T) {
	first := GenerateGridTuningCandidates(5)
	second := GenerateGridTuningCandidates(5)
	if len(first) != 5 {
		t.Fatalf("grid candidates = %d, want 5", len(first))
	}
	if len(second) != len(first) {
		t.Fatalf("second grid candidates = %d, want %d", len(second), len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("grid candidate %d not deterministic:\nfirst=%+v\nsecond=%+v", i, first[i], second[i])
		}
	}
	if first[0].Name != "grid-balanced-001" {
		t.Fatalf("first grid candidate = %q, want grid-balanced-001", first[0].Name)
	}
}

func TestGenerateGridTuningCandidatesCoversMultiplayerCombinations(t *testing.T) {
	candidates := GenerateGridTuningCandidates(0)
	if len(candidates) != 768 {
		t.Fatalf("grid candidates = %d, want 768", len(candidates))
	}
	seenHighTopRank := false
	seenLeaderBenefit := false
	for _, candidate := range candidates {
		if candidate.Weights.TopRankBonus == 80000 {
			seenHighTopRank = true
		}
		if candidate.Weights.LeaderBenefitPenalty == 2 {
			seenLeaderBenefit = true
		}
	}
	if !seenHighTopRank || !seenLeaderBenefit {
		t.Fatalf("grid missing expected multiplayer ranges: highTopRank=%v leaderBenefit=%v", seenHighTopRank, seenLeaderBenefit)
	}
}
